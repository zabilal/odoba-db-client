package secrets

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
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
				k.Delete(c, key)
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
	k.Set(id, "password", "pw-1")
	k.Set(id, "ssh_passphrase", "pp-1")
	k.Set(other, "password", "pw-2")
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
			k.Set(id, "password", "v")
			k.Get(id, "password")
			k.Delete(id, "password")
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
