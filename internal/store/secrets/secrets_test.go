package secrets

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

// contract is the behaviour every Keychain must have. It runs against Memory
// always, and against the real OS keychain when explicitly enabled.
func contract(t *testing.T, k Keychain) {
	t.Helper()
	id := "test-" + randomHex()
	other := "test-" + randomHex()
	t.Cleanup(func() {
		for _, c := range []string{id, other} {
			for _, key := range []string{"password", "ssh_passphrase"} {
				_ = k.Delete(c, key)
			}
		}
	})

	if _, err := k.Get(id, "password"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing secret: want ErrNotFound, got %v", err)
	}

	// Values chosen to break naive handling: shell metacharacters (go-keyring
	// on macOS drives a shell-like tool), quotes, newlines, unicode, and
	// significant surrounding whitespace.
	values := []string{
		"hunter2",
		`'; rm -rf / #"` + "\n$(whoami)`id`",
		"  padded  ",
		"pässwörd 🔑 日本",
		strings.Repeat("x", MaxBytes),
	}
	for _, v := range values {
		if err := k.Set(id, "password", v); err != nil {
			t.Fatalf("Set %q: %v", v[:min(len(v), 20)], err)
		}
		got, err := k.Get(id, "password")
		if err != nil || got != v {
			t.Errorf("round trip of %q: got %q, %v", v[:min(len(v), 20)], got[:min(len(got), 20)], err)
		}
	}

	// Keys and connections are isolated from one another.
	_ = k.Set(id, "password", "pw-1")
	_ = k.Set(id, "ssh_passphrase", "pp-1")
	_ = k.Set(other, "password", "pw-2")
	if v, _ := k.Get(id, "password"); v != "pw-1" {
		t.Errorf("keys not isolated: %q", v)
	}
	if v, _ := k.Get(other, "password"); v != "pw-2" {
		t.Errorf("connections not isolated: %q", v)
	}

	if err := k.Delete(id, "password"); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Get(id, "password"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted secret still readable: %v", err)
	}
	if v, _ := k.Get(id, "ssh_passphrase"); v != "pp-1" {
		t.Error("deleting one key removed another")
	}
	if err := k.Delete(id, "password"); err != nil {
		t.Errorf("deleting a missing secret should be a no-op, got %v", err)
	}

	// Oversized values are refused, and the refusal does not echo them.
	big := "SECRETVALUE" + strings.Repeat("y", MaxBytes)
	if err := k.Set(id, "password", big); !errors.Is(err, ErrTooLarge) || strings.Contains(err.Error(), "SECRETVALUE") {
		t.Errorf("oversized secret: %v", err)
	}

	for _, bad := range [][2]string{{"", "password"}, {"id", ""}, {"a/b", "password"}} {
		if err := k.Set(bad[0], bad[1], "x"); err == nil {
			t.Errorf("accepted invalid id/key %q", bad)
		}
	}
}

func TestMemoryKeychain(t *testing.T) { contract(t, NewMemory()) }

func TestMemoryIsSafeForConcurrentUse(t *testing.T) {
	k := NewMemory()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "c" + string(rune('a'+i%26))
			_ = k.Set(id, "password", "v")
			_, _ = k.Get(id, "password")
			_ = k.Delete(id, "password")
		}(i)
	}
	wg.Wait()
}

// TestOSKeychain writes to the real keychain, under a service name of its own,
// and removes what it wrote. It runs only when asked, so an ordinary
// `go test ./...` never touches a developer's keychain.
func TestOSKeychain(t *testing.T) {
	if os.Getenv("IKIGAI_TEST_OS_KEYCHAIN") == "" {
		t.Skip("set IKIGAI_TEST_OS_KEYCHAIN=1 to run against the real OS keychain")
	}
	k := osKeychain{service: "Ikigai DB (test)"}
	if err := probe(k); errors.Is(err, ErrUnavailable) {
		t.Skipf("no OS keychain here: %v", err)
	} else if err != nil {
		t.Fatalf("probe: %v", err)
	}
	contract(t, k)
}

// What translate says, and what it keeps.
//
// A refusal it recognises becomes this package's own sentinel, so a caller can
// match on it. A D-Bus error has no sentinel to match, so it is recognised by
// what it says — and the recognition is not allowed to lose it: a keychain that
// will not answer is worth reporting along with the reason a person could look
// up, and errors.Is must reach both.
func TestATranslatedRefusalKeepsWhatItCameFrom(t *testing.T) {
	if got := translate(nil); got != nil {
		t.Errorf("nothing translated to %v", got)
	}
	for _, c := range []struct {
		from error
		want error
	}{
		{keyring.ErrNotFound, ErrNotFound},
		{keyring.ErrUnsupportedPlatform, ErrUnavailable},
		{keyring.ErrSetDataTooBig, ErrTooLarge},
		{fmt.Errorf("wrapped: %w", keyring.ErrNotFound), ErrNotFound},
	} {
		if got := translate(c.from); !errors.Is(got, c.want) {
			t.Errorf("%v translated to %v, want %v", c.from, got, c.want)
		}
	}

	dbus := errors.New("dial unix /run/user/1000/bus: connect: connection refused (org.freedesktop.secrets)")
	got := translate(dbus)
	if !errors.Is(got, ErrUnavailable) {
		t.Errorf("a Secret Service that is not running reads as %v", got)
	}
	if !errors.Is(got, dbus) {
		t.Errorf("the reason it is unavailable was dropped: %v", got)
	}

	// Anything else is this package's, and still carries what it came from.
	other := errors.New("the keychain is locked")
	got = translate(other)
	if errors.Is(got, ErrUnavailable) || errors.Is(got, ErrNotFound) || errors.Is(got, ErrTooLarge) {
		t.Errorf("an unrecognised refusal was classified: %v", got)
	}
	if !errors.Is(got, other) {
		t.Errorf("it dropped what it came from: %v", got)
	}
}
