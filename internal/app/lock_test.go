package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
)

// The app-level lock over the credential vault (NFR-S7).

// aLock is a lock on a passphrase, open.
func aLock(t *testing.T, pass string) (*Lock, store.VaultLock) {
	t.Helper()
	set, err := NewSettings(pass)
	if err != nil {
		t.Fatal(err)
	}
	l := NewLock(&set)
	if err := l.Unlock(pass); err != nil {
		t.Fatal(err)
	}
	return l, set
}

// A sealed secret is not the secret, and comes back only through the
// passphrase.
func TestASealedSecretNeedsThePassphrase(t *testing.T) {
	l, set := aLock(t, "correct horse")
	sealed, err := l.Seal("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "hunter2") {
		t.Fatalf("the secret is in what was stored: %q", sealed)
	}
	if !Sealed(sealed) {
		t.Errorf("a sealed secret does not say it is one: %q", sealed)
	}
	// Another lock on the same setting, shut, will not open it.
	shut := NewLock(&set)
	if _, err := shut.Unseal(sealed); !errors.Is(err, ErrLocked) {
		t.Errorf("a shut lock said %v", err)
	}
	if err := shut.Unlock("hunter2"); !errors.Is(err, ErrPassphrase) {
		t.Errorf("the wrong passphrase said %v", err)
	}
	if shut.Open() {
		t.Error("a wrong passphrase opened the lock")
	}
	if err := shut.Unlock("correct horse"); err != nil {
		t.Fatal(err)
	}
	got, err := shut.Unseal(sealed)
	if err != nil || got != "hunter2" {
		t.Errorf("it read %q, %v", got, err)
	}
}

// Nothing that could be used to work out the passphrase is kept.
func TestWhatIsKeptOfAPassphrase(t *testing.T) {
	set, err := NewSettings("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Salt) != lockSaltLen || len(set.Verifier) != 32 {
		t.Fatalf("it kept %d bytes of salt and a %d-byte verifier", len(set.Salt), len(set.Verifier))
	}
	for _, b := range [][]byte{set.Salt, set.Verifier} {
		if strings.Contains(string(b), "correct") {
			t.Error("the passphrase is in what was kept")
		}
	}
	// The same passphrase twice is two different salts, so two vaults
	// locked with one passphrase share nothing.
	other, err := NewSettings("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if string(other.Salt) == string(set.Salt) || string(other.Verifier) == string(set.Verifier) {
		t.Error("two locks on the same passphrase are the same lock")
	}
}

// The same secret sealed twice is not the same bytes: a nonce of its own
// each time, so that two connections with one password do not look alike.
func TestSealingTwiceIsNotTheSameBytes(t *testing.T) {
	l, _ := aLock(t, "correct horse")
	a, err := l.Seal("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.Seal("hunter2")
	if err != nil || a == b {
		t.Errorf("sealed twice as %q and %q (%v)", a, b, err)
	}
}

// What is not sealed reads as it is, so a vault part way through being
// sealed reads throughout.
func TestWhatIsNotSealedReadsAsItIs(t *testing.T) {
	l, _ := aLock(t, "correct horse")
	got, err := l.Unseal("plain")
	if err != nil || got != "plain" {
		t.Errorf("it read %q, %v", got, err)
	}
	// And with no lock at all, sealing leaves it alone.
	none := NewLock(nil)
	if none.On() {
		t.Error("no setting is a lock")
	}
	// A setting with nothing in it is no lock either: a lock with no salt
	// could not derive a key, and would hold back what it cannot seal.
	if NewLock(&store.VaultLock{}).On() {
		t.Error("an empty setting is a lock")
	}
	if s, err := none.Seal("plain"); err != nil || s != "plain" {
		t.Errorf("no lock sealed %q, %v", s, err)
	}
}

// A sealed secret with no lock to open it says so rather than falling
// over: a setting removed by hand leaves the keychain as it was.
func TestASealedSecretWithNoLock(t *testing.T) {
	l, _ := aLock(t, "correct horse")
	sealed, err := l.Seal("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewLock(nil).Unseal(sealed); !errors.Is(err, ErrLocked) {
		t.Errorf("it said %v", err)
	}
	var nothing *Lock
	if _, err := nothing.Unseal(sealed); !errors.Is(err, ErrLocked) {
		t.Errorf("a nil lock said %v", err)
	}
}

// A shut lock seals nothing: sealing with no key would be storing the
// secret in the open while saying otherwise.
func TestAShutLockSealsNothing(t *testing.T) {
	set, err := NewSettings("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewLock(&set).Seal("hunter2"); !errors.Is(err, ErrLocked) {
		t.Errorf("it said %v", err)
	}
}

// Bytes that say they are sealed and are not come back as an error, not as
// themselves: a secret that cannot be read is not a secret that is empty.
func TestBytesThatOnlyLookSealed(t *testing.T) {
	l, _ := aLock(t, "correct horse")
	for _, s := range []string{sealPrefix + "not base64!!", sealPrefix + "c2hvcnQ=", sealPrefix + "YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXo="} {
		if got, err := l.Unseal(s); err == nil {
			t.Errorf("%q read as %q", s, got)
		}
	}
}

// Closing forgets the key, and the vault holds its secrets back again.
func TestClosingForgetsTheKey(t *testing.T) {
	l, _ := aLock(t, "correct horse")
	sealed, err := l.Seal("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if l.Open() {
		t.Error("a closed lock says it is open")
	}
	if _, err := l.Unseal(sealed); !errors.Is(err, ErrLocked) {
		t.Errorf("it said %v", err)
	}
}

// A vault with a lock on holds its secrets back until it is opened, and
// hands them over afterwards.
func TestAVaultHoldsItsSecretsBack(t *testing.T) {
	v := NewVault(secrets.NewMemory(), nil)
	if err := v.Set("c1", "password", "hunter2"); err != nil {
		t.Fatal(err)
	}
	l, set := aLock(t, "correct horse")
	v.SetLock(l)
	if err := v.Set("c1", "password", "hunter2"); err != nil {
		t.Fatal(err)
	}
	got, err := v.Get("c1", "password")
	if err != nil || got != "hunter2" {
		t.Fatalf("an open vault read %q, %v", got, err)
	}
	v.SetLock(NewLock(&set)) // shut
	if !v.Locked() {
		t.Fatal("a vault with a shut lock says it is not locked")
	}
	if _, err := v.Get("c1", "password"); !errors.Is(err, ErrLocked) {
		t.Errorf("a locked vault read it anyway: %v", err)
	}
	if err := v.Set("c1", "password", "other"); !errors.Is(err, ErrLocked) {
		t.Errorf("a locked vault wrote: %v", err)
	}
	// A secret stored before the lock went on is not sealed, and a locked
	// vault does not hand that one over either.
	plain := secrets.NewMemory()
	if err := plain.Set("c1", "password", "in the open"); err != nil {
		t.Fatal(err)
	}
	shut := NewVault(plain, nil)
	shut.SetLock(NewLock(&set))
	if got, err := shut.Get("c1", "password"); !errors.Is(err, ErrLocked) {
		t.Errorf("a locked vault handed over %q (%v)", got, err)
	}

	// A secret typed in for this session is neither sealed nor held back:
	// it is here because somebody just typed it here.
	if err := v.SetSession("c1", "password", "typed"); err != nil {
		t.Fatal(err)
	}
	if got, err := v.Get("c1", "password"); err != nil || got != "typed" {
		t.Errorf("the session's own secret read %q, %v", got, err)
	}
}
