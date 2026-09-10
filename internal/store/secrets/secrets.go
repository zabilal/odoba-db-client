// Package secrets keeps credentials in the operating system's keychain:
// macOS Keychain, Windows Credential Manager, and the Secret Service (GNOME
// Keyring, KWallet) on Linux (FR-1.5, NFR-S1).
//
// There is deliberately no file fallback. Where no keychain exists — a
// headless Linux machine with no Secret Service — Set fails with
// ErrUnavailable, and the application asks for the password each session,
// holding it in Memory. NFR-S1 says never plaintext, and a secret "encrypted"
// with a key stored beside it is plaintext with extra steps.
//
// go-keyring's macOS backend hands the value to /usr/bin/security over stdin
// (`security -i`), not as a command-line argument, so it never appears in
// the process list. Checked in its source, keyring_darwin.go, at v0.2.8.
//
// This package must not import any UI package (ARCH-1).
package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

// Service is the keychain service name every entry is filed under.
const Service = "Ikigai DB"

// MaxBytes caps a secret's size. The platforms' own limits differ, and
// macOS's is hit after base64 inflation, so a single cap below all of them
// makes behaviour identical everywhere instead of platform-dependent.
const MaxBytes = 2048

var (
	ErrNotFound    = errors.New("secrets: no such secret")
	ErrUnavailable = errors.New("secrets: no OS keychain is available")
	ErrTooLarge    = fmt.Errorf("secrets: a secret may be at most %d bytes", MaxBytes)
)

// Keychain stores one connection's named secrets: "password",
// "ssh_passphrase" and so on.
type Keychain interface {
	Get(connectionID, key string) (string, error)
	Set(connectionID, key, value string) error
	// Delete removes a secret. Deleting one that does not exist is not an
	// error: the caller's intent, that it be gone, is already satisfied.
	Delete(connectionID, key string) error
}

// account composes the keychain account for one secret of one connection.
func account(connectionID, key string) (string, error) {
	if connectionID == "" || key == "" || strings.Contains(connectionID, "/") {
		return "", fmt.Errorf("secrets: invalid connection id %q or key %q", connectionID, key)
	}
	return connectionID + "/" + key, nil
}

type osKeychain struct{ service string }

// OS returns the operating system's keychain.
func OS() Keychain { return osKeychain{service: Service} }

func (k osKeychain) Get(connectionID, key string) (string, error) {
	acct, err := account(connectionID, key)
	if err != nil {
		return "", err
	}
	v, err := keyring.Get(k.service, acct)
	return v, translate(err)
}

func (k osKeychain) Set(connectionID, key, value string) error {
	acct, err := account(connectionID, key)
	if err != nil {
		return err
	}
	if len(value) > MaxBytes {
		return ErrTooLarge
	}
	return translate(keyring.Set(k.service, acct, value))
}

func (k osKeychain) Delete(connectionID, key string) error {
	acct, err := account(connectionID, key)
	if err != nil {
		return err
	}
	if err := translate(keyring.Delete(k.service, acct)); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// translate maps backend errors to this package's. None of them carry the
// secret's value, and neither does anything returned here.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, keyring.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, keyring.ErrUnsupportedPlatform):
		return ErrUnavailable
	case errors.Is(err, keyring.ErrSetDataTooBig):
		return ErrTooLarge
	}
	// A Secret Service that is not running surfaces only as a D-Bus error,
	// with no sentinel to match against.
	if m := err.Error(); strings.Contains(m, "org.freedesktop.secrets") || strings.Contains(strings.ToLower(m), "dbus") {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return fmt.Errorf("secrets: %w", err)
}

// Available checks the OS keychain by storing, reading back and deleting a
// throwaway entry. A Secret Service that is installed but not running, or a
// locked keychain, fails only when used, so nothing less is a real check.
func Available() error { return probe(OS()) }

func probe(k Keychain) error {
	id := "probe-" + randomHex()
	defer k.Delete(id, "probe")
	if err := k.Set(id, "probe", "ok"); err != nil {
		return err
	}
	v, err := k.Get(id, "probe")
	if err != nil {
		return err
	}
	if v != "ok" {
		return fmt.Errorf("%w: a probe value did not read back intact", ErrUnavailable)
	}
	return nil
}

// Memory is an in-process keychain. It serves tests, and sessions where the
// OS keychain is unavailable: secrets then live exactly as long as the process.
type Memory struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemory returns an empty in-process keychain.
func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

func (k *Memory) Get(connectionID, key string) (string, error) {
	acct, err := account(connectionID, key)
	if err != nil {
		return "", err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[acct]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

func (k *Memory) Set(connectionID, key, value string) error {
	acct, err := account(connectionID, key)
	if err != nil {
		return err
	}
	if len(value) > MaxBytes {
		return ErrTooLarge
	}
	k.mu.Lock()
	k.m[acct] = value
	k.mu.Unlock()
	return nil
}

func (k *Memory) Delete(connectionID, key string) error {
	acct, err := account(connectionID, key)
	if err != nil {
		return err
	}
	k.mu.Lock()
	delete(k.m, acct)
	k.mu.Unlock()
	return nil
}

func randomHex() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
