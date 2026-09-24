package app

import (
	"errors"
	"sync"

	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
)

// Vault holds connection secrets: in the OS keychain when there is one, and in
// memory for this session when there is not (ADR-0009). A secret typed in for
// this session only always takes precedence, so it can stand in for a
// missing or stale keychain entry without overwriting it.
type Vault struct {
	os         secrets.Keychain
	session    *secrets.Memory
	persistent bool
	// lock is the app-level lock over what is persisted (NFR-S7). Nil is
	// no lock, which is what a vault has until somebody puts one on. It is
	// changed off the goroutine the window runs on — putting a lock on
	// takes a moment — and read on it, so it is behind a mutex.
	mu   sync.RWMutex
	lock *Lock
}

// NewVault builds a vault. availability is the result of secrets.Available().
// A non-nil value makes the vault session-only.
func NewVault(os secrets.Keychain, availability error) *Vault {
	return &Vault{os: os, session: secrets.NewMemory(), persistent: os != nil && availability == nil}
}

// Persistent reports whether secrets survive a restart.
func (v *Vault) Persistent() bool { return v.persistent }

// SetLock puts an app-level lock over what this vault persists, or takes
// one off with nil (NFR-S7).
func (v *Vault) SetLock(l *Lock) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lock = l
}

// Lock is the lock over this vault, which may be no lock at all.
func (v *Vault) Lock() *Lock {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.lock
}

// Locked reports whether the vault is holding its secrets back for want of
// a passphrase.
func (v *Vault) Locked() bool {
	l := v.Lock()
	return l.On() && !l.Open()
}

func (v *Vault) Get(id, key string) (string, error) {
	// A secret typed in for this session is not sealed and not held back:
	// it is in this process because somebody just typed it here.
	if s, err := v.session.Get(id, key); err == nil {
		return s, nil
	}
	if !v.persistent {
		return "", secrets.ErrNotFound
	}
	// Said here rather than left to the unsealing: a secret stored before
	// the lock went on is not sealed, and a locked vault that handed that
	// one over would be a locked vault handing a secret over.
	if v.Locked() {
		return "", ErrLocked
	}
	stored, err := v.os.Get(id, key)
	if err != nil {
		return "", err
	}
	return v.Lock().Unseal(stored)
}

func (v *Vault) Set(id, key, value string) error {
	if !v.persistent {
		return v.session.Set(id, key, value)
	}
	// Nothing says "locked" here: sealing with no key refuses, which is
	// the same answer for the same reason, and one place to give it.
	sealed, err := v.Lock().Seal(value)
	if err != nil {
		return err
	}
	return v.os.Set(id, key, sealed)
}

// SetSession holds a secret for this session only, never persisting it.
func (v *Vault) SetSession(id, key, value string) error { return v.session.Set(id, key, value) }

func (v *Vault) Delete(id, key string) error {
	v.session.Delete(id, key)
	if v.persistent {
		return v.os.Delete(id, key)
	}
	return nil
}

// setAll writes several secrets, undoing the ones already written if any
// write fails, so a partial failure leaves nothing behind.
func (v *Vault) setAll(id string, m map[string]string) error {
	var done []string
	for _, k := range sortedKeys(m) {
		if m[k] == "" {
			continue
		}
		if err := v.Set(id, k, m[k]); err != nil {
			for _, d := range done {
				v.Delete(id, d)
			}
			return err
		}
		done = append(done, k)
	}
	return nil
}

func (v *Vault) deleteAll(id string, keys []string) error {
	var errs []error
	for _, k := range keys {
		if err := v.Delete(id, k); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
