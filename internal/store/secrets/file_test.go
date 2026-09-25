package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Secrets kept beside the application, for a portable copy (FR-17.6).

func TestAFileKeepsAndGivesBack(t *testing.T) {
	f := NewFile(filepath.Join(t.TempDir(), "secrets.json"))
	if _, err := f.Get("c1", "password"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an empty file said %v", err)
	}
	if err := f.Set("c1", "password", "sealed-bytes"); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("c1", "ssh_passphrase", "other"); err != nil {
		t.Fatal(err)
	}
	if got, err := f.Get("c1", "password"); err != nil || got != "sealed-bytes" {
		t.Errorf("it read %q, %v", got, err)
	}
	// Another one over the same file reads what the first wrote: the file
	// is the keychain, not the process.
	if got, err := NewFile(f.Path()).Get("c1", "ssh_passphrase"); err != nil || got != "other" {
		t.Errorf("a second reader read %q, %v", got, err)
	}
}

// One secret of one connection is one entry: two connections with the same
// password do not share one, and two keys of one connection do not either.
func TestAFileTellsItsEntriesApart(t *testing.T) {
	f := NewFile(filepath.Join(t.TempDir(), "secrets.json"))
	for _, s := range []struct{ id, key, val string }{
		{"c1", "password", "one"},
		{"c2", "password", "two"},
		{"c1", "ssh_passphrase", "three"},
	} {
		if err := f.Set(s.id, s.key, s.val); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []struct{ id, key, val string }{
		{"c1", "password", "one"},
		{"c2", "password", "two"},
		{"c1", "ssh_passphrase", "three"},
	} {
		if got, _ := f.Get(s.id, s.key); got != s.val {
			t.Errorf("%s/%s read %q", s.id, s.key, got)
		}
	}
}

// Deleting removes one and leaves the rest, and deleting what is not there
// is what was asked for already.
func TestDeletingFromAFile(t *testing.T) {
	f := NewFile(filepath.Join(t.TempDir(), "secrets.json"))
	if err := f.Set("c1", "password", "one"); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("c2", "password", "two"); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete("c1", "password"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("c1", "password"); !errors.Is(err, ErrNotFound) {
		t.Errorf("it is still there: %v", err)
	}
	if got, _ := f.Get("c2", "password"); got != "two" {
		t.Errorf("the other one reads %q", got)
	}
	if err := f.Delete("c1", "password"); err != nil {
		t.Errorf("deleting what is not there said %v", err)
	}
}

// The file is the owner's alone, and is written whole or not at all.
func TestTheFileIsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	f := NewFile(path)
	if err := f.Set("c1", "password", "one"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("the file has mode %v", st.Mode().Perm())
	}
	// Nothing is left beside it: a temporary file with secrets in it is a
	// secrets file nobody is looking after.
	left, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 {
		var names []string
		for _, e := range left {
			names = append(names, e.Name())
		}
		t.Errorf("the directory holds %v", names)
	}
}

// A file that is not what it should be says so rather than reading as
// empty: a secrets file nobody can parse is not a person with no secrets.
func TestAFileThatCannotBeRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := NewFile(path)
	if _, err := f.Get("c1", "password"); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("it said %v", err)
	}
	if err := f.Set("c1", "password", "one"); err == nil {
		t.Error("it wrote over a file it could not read")
	}
}

// A name that could not be one is refused, as the keychain's is.
func TestAFileRefusesANameThatIsNotOne(t *testing.T) {
	f := NewFile(filepath.Join(t.TempDir(), "secrets.json"))
	for _, c := range [][2]string{{"", "password"}, {"c1", ""}, {"a/b", "password"}} {
		if err := f.Set(c[0], c[1], "x"); err == nil {
			t.Errorf("%q/%q was written", c[0], c[1])
		}
		// Refused for what it is, and not as a secret that is merely
		// absent: one says the name is wrong, the other that there is
		// nothing under it.
		if _, err := f.Get(c[0], c[1]); err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("%q/%q was read: %v", c[0], c[1], err)
		}
		if err := f.Delete(c[0], c[1]); err == nil {
			t.Errorf("%q/%q was deleted", c[0], c[1])
		}
	}
}

// Deleting from a file that is not there leaves it not there: a copy
// nobody has saved a password in has no secrets file at all.
func TestDeletingFromNoFileWritesNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	if err := NewFile(path).Delete("c1", "password"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("it wrote a file to delete nothing: %v", err)
	}
}

// A write that cannot finish leaves nothing half written beside the file:
// a temporary file with secrets in it is a secrets file nobody is looking
// after.
func TestAFailedWriteLeavesNothingBeside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.json")
	// A directory where the file goes: the write can be made and not
	// moved into place.
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := NewFile(path).Set("c1", "password", "one"); err == nil {
		t.Fatal("it wrote over a directory")
	}
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range left {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("it left %s behind", e.Name())
		}
	}
}

// A secret larger than any platform takes is refused here too, so that a
// portable copy behaves as the others do.
func TestAFileRefusesOneTooLarge(t *testing.T) {
	f := NewFile(filepath.Join(t.TempDir(), "secrets.json"))
	if err := f.Set("c1", "password", strings.Repeat("x", MaxBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("it said %v", err)
	}
}
