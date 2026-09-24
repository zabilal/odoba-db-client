package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// The app-level lock over the credential vault, in the window (NFR-S7).

// Locking the vault from the window seals what the keychain holds, and the
// menu then offers what a locked vault can do.
func TestLockingTheVaultFromTheWindow(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", map[string]string{"password": "hunter2"})
	fx.s.sync()
	if fx.s.menuItems[cmdVaultLock].Disabled {
		t.Fatal("a vault with no lock is not offered one")
	}
	if !fx.s.menuItems[cmdVaultRemove].Disabled || !fx.s.menuItems[cmdVaultClose].Disabled {
		t.Error("a vault with no lock is offered a lock to take off")
	}
	fx.s.run(cmdVaultLock)
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("the form did not open")
	}
	fields := entriesIn(top)
	if len(fields) < 2 {
		t.Fatalf("the form has %d fields", len(fields))
	}
	// The two must match, and until they do there is nothing to tap.
	fields[0].SetText("correct horse")
	fields[1].SetText("correct hose")
	if b := findButton(top, "Lock"); b == nil || !b.Disabled() {
		t.Error("two passphrases that differ were offered a lock")
	}
	fields[1].SetText("correct horse")
	if b := findButton(top, "Lock"); b == nil || b.Disabled() {
		t.Fatal("two passphrases that match were not offered a lock")
	}
	test.Tap(findButton(top, "Lock"))
	pump(t, fx.q, func() bool { return fx.conns.VaultLocked() })

	stored, _ := fx.vaultKeys.Get(c.ID, "password")
	if !app.Sealed(stored) || strings.Contains(stored, "hunter2") {
		t.Errorf("the keychain holds %q", stored)
	}
	fx.s.sync()
	if fx.s.menuItems[cmdVaultRemove].Disabled || fx.s.menuItems[cmdVaultClose].Disabled {
		t.Error("a locked vault is offered neither shutting nor taking the lock off")
	}
	// And the application still reads what it sealed, the lock being open.
	if got, err := fx.conns.Vault().Get(c.ID, "password"); err != nil || got != "hunter2" {
		t.Errorf("it read %q, %v", got, err)
	}
}

// A window over a shut vault asks before it puts the last session back:
// tabs that reopened first would fail for a reason nobody had been asked
// about yet.
func TestAShutVaultIsAskedAboutFirst(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.conns.Create(store.SavedConnection{Name: "db1", Driver: "postgres", Host: "db1"},
		map[string]string{"password": "hunter2"}); err != nil {
		t.Fatal(err)
	}
	if err := fx.conns.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	fx.conns.CloseVault()

	restored := false
	s := New(fx.s.app, fx.deps)
	t.Cleanup(s.shutdown)
	s.askToUnlock(func() { restored = true })
	if restored {
		t.Fatal("it went on before the vault was asked about")
	}
	top := s.win.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("it did not ask")
	}
	fields := entriesIn(top)
	if len(fields) != 1 {
		t.Fatalf("the sheet has %d fields", len(fields))
	}
	fields[0].SetText("wrong")
	test.Tap(findButton(top, "Unlock"))
	if restored || fx.conns.VaultOpen() {
		t.Fatal("the wrong passphrase opened it")
	}
	fields[0].SetText("correct horse")
	test.Tap(findButton(top, "Unlock"))
	if !restored || !fx.conns.VaultOpen() {
		t.Error("the right passphrase did not open it")
	}
}

// Going on without the passphrase leaves the vault shut and puts the
// session back anyway: a person may only want the connections that need
// no password.
func TestGoingOnWithoutThePassphrase(t *testing.T) {
	fx := newFixture(t)
	if _, err := fx.conns.Create(store.SavedConnection{Name: "db1", Driver: "postgres", Host: "db1"},
		map[string]string{"password": "hunter2"}); err != nil {
		t.Fatal(err)
	}
	if err := fx.conns.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	fx.conns.CloseVault()
	restored := false
	s := New(fx.s.app, fx.deps)
	t.Cleanup(s.shutdown)
	s.askToUnlock(func() { restored = true })
	test.Tap(findButton(s.win.Canvas().Overlays().Top(), "Go on Without It"))
	if !restored {
		t.Error("it did not go on")
	}
	if fx.conns.VaultOpen() {
		t.Error("it opened the vault anyway")
	}
}

// A vault with no lock is not asked about at all.
func TestAVaultWithNoLockIsNotAskedAbout(t *testing.T) {
	fx := newFixture(t)
	asked := false
	fx.s.askToUnlock(func() { asked = true })
	if !asked {
		t.Error("it did not go straight on")
	}
	if top := fx.s.win.Canvas().Overlays().Top(); top != nil {
		t.Errorf("it asked about a vault with no lock: %v", labelsIn(top))
	}
}

// Shutting the vault now holds the secrets back without a restart.
func TestShuttingTheVaultNow(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", map[string]string{"password": "hunter2"})
	if err := fx.conns.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	fx.s.sync()
	if fx.s.menuItems[cmdVaultClose].Disabled {
		t.Fatal("an open lock cannot be shut")
	}
	fx.s.run(cmdVaultClose)
	if fx.conns.VaultOpen() {
		t.Fatal("it is still open")
	}
	if _, err := fx.conns.Vault().Get(c.ID, "password"); err == nil {
		t.Error("a shut vault handed out a secret")
	}
	fx.s.sync()
	if !fx.s.menuItems[cmdVaultClose].Disabled {
		t.Error("a shut vault is offered shutting again")
	}
	if !fx.s.menuItems[cmdVaultLock].Disabled {
		t.Error("a shut vault is offered a new passphrase, which would seal what it cannot read")
	}
}

// Taking the lock off asks for the passphrase, and writes the secrets back
// in the open.
func TestTakingTheLockOffFromTheWindow(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", map[string]string{"password": "hunter2"})
	if err := fx.conns.LockVault("correct horse"); err != nil {
		t.Fatal(err)
	}
	fx.s.sync()
	fx.s.run(cmdVaultRemove)
	top := fx.s.win.Canvas().Overlays().Top()
	fields := entriesIn(top)
	if len(fields) != 1 {
		t.Fatalf("the form has %d fields", len(fields))
	}
	fields[0].SetText("correct horse")
	test.Tap(findButton(top, "Remove"))
	pump(t, fx.q, func() bool { return !fx.conns.VaultLocked() })
	if got, _ := fx.vaultKeys.Get(c.ID, "password"); got != "hunter2" {
		t.Errorf("the keychain holds %q", got)
	}
}
