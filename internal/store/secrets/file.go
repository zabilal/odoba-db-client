package secrets

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// A portable install keeps its secrets beside the application rather than
// in the machine's keychain (FR-17.6).
//
// This does not contradict what this package says about file fallbacks. A
// secret encrypted with a key stored beside it is plaintext with extra
// steps, and that is still true — which is why what writes here writes
// only sealed values, with a key derived from a passphrase that is on no
// disk at all (ADR-0152, ADR-0154). What is in this file without a lock
// over it is nothing: a portable install with no passphrase keeps its
// passwords for the session and no longer.

// File keeps secrets in one file. It holds whatever it is given and reads
// it back; sealing is the vault's, above it.
type File struct {
	mu   sync.Mutex
	path string
}

// NewFile keeps secrets in a file at path, which is created on the first
// write, readable only by its owner.
func NewFile(path string) *File { return &File{path: path} }

// Path is where the secrets are kept.
func (f *File) Path() string { return f.path }

func (f *File) Get(connectionID, key string) (string, error) {
	acct, err := account(connectionID, key)
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	held, err := f.read()
	if err != nil {
		return "", err
	}
	v, ok := held[acct]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (f *File) Set(connectionID, key, value string) error {
	acct, err := account(connectionID, key)
	if err != nil {
		return err
	}
	if len(value) > MaxBytes {
		return ErrTooLarge
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	held, err := f.read()
	if err != nil {
		return err
	}
	held[acct] = value
	return f.write(held)
}

// Delete removes a secret. Deleting one that is not there is not an error:
// what was asked for is already so.
func (f *File) Delete(connectionID, key string) error {
	acct, err := account(connectionID, key)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	held, err := f.read()
	if err != nil {
		return err
	}
	if _, ok := held[acct]; !ok {
		return nil
	}
	delete(held, acct)
	return f.write(held)
}

// read is what the file holds. A file that is not there yet holds nothing,
// which is not a failure: it is written at the first secret.
func (f *File) read() (map[string]string, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	held := map[string]string{}
	if err := json.Unmarshal(b, &held); err != nil {
		return nil, errors.New("secrets: the secrets file could not be read")
	}
	return held, nil
}

// write replaces the file so that a reader, or a crash, sees either what
// was there or what is now, and never a torn mix of the two.
func (f *File) write(held map[string]string) error {
	b, err := json.MarshalIndent(held, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(f.path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.path)
}
