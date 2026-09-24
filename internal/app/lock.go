package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// An app-level lock over the credential vault (NFR-S7).
//
// Without it, the keychain holds each secret under the OS user's login,
// and anything running as that user can read it — this application, and
// anything else. That is the bargain the platform offers and it is the
// right default: the alternative is a passphrase at every start.
//
// With the lock on, what the keychain holds is sealed: the secret
// encrypted with a key derived from a passphrase that is stored nowhere.
// The keychain entry on its own is then no longer the secret, and neither
// is this application without the passphrase. What is stored beside it is
// a verifier — enough to say that a passphrase is the right one, and not
// enough to work out what it is.
//
// The cost is stated plainly wherever it is offered: a passphrase nobody
// remembers is secrets nobody can read, here or anywhere, and the only way
// on is to type them again.

// ErrLocked is a vault whose lock has not been opened this session.
var ErrLocked = errors.New("app: the vault is locked")

// ErrPassphrase is the wrong passphrase.
var ErrPassphrase = errors.New("app: that is not the passphrase")

// sealPrefix marks a stored secret as sealed. It carries a version so that
// a change of cipher can be told from the cipher in use, and it means a
// vault part way through being sealed is still readable: each secret says
// for itself which it is.
const sealPrefix = "ikigai-sealed-v1:"

// Argon2id parameters. A tenth of a second and sixty-four megabytes on a
// desktop, which is a great deal of work for each guess and nothing for
// the one person who knows the passphrase.
const (
	lockTime    uint32 = 1
	lockMemory  uint32 = 64 * 1024
	lockThreads uint8  = 4
	lockKeyLen  uint32 = 32
	lockSaltLen        = 16
)

// A Lock is the passphrase gate over a vault. The zero value is no lock,
// which is what a vault has until somebody puts one on.
type Lock struct {
	set store.VaultLock
	key []byte // derived from the passphrase; nil while locked
}

// NewLock reads a lock from what was saved. A nil setting is no lock, and
// so is one with no salt in it: On asks the salt, because a lock with
// nothing to derive a key from could only hold back what it cannot open.
func NewLock(set *store.VaultLock) *Lock {
	if set == nil {
		return &Lock{}
	}
	return &Lock{set: *set}
}

// On reports whether there is a lock.
func (l *Lock) On() bool { return l != nil && len(l.set.Salt) > 0 }

// Open reports whether the lock has been opened this session. A vault with
// no lock is open.
func (l *Lock) Open() bool { return l == nil || !l.On() || l.key != nil }

// Unlock derives the key from a passphrase and checks it against the
// verifier. A wrong passphrase leaves the lock as it was.
func (l *Lock) Unlock(pass string) error {
	if !l.On() {
		return nil
	}
	key := deriveKey(pass, l.set.Salt)
	if subtle.ConstantTimeCompare(verifierOf(key), l.set.Verifier) != 1 {
		return ErrPassphrase
	}
	l.key = key
	return nil
}

// Close forgets the key, leaving the lock shut until a passphrase opens it
// again.
func (l *Lock) Close() { l.key = nil }

// deriveKey is the key a passphrase and salt make.
func deriveKey(pass string, salt []byte) []byte {
	return argon2.IDKey([]byte(pass), salt, lockTime, lockMemory, lockThreads, lockKeyLen)
}

// verifierOf is what is stored to recognise the key: a hash of it, which
// says whether a passphrase is right and nothing about what it is. The key
// itself is never written anywhere.
func verifierOf(key []byte) []byte {
	sum := sha256.Sum256(append([]byte("ikigai-vault-verifier"), key...))
	return sum[:]
}

// NewSettings is the setting for a new lock on this passphrase.
func NewSettings(pass string) (store.VaultLock, error) {
	salt := make([]byte, lockSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return store.VaultLock{}, err
	}
	return store.VaultLock{Salt: salt, Verifier: verifierOf(deriveKey(pass, salt))}, nil
}

// Seal encrypts a secret for storing. A lock that is off or shut leaves it
// as it is, and says so: sealing with no key would be storing the secret
// in the open while claiming otherwise.
func (l *Lock) Seal(plain string) (string, error) {
	if !l.On() {
		return plain, nil
	}
	if l.key == nil {
		return "", ErrLocked
	}
	gcm, err := gcmFor(l.key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return sealPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// Unseal decrypts a stored secret. What is not sealed comes back as it is,
// so a vault part way through being sealed reads throughout.
func (l *Lock) Unseal(stored string) (string, error) {
	rest, sealed := strings.CutPrefix(stored, sealPrefix)
	if !sealed {
		return stored, nil
	}
	if l == nil || l.key == nil {
		return "", ErrLocked
	}
	raw, err := base64.StdEncoding.DecodeString(rest)
	if err != nil {
		return "", errors.New("app: this secret is not stored as it says it is")
	}
	gcm, err := gcmFor(l.key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("app: this secret is too short to be what it says it is")
	}
	out, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("app: this secret could not be read with that passphrase")
	}
	return string(out), nil
}

// Sealed reports whether a stored secret is sealed.
func Sealed(stored string) bool { return strings.HasPrefix(stored, sealPrefix) }

func gcmFor(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
