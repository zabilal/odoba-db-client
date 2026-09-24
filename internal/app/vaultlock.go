package app

import (
	"errors"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
)

// Putting the app-level lock on and taking it off (NFR-S7).
//
// A lock is not a flag: what is in the keychain has to be resealed, every
// secret of every connection, and the same again in the open when it comes
// off. Each secret says for itself whether it is sealed, so a run that
// stops half way leaves a vault that still reads: what is sealed is opened
// with the key, and what is not is read as it is.

// LockedOut is a secret that could not be resealed, and the connection it
// belongs to. The rest were, and are named here so that whoever asked can
// say which were left.
type LockedOut struct {
	ConnectionID, Key string
	Err               error
}

func (l LockedOut) Error() string { return fmt.Sprintf("%s/%s: %v", l.ConnectionID, l.Key, l.Err) }

// VaultLocked reports whether there is a lock over the vault.
func (c *Connections) VaultLocked() bool { return c.vault.Lock().On() }

// VaultOpen reports whether the vault will hand out what it holds.
func (c *Connections) VaultOpen() bool { return c.vault.Lock().Open() }

// UnlockVault opens the lock for this session.
func (c *Connections) UnlockVault(pass string) error { return c.vault.Lock().Unlock(pass) }

// CloseVault shuts the lock again, leaving the secrets where they are and
// unreadable until somebody types the passphrase.
func (c *Connections) CloseVault() { c.vault.Lock().Close() }

// LockVault puts a lock on the vault, or changes the passphrase of the one
// there, and seals every secret with it.
//
// The vault must be open: sealing what cannot be read would be sealing
// nothing. What it says when it cannot read a passphrase is said before
// anything is written.
func (c *Connections) LockVault(pass string) error {
	if pass == "" {
		return errors.New("app: a lock needs a passphrase")
	}
	if !c.vault.Lock().Open() {
		return ErrLocked
	}
	held, err := c.heldSecrets()
	if err != nil {
		return err
	}
	set, err := NewSettings(pass)
	if err != nil {
		return err
	}
	lock := NewLock(&set)
	if err := lock.Unlock(pass); err != nil {
		return err
	}
	// The setting first, so that a run stopped half way leaves a lock that
	// opens what it has already sealed.
	if err := c.saveVaultLock(&set); err != nil {
		return err
	}
	c.vault.SetLock(lock)
	return c.rewrite(held)
}

// RemoveVaultLock takes the lock off, writing every secret back in the
// open. The passphrase is asked for again here because taking a lock off
// is the one thing somebody who has walked up to an unlocked window should
// not be able to do.
func (c *Connections) RemoveVaultLock(pass string) error {
	if !c.vault.Lock().On() {
		return nil
	}
	if err := c.vault.Lock().Unlock(pass); err != nil {
		return err
	}
	held, err := c.heldSecrets()
	if err != nil {
		return err
	}
	c.vault.SetLock(NewLock(nil))
	if err := c.rewrite(held); err != nil {
		return err
	}
	// The setting last: while a secret is still sealed, the lock that
	// opens it has to be there.
	return c.saveVaultLock(nil)
}

// heldSecrets is every secret the vault holds, read as it holds them.
//
// A secret that is not there is not an error: a connection whose password
// was never typed simply has none. One that is there and cannot be read is,
// because writing it back would lose it.
func (c *Connections) heldSecrets() (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	for _, conn := range c.List() {
		for _, k := range conn.Secrets {
			s, err := c.vault.Get(conn.ID, k)
			switch {
			case errors.Is(err, secrets.ErrNotFound):
				continue
			case err != nil:
				return nil, LockedOut{ConnectionID: conn.ID, Key: k, Err: err}
			}
			if out[conn.ID] == nil {
				out[conn.ID] = map[string]string{}
			}
			out[conn.ID][k] = s
		}
	}
	return out, nil
}

// rewrite writes every secret back through the vault as it is now.
func (c *Connections) rewrite(held map[string]map[string]string) error {
	var failed []error
	for id, m := range held {
		for k, s := range m {
			if err := c.vault.Set(id, k, s); err != nil {
				failed = append(failed, LockedOut{ConnectionID: id, Key: k, Err: err})
			}
		}
	}
	return errors.Join(failed...)
}

// saveVaultLock keeps what recognises the passphrase, and nothing else.
func (c *Connections) saveVaultLock(set *store.VaultLock) error {
	if c.settings == nil {
		return nil
	}
	return c.settings.Update(func(st *store.Settings) error {
		st.Vault = set
		return nil
	})
}
