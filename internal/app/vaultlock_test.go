package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
)

// Putting the app-level lock on and taking it off (NFR-S7).

// Locking the vault seals what the keychain holds: the entry on its own is
// no longer the secret.
func TestLockingTheVaultSealsWhatIsThere(t *testing.T) {
	f := setup(t)
	conn, err := f.c.Create(draft("Orders"), map[string]string{"password": "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := f.kc.Get(conn.ID, "password"); got != "hunter2" {
		t.Fatalf("before the lock the keychain holds %q", got)
	}
	if err := f.c.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	stored, _ := f.kc.Get(conn.ID, "password")
	if !Sealed(stored) || strings.Contains(stored, "hunter2") {
		t.Errorf("the keychain holds %q", stored)
	}
	// And the application still reads it, because the lock is open.
	if got, err := f.c.vault.Get(conn.ID, "password"); err != nil || got != "hunter2" {
		t.Errorf("it read %q, %v", got, err)
	}
	if !f.c.VaultLocked() {
		t.Error("the vault says it has no lock")
	}
	// What is saved is enough to recognise a passphrase and no more.
	st := f.c.settings.Get()
	if st.Vault == nil || len(st.Vault.Salt) == 0 || len(st.Vault.Verifier) == 0 {
		t.Fatalf("the lock was saved as %+v", st.Vault)
	}
}

// A vault locked and shut holds everything back until the passphrase, and
// opens with it.
func TestAShutVaultOpensWithThePassphrase(t *testing.T) {
	f := setup(t)
	conn, err := f.c.Create(draft("Orders"), map[string]string{"password": "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.c.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	f.c.CloseVault()
	if f.c.VaultOpen() {
		t.Fatal("a shut vault says it is open")
	}
	if _, err := f.c.vault.Get(conn.ID, "password"); !errors.Is(err, ErrLocked) {
		t.Errorf("it read the secret anyway: %v", err)
	}
	if err := f.c.UnlockVault("wrong"); !errors.Is(err, ErrPassphrase) {
		t.Errorf("the wrong passphrase said %v", err)
	}
	if err := f.c.UnlockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.c.vault.Get(conn.ID, "password"); err != nil || got != "hunter2" {
		t.Errorf("it read %q, %v", got, err)
	}
}

// Taking the lock off writes every secret back in the open, and asks for
// the passphrase to do it.
func TestRemovingTheLockWritesTheSecretsBack(t *testing.T) {
	f := setup(t)
	conn, err := f.c.Create(draft("Orders"), map[string]string{"password": "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.c.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	if err := f.c.RemoveVaultLock("wrong"); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("the wrong passphrase took the lock off: %v", err)
	}
	if err := f.c.RemoveVaultLock("correct horse"); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.kc.Get(conn.ID, "password"); got != "hunter2" {
		t.Errorf("the keychain holds %q", got)
	}
	if f.c.VaultLocked() {
		t.Error("the vault still says it has a lock")
	}
	if st := f.c.settings.Get(); st.Vault != nil {
		t.Errorf("the lock is still saved as %+v", st.Vault)
	}
}

// Changing the passphrase reseals with the new one, and the old one no
// longer opens it.
func TestChangingThePassphraseReseals(t *testing.T) {
	f := setup(t)
	conn, err := f.c.Create(draft("Orders"), map[string]string{"password": "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.c.LockVault("first"); err != nil {
		t.Fatal(err)
	}
	if err := f.c.LockVault("second"); err != nil {
		t.Fatal(err)
	}
	f.c.CloseVault()
	if err := f.c.UnlockVault("first"); !errors.Is(err, ErrPassphrase) {
		t.Errorf("the old passphrase said %v", err)
	}
	if err := f.c.UnlockVault("second"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.c.vault.Get(conn.ID, "password"); err != nil || got != "hunter2" {
		t.Errorf("it read %q, %v", got, err)
	}
}

// A lock is not put on a vault that cannot be read: sealing what cannot be
// read would be sealing nothing.
func TestLockingNeedsAnOpenVault(t *testing.T) {
	f := setup(t)
	if _, err := f.c.Create(draft("Orders"), map[string]string{"password": "hunter2"}); err != nil {
		t.Fatal(err)
	}
	if err := f.c.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	f.c.CloseVault()
	if err := f.c.LockVault("another"); !errors.Is(err, ErrLocked) {
		t.Errorf("it said %v", err)
	}
}

// A lock with no passphrase is no lock, and is refused before anything is
// written.
func TestALockNeedsAPassphrase(t *testing.T) {
	f := setup(t)
	if _, err := f.c.Create(draft("Orders"), map[string]string{"password": "hunter2"}); err != nil {
		t.Fatal(err)
	}
	if err := f.c.LockVault(""); err == nil {
		t.Fatal("a lock with no passphrase was put on")
	}
	if f.c.VaultLocked() {
		t.Error("the vault took it anyway")
	}
	if st := f.c.settings.Get(); st.Vault != nil {
		t.Errorf("it was saved as %+v", st.Vault)
	}
}

// A secret that was never typed is not one that could not be read: a
// connection that wants a password and has none locks like any other.
func TestLockingAVaultWithNothingInIt(t *testing.T) {
	f := setup(t)
	conn, err := f.c.Create(draft("Orders"), map[string]string{"password": "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	// The keychain entry has gone, as it does when somebody clears it:
	// the connection still says it wants a password and there is none.
	if err := f.kc.Delete(conn.ID, "password"); err != nil {
		t.Fatal(err)
	}
	if got := f.c.MissingSecrets(conn.ID); len(got) != 1 {
		t.Fatalf("the connection is missing %v", got)
	}
	if err := f.c.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	if !f.c.VaultLocked() || !f.c.VaultOpen() {
		t.Error("the vault did not take the lock")
	}
}

// A lock read back from the settings is the lock that was put on.
func TestALockComesBackFromTheSettings(t *testing.T) {
	f := setup(t)
	conn, err := f.c.Create(draft("Orders"), map[string]string{"password": "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.c.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	// The next start: the same keychain, the same settings, a new vault.
	sf, _, err := store.OpenSettings(f.path)
	if err != nil {
		t.Fatal(err)
	}
	v := NewVault(f.kc, nil)
	v.SetLock(NewLock(sf.Get().Vault))
	next := NewConnections(sf, v, nil)
	if !next.VaultLocked() || next.VaultOpen() {
		t.Fatal("the next start did not come back locked")
	}
	if err := next.UnlockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	if got, err := v.Get(conn.ID, "password"); err != nil || got != "hunter2" {
		t.Errorf("it read %q, %v", got, err)
	}
}

// A portable copy keeps nothing beyond the session until there is a
// passphrase: a password in a file beside the application is plaintext
// with extra steps.
func TestAPortableVaultKeepsNothingUntilItIsLocked(t *testing.T) {
	dir := t.TempDir()
	sf, _, err := store.OpenSettings(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	file := secrets.NewFile(filepath.Join(dir, "secrets.json"))
	v := NewPortableVault(file)
	c := NewConnections(sf, v, nil)
	if !v.Portable() || v.Persistent() {
		t.Fatalf("a portable vault with no lock says portable %v, persistent %v", v.Portable(), v.Persistent())
	}
	conn, err := c.Create(draft("Orders"), map[string]string{"password": "hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	// This session has it, and the file does not.
	if got, err := v.Get(conn.ID, "password"); err != nil || got != "hunter2" {
		t.Errorf("this session read %q, %v", got, err)
	}
	if _, err := file.Get(conn.ID, "password"); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("the file holds it: %v", err)
	}

	// A passphrase makes it a vault that keeps things, and what this
	// session was holding goes into it, sealed.
	if err := c.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	if !v.Persistent() {
		t.Error("a portable vault with a lock does not keep anything")
	}
	stored, err := file.Get(conn.ID, "password")
	if err != nil {
		t.Fatalf("the file holds nothing: %v", err)
	}
	if !Sealed(stored) || strings.Contains(stored, "hunter2") {
		t.Errorf("the file holds %q", stored)
	}
}

// A portable copy with no lock keeps nothing, and reads nothing: what a
// file beside the application holds is not its business until there is a
// passphrase to open it with.
func TestAPortableVaultWithNoLockReadsNothing(t *testing.T) {
	dir := t.TempDir()
	sf, _, err := store.OpenSettings(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	file := secrets.NewFile(filepath.Join(dir, "secrets.json"))
	// Something a locked run left behind, whose lock is no longer set.
	l, _ := aLock(t, "gone")
	sealed, err := l.Seal("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Set("c1", "password", sealed); err != nil {
		t.Fatal(err)
	}
	v := NewPortableVault(file)
	_ = NewConnections(sf, v, nil)
	if got, err := v.Get("c1", "password"); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("it read %q, %v", got, err)
	}
}

// A portable copy's lock cannot be taken off: there is nowhere for the
// passwords to go.
func TestAPortableLockCannotComeOff(t *testing.T) {
	dir := t.TempDir()
	sf, _, err := store.OpenSettings(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := NewConnections(sf, NewPortableVault(secrets.NewFile(filepath.Join(dir, "secrets.json"))), nil)
	if _, err := c.Create(draft("Orders"), map[string]string{"password": "hunter2"}); err != nil {
		t.Fatal(err)
	}
	if err := c.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveVaultLock("correct horse"); err == nil || !strings.Contains(err.Error(), "portable") {
		t.Errorf("it said %v", err)
	}
	if !c.VaultLocked() {
		t.Error("the lock came off anyway")
	}
	// Changing it is offered, though: that is a different question.
	if err := c.LockVault("second"); err != nil {
		t.Errorf("the passphrase could not be changed: %v", err)
	}
}
