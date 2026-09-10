package app

import (
	"errors"

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
}

// NewVault builds a vault. availability is the result of secrets.Available().
// A non-nil value makes the vault session-only.
func NewVault(os secrets.Keychain, availability error) *Vault {
	return &Vault{os: os, session: secrets.NewMemory(), persistent: os != nil && availability == nil}
}

// Persistent reports whether secrets survive a restart.
func (v *Vault) Persistent() bool { return v.persistent }

func (v *Vault) Get(id, key string) (string, error) {
	if s, err := v.session.Get(id, key); err == nil {
		return s, nil
	}
	if !v.persistent {
		return "", secrets.ErrNotFound
	}
	return v.os.Get(id, key)
}

func (v *Vault) Set(id, key, value string) error {
	if v.persistent {
		return v.os.Set(id, key, value)
	}
	return v.session.Set(id, key, value)
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
