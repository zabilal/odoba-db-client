package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/redact"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// CurrentSettingsVersion is the settings schema this build reads and writes.
const CurrentSettingsVersion = 1

// ErrSettingsFrozen is returned by Update when the settings file cannot be
// written safely: it came from a newer version, or it failed validation.
var ErrSettingsFrozen = errors.New("store: the settings file will not be overwritten")

// Settings are the user's preferences and saved connections (FR-17.2).
//
// The file is JSON: the standard library reads it, and it round-trips
// predictably and diffs cleanly under version control. Keys are snake_case
// because people edit this file by hand.
type Settings struct {
	Version     int               `json:"version"`
	Appearance  string            `json:"appearance,omitempty"` // system, light, dark
	Accent      string            `json:"accent,omitempty"`     // blue unless set: see theme.Accents
	Editor      EditorSettings    `json:"editor"`
	Folders     []Folder          `json:"folders,omitempty"`
	Connections []SavedConnection `json:"connections"`
	Favorites   []Favorite        `json:"favorites,omitempty"`
	// Bindings are the shortcuts a person changed, by command ID, in the
	// form commands.Shortcut.String writes; "" is no shortcut (T1.8).
	Bindings map[string]string `json:"bindings,omitempty"`
	// Vault is the app-level lock over the credential vault, or nil where
	// there is none (NFR-S7). It holds what recognises a passphrase and
	// nothing that could be used to work one out.
	Vault *VaultLock `json:"vault,omitempty"`
}

// VaultLock is what is kept of an app-level lock: the salt the key is
// derived with, and a verifier that says whether a passphrase is the right
// one. Neither the passphrase nor the key it makes is stored anywhere.
type VaultLock struct {
	Salt     []byte `json:"salt"`
	Verifier []byte `json:"verifier"`
}

// EditorSettings are query-editor preferences.
type EditorSettings struct {
	TabSize int `json:"tab_size,omitempty"`
}

// Folder groups connections in the explorer (FR-1.6).
type Folder struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// Favorite is an object pinned above the explorer's tree (FR-2.6, ADR-0019):
// a connection, the object's kind and path on it, and its name.
type Favorite struct {
	ConnectionID string   `json:"connection"`
	Kind         string   `json:"kind"`
	Path         []string `json:"path"`
	Label        string   `json:"label"`
}

// Key is what makes a favourite one of a kind: its connection and object.
func (f Favorite) Key() string {
	return f.ConnectionID + "\x00" + f.Kind + "\x00" + strings.Join(f.Path, "\x00")
}

// SavedConnection is a connection's settings as persisted.
//
// There is no password field, and there must never be one. Secrets live in
// the OS keychain under the connection's ID; Secrets lists which exist, so the
// UI can show "password saved" without reading the keychain.
type SavedConnection struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Driver      string            `json:"driver"`
	Host        string            `json:"host,omitempty"`
	Port        int               `json:"port,omitempty"`
	Database    string            `json:"database,omitempty"`
	User        string            `json:"user,omitempty"`
	Params      map[string]string `json:"params,omitempty"`
	TLS         TLS               `json:"tls"`
	SSH         *SSH              `json:"ssh,omitempty"`
	Cloud       *Cloud            `json:"cloud,omitempty"`
	Environment string            `json:"environment,omitempty"`
	ReadOnly    bool              `json:"read_only,omitempty"`
	Folder      string            `json:"folder,omitempty"`
	Color       string            `json:"color,omitempty"`
	Secrets     []string          `json:"secrets,omitempty"`
}

// TLS is a connection's transport-security settings: paths, never key material.
type TLS struct {
	Mode       string `json:"mode,omitempty"`
	CAFile     string `json:"ca_file,omitempty"`
	CertFile   string `json:"cert_file,omitempty"`
	KeyFile    string `json:"key_file,omitempty"`
	ServerName string `json:"server_name,omitempty"`
}

// SSH is a tunnel's settings. Passwords and passphrases are keychain secrets.
type SSH struct {
	Host      string   `json:"host"`
	Port      int      `json:"port,omitempty"`
	User      string   `json:"user,omitempty"`
	Method    string   `json:"method,omitempty"`
	KeyFile   string   `json:"key_file,omitempty"`
	JumpHosts []string `json:"jump_hosts,omitempty"`
}

// Cloud says a connection signs in with a cloud identity instead of a
// password (FR-1.14). Params holds the provider's own settings and nothing
// secret: this file is written in plain sight.
type Cloud struct {
	Provider string            `json:"provider"`
	Params   map[string]string `json:"params,omitempty"`
}

func defaultSettings() Settings {
	return Settings{
		Version:     CurrentSettingsVersion,
		Appearance:  "system",
		Connections: []SavedConnection{},
	}
}

// NewID returns an identifier for a connection or folder: random, so that
// two machines creating connections never collide when settings are merged.
func NewID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("store: no randomness: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// OpenNotice reports what the user should be told about at startup.
type OpenNotice struct {
	// Created is true when there was no settings file yet.
	Created bool
	// Recovered is where an unreadable settings file was moved, if one was.
	Recovered string
	// Frozen explains why the file is being left untouched, if it is.
	Frozen string
}

// SettingsFile is the settings file and its in-memory copy. It is safe for
// concurrent use.
type SettingsFile struct {
	path string

	mu     sync.Mutex
	cur    Settings
	frozen string
}

// OpenSettings reads the settings file.
//
// It never loses the user's data, which shapes the three failure cases:
//
//   - No file: defaults. The file is written on the first change.
//   - Not valid JSON: the file is MOVED ASIDE, not overwritten, and defaults are
//     used. Overwriting it would destroy the only copy of the user's
//     connections, and any chance of recovering them by hand.
//   - Written by a newer version, or valid JSON that fails validation: loaded,
//     but frozen. Saving would drop fields this version does not know, or
//     would have to "fix" the file silently, so the file stays as it is until
//     the user decides.
func OpenSettings(path string) (*SettingsFile, OpenNotice, error) {
	f := &SettingsFile{path: path}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		f.cur = defaultSettings()
		return f, OpenNotice{Created: true}, nil
	}
	if err != nil {
		return nil, OpenNotice{}, fmt.Errorf("store: reading settings: %w", err)
	}

	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		aside := path + ".corrupt-" + time.Now().Format("20060102-150405")
		if rerr := os.Rename(path, aside); rerr != nil {
			return nil, OpenNotice{}, fmt.Errorf("store: settings unreadable (%v) and could not be moved aside: %w", err, rerr)
		}
		f.cur = defaultSettings()
		return f, OpenNotice{Recovered: aside}, nil
	}

	if s.Version == 0 {
		s.Version = 1 // written by hand without a version: read it as the first one
	}
	if s.Connections == nil {
		s.Connections = []SavedConnection{}
	}
	f.cur = s

	var notice OpenNotice
	switch {
	case s.Version > CurrentSettingsVersion:
		f.frozen = fmt.Sprintf("written by a newer version of Ikigai DB (settings version %d; this build understands %d)",
			s.Version, CurrentSettingsVersion)
	default:
		if err := validate(s); err != nil {
			f.frozen = err.Error()
		}
	}
	notice.Frozen = f.frozen
	return f, notice, nil
}

// Get returns a copy of the current settings. Changing it changes nothing.
func (f *SettingsFile) Get() Settings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return clone(f.cur)
}

// Frozen returns why the file cannot be written, or "" if it can.
func (f *SettingsFile) Frozen() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frozen
}

// Update applies a change and writes it to disk.
//
// The change is made to a copy, validated, written atomically, and only then
// made current. A change that fails at any step leaves both memory and disk
// exactly as they were.
func (f *SettingsFile) Update(fn func(*Settings) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.frozen != "" {
		return fmt.Errorf("%w: %s", ErrSettingsFrozen, f.frozen)
	}

	next := clone(f.cur)
	if err := fn(&next); err != nil {
		return err
	}
	next.Version = CurrentSettingsVersion
	if err := validate(next); err != nil {
		return err
	}

	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFileAtomic(f.path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("store: writing settings: %w", err)
	}
	f.cur = next
	return nil
}

func clone(s Settings) Settings {
	data, err := json.Marshal(s)
	if err != nil {
		panic("store: settings cannot be marshalled: " + err.Error())
	}
	var out Settings
	if err := json.Unmarshal(data, &out); err != nil {
		panic("store: settings cannot be unmarshalled: " + err.Error())
	}
	return out
}

var secretName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// validate enforces the invariants the rest of the application relies on.
func validate(s Settings) error {
	folders := map[string]bool{}
	for _, fo := range s.Folders {
		if fo.ID == "" || folders[fo.ID] {
			return fmt.Errorf("store: folder %q has a missing or duplicate id", fo.Name)
		}
		folders[fo.ID] = true
	}

	ids := map[string]bool{}
	for i, c := range s.Connections {
		where := fmt.Sprintf("connection %d (%q)", i+1, c.Name)
		switch {
		case c.ID == "":
			return fmt.Errorf("store: %s has no id", where)
		case ids[c.ID]:
			return fmt.Errorf("store: %s duplicates id %s", where, c.ID)
		case strings.TrimSpace(c.Name) == "":
			return fmt.Errorf("store: connection %d has no name", i+1)
		case c.Driver == "":
			return fmt.Errorf("store: %s has no driver", where)
		case c.Port < 0 || c.Port > 65535:
			return fmt.Errorf("store: %s has port %d out of range", where, c.Port)
		case c.Environment != "" && !source.Environment(c.Environment).Valid():
			return fmt.Errorf("store: %s has unknown environment %q", where, c.Environment)
		case c.Folder != "" && !folders[c.Folder]:
			return fmt.Errorf("store: %s is in folder %s, which does not exist", where, c.Folder)
		}
		ids[c.ID] = true

		// The structural guarantee behind NFR-S1: a parameter whose name looks
		// like a secret cannot be saved at all, so no code path can put one
		// in this file by mistake.
		for k := range c.Params {
			if redact.IsSecretKey(k) {
				return fmt.Errorf("store: %s: %q looks like a secret; secrets belong in the OS keychain, never in the settings file", where, k)
			}
		}
		for _, n := range c.Secrets {
			if !secretName.MatchString(n) {
				return fmt.Errorf("store: %s: %q is not a secret name", where, n)
			}
		}
	}

	// A favourite points into a connection this file holds, once: deleting
	// a connection has to delete its favourites in the same write.
	seen := map[string]bool{}
	for i, f := range s.Favorites {
		where := fmt.Sprintf("favourite %d (%q)", i+1, f.Label)
		switch {
		case !ids[f.ConnectionID]:
			return fmt.Errorf("store: %s is on connection %q, which does not exist", where, f.ConnectionID)
		case f.Kind == "" || len(f.Path) == 0:
			return fmt.Errorf("store: %s names no object", where)
		case seen[f.Key()]:
			return fmt.Errorf("store: %s is listed twice", where)
		}
		seen[f.Key()] = true
	}
	for id := range s.Bindings {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("store: a custom shortcut names no command")
		}
	}
	return nil
}

// ConnectionConfig turns a saved connection into what a driver opens.
//
// Secrets are resolved lazily through lookup, at the moment the driver asks,
// so a plaintext credential never sits in a struct that might be logged or
// serialised (see source.ConnectionConfig.Secret).
func (c SavedConnection) ConnectionConfig(lookup func(connectionID, key string) (string, error)) source.ConnectionConfig {
	cfg := source.ConnectionConfig{
		DriverID: c.Driver,
		Name:     c.Name,
		Host:     c.Host,
		Port:     c.Port,
		Database: c.Database,
		User:     c.User,
		TLS: source.TLSConfig{
			Mode: c.TLS.Mode, CAFile: c.TLS.CAFile, CertFile: c.TLS.CertFile,
			KeyFile: c.TLS.KeyFile, ServerName: c.TLS.ServerName,
		},
		Guard: source.Guard{ReadOnly: c.ReadOnly, Environment: source.Environment(c.Environment)},
	}
	if len(c.Params) > 0 {
		cfg.Params = make(map[string]string, len(c.Params))
		for k, v := range c.Params {
			cfg.Params[k] = v
		}
	}
	if c.SSH != nil {
		cfg.SSH = &source.SSHConfig{
			Host: c.SSH.Host, Port: c.SSH.Port, User: c.SSH.User, Method: c.SSH.Method,
			KeyFile: c.SSH.KeyFile, JumpHosts: append([]string(nil), c.SSH.JumpHosts...),
		}
	}
	if c.Cloud != nil {
		cfg.Cloud = &source.CloudConfig{Provider: c.Cloud.Provider}
		if len(c.Cloud.Params) > 0 {
			cfg.Cloud.Params = make(map[string]string, len(c.Cloud.Params))
			for k, v := range c.Cloud.Params {
				cfg.Cloud.Params[k] = v
			}
		}
	}
	if lookup != nil {
		id := c.ID
		cfg.Secret = func(key string) (string, error) { return lookup(id, key) }
	}
	return cfg
}
