package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/redact"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
)

// ErrNotFound: no connection or folder with that ID.
var ErrNotFound = errors.New("app: not found")

// testTimeout bounds "test connection". A server that has not answered in 15
// seconds is not going to, and the user is waiting.
const testTimeout = 15 * time.Second

// SecretEdit describes changes to a connection's secrets. A key that is absent
// is left as it is. That is what "leave the password blank to keep the saved
// one" needs, and why a plain map could not express it.
type SecretEdit struct {
	Set   map[string]string
	Clear []string
}

// Connections manages saved connections (FR-1.1, FR-1.6).
type Connections struct {
	settings *store.SettingsFile
	vault    *Vault
	log      *slog.Logger
}

// NewConnections builds the service. log may be nil.
func NewConnections(settings *store.SettingsFile, vault *Vault, log *slog.Logger) *Connections {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Connections{settings: settings, vault: vault, log: log}
}

// Vault returns the secret store, for prompting for session-only secrets.
func (c *Connections) Vault() *Vault { return c.vault }

// List returns saved connections in the user's order.
func (c *Connections) List() []store.SavedConnection { return c.settings.Get().Connections }

// Folders returns the connection folders.
func (c *Connections) Folders() []store.Folder { return c.settings.Get().Folders }

// Get returns one connection.
func (c *Connections) Get(id string) (store.SavedConnection, bool) {
	for _, conn := range c.settings.Get().Connections {
		if conn.ID == id {
			return conn, true
		}
	}
	return store.SavedConnection{}, false
}

// Create saves a new connection. Its secrets go to the vault, and the
// settings file records only their names.
//
// Secrets are written first. A connection saved without its password would
// fail on first use with an error pointing nowhere useful. If saving the
// settings then fails, the secrets are removed again, so none is orphaned.
func (c *Connections) Create(conn store.SavedConnection, secretValues map[string]string) (store.SavedConnection, error) {
	if _, err := source.Lookup(conn.Driver); err != nil {
		return store.SavedConnection{}, fmt.Errorf("app: %w", err)
	}
	conn.ID = store.NewID()
	conn.Secrets = mergeNames(nil, secretValues, nil)

	if err := c.vault.setAll(conn.ID, secretValues); err != nil {
		return store.SavedConnection{}, fmt.Errorf("app: saving secrets: %w", err)
	}
	if err := c.settings.Update(func(s *store.Settings) error {
		s.Connections = append(s.Connections, conn)
		return nil
	}); err != nil {
		c.vault.deleteAll(conn.ID, conn.Secrets)
		return store.SavedConnection{}, err
	}
	return conn, nil
}

// Update replaces a connection's settings and applies secret changes.
//
// The previous value of every secret being overwritten is read first, so that
// a failed settings write can put it back. Otherwise a failed save would leave
// the user with a changed password and unchanged settings.
func (c *Connections) Update(conn store.SavedConnection, edit SecretEdit) error {
	cur, ok := c.Get(conn.ID)
	if !ok {
		return ErrNotFound
	}
	if conn.Driver != cur.Driver {
		if _, err := source.Lookup(conn.Driver); err != nil {
			return fmt.Errorf("app: %w", err)
		}
	}

	previous := map[string]*string{}
	for k, v := range edit.Set {
		if v == "" {
			continue
		}
		if old, err := c.vault.Get(conn.ID, k); err == nil {
			previous[k] = &old
		} else {
			previous[k] = nil
		}
	}
	if err := c.vault.setAll(conn.ID, edit.Set); err != nil {
		return fmt.Errorf("app: saving secrets: %w", err)
	}

	conn.Secrets = mergeNames(cur.Secrets, edit.Set, edit.Clear)
	if err := c.settings.Update(func(s *store.Settings) error {
		i := indexOf(s.Connections, conn.ID)
		if i < 0 {
			return ErrNotFound
		}
		s.Connections[i] = conn
		return nil
	}); err != nil {
		for k, old := range previous {
			if old == nil {
				c.vault.Delete(conn.ID, k)
			} else {
				c.vault.Set(conn.ID, k, *old)
			}
		}
		return err
	}

	for _, k := range edit.Clear {
		if edit.Set[k] == "" {
			if err := c.vault.Delete(conn.ID, k); err != nil {
				c.log.Warn("secret not removed from keychain", "connection", conn.ID, "key", k, "err", err)
			}
		}
	}
	return nil
}

// Duplicate copies a connection, secrets included, and places the copy
// directly after the original.
func (c *Connections) Duplicate(id string) (store.SavedConnection, error) {
	cur, ok := c.Get(id)
	if !ok {
		return store.SavedConnection{}, ErrNotFound
	}
	dup := cur // Get returns a deep copy
	dup.ID = store.NewID()
	dup.Name = cur.Name + " copy"

	copied := map[string]string{}
	for _, k := range cur.Secrets {
		// A secret that cannot be read is not copied; the copy then simply
		// asks for it, rather than failing to duplicate at all.
		if v, err := c.vault.Get(cur.ID, k); err == nil {
			copied[k] = v
		}
	}
	dup.Secrets = mergeNames(nil, copied, nil)

	if err := c.vault.setAll(dup.ID, copied); err != nil {
		return store.SavedConnection{}, fmt.Errorf("app: copying secrets: %w", err)
	}
	if err := c.settings.Update(func(s *store.Settings) error {
		i := indexOf(s.Connections, id)
		if i < 0 {
			return ErrNotFound
		}
		s.Connections = append(s.Connections[:i+1], append([]store.SavedConnection{dup}, s.Connections[i+1:]...)...)
		return nil
	}); err != nil {
		c.vault.deleteAll(dup.ID, dup.Secrets)
		return store.SavedConnection{}, err
	}
	return dup, nil
}

// Delete removes a connection and its secrets.
//
// The settings entry goes first. A leftover keychain entry that nothing points
// at is harmless, and it is logged. A connection whose password was deleted
// but whose entry survived would fail confusingly on its next use.
func (c *Connections) Delete(id string) error {
	cur, ok := c.Get(id)
	if !ok {
		return ErrNotFound
	}
	if err := c.settings.Update(func(s *store.Settings) error {
		i := indexOf(s.Connections, id)
		if i < 0 {
			return ErrNotFound
		}
		s.Connections = append(s.Connections[:i], s.Connections[i+1:]...)
		return nil
	}); err != nil {
		return err
	}
	if err := c.vault.deleteAll(id, cur.Secrets); err != nil {
		c.log.Warn("orphaned secrets left in keychain", "connection", id, "err", err)
	}
	return nil
}

// Reorder sets the order of connections. ids must be a permutation of the
// current IDs: a partial list is refused rather than silently dropping the
// connections it leaves out.
func (c *Connections) Reorder(ids []string) error {
	return c.settings.Update(func(s *store.Settings) error {
		if len(ids) != len(s.Connections) {
			return fmt.Errorf("app: reorder lists %d connections, there are %d", len(ids), len(s.Connections))
		}
		byID := make(map[string]store.SavedConnection, len(ids))
		for _, conn := range s.Connections {
			byID[conn.ID] = conn
		}
		out := make([]store.SavedConnection, 0, len(ids))
		for _, id := range ids {
			conn, ok := byID[id]
			if !ok {
				return fmt.Errorf("app: reorder names unknown or repeated connection %s", id)
			}
			delete(byID, id)
			out = append(out, conn)
		}
		s.Connections = out
		return nil
	})
}

// CreateFolder adds a folder.
func (c *Connections) CreateFolder(name, color string) (store.Folder, error) {
	if strings.TrimSpace(name) == "" {
		return store.Folder{}, errors.New("app: a folder needs a name")
	}
	f := store.Folder{ID: store.NewID(), Name: name, Color: color}
	err := c.settings.Update(func(s *store.Settings) error {
		s.Folders = append(s.Folders, f)
		return nil
	})
	return f, err
}

// RenameFolder renames a folder.
func (c *Connections) RenameFolder(id, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("app: a folder needs a name")
	}
	return c.settings.Update(func(s *store.Settings) error {
		for i := range s.Folders {
			if s.Folders[i].ID == id {
				s.Folders[i].Name = name
				return nil
			}
		}
		return ErrNotFound
	})
}

// DeleteFolder removes a folder. Its connections move to the top level; they
// are never deleted along with it.
func (c *Connections) DeleteFolder(id string) error {
	return c.settings.Update(func(s *store.Settings) error {
		i := -1
		for j, f := range s.Folders {
			if f.ID == id {
				i = j
			}
		}
		if i < 0 {
			return ErrNotFound
		}
		for j := range s.Connections {
			if s.Connections[j].Folder == id {
				s.Connections[j].Folder = ""
			}
		}
		s.Folders = append(s.Folders[:i], s.Folders[i+1:]...)
		return nil
	})
}

// SetFolder moves a connection into a folder, or to the top level with "".
func (c *Connections) SetFolder(connID, folderID string) error {
	return c.settings.Update(func(s *store.Settings) error {
		i := indexOf(s.Connections, connID)
		if i < 0 {
			return ErrNotFound
		}
		s.Connections[i].Folder = folderID
		return nil
	})
}

// MissingSecrets lists the secrets a connection needs but the vault cannot
// supply, such as a password saved on a machine with no keychain. The UI asks
// for these before connecting instead of letting the attempt fail.
func (c *Connections) MissingSecrets(id string) []string {
	conn, ok := c.Get(id)
	if !ok {
		return nil
	}
	var missing []string
	for _, k := range conn.Secrets {
		if _, err := c.vault.Get(id, k); errors.Is(err, secrets.ErrNotFound) {
			missing = append(missing, k)
		}
	}
	return missing
}

// TestResult is the outcome of "test connection" (FR-1.4).
type TestResult struct {
	OK      bool
	Kind    source.ConnectKind
	Hint    string // what to fix, for the user
	Detail  string // the redacted technical error
	Server  source.ServerInfo
	Elapsed time.Duration
}

// Test tries a connection, which may not be saved yet. typed holds whatever the
// form contains. A secret not typed falls back to the vault, so testing a
// saved connection does not mean typing its password again.
func (c *Connections) Test(ctx context.Context, conn store.SavedConnection, typed map[string]string) TestResult {
	start := time.Now()
	drv, err := source.Lookup(conn.Driver)
	if err != nil {
		return TestResult{Kind: source.ConnectConfig, Hint: "no driver is installed for this kind of connection",
			Detail: redact.Error(err), Elapsed: time.Since(start)}
	}

	cfg := conn.ConnectionConfig(func(id, key string) (string, error) {
		if v, ok := typed[key]; ok {
			return v, nil
		}
		if id == "" {
			return "", nil // unsaved and not typed: try without
		}
		v, err := c.vault.Get(id, key)
		if errors.Is(err, secrets.ErrNotFound) {
			return "", nil
		}
		return v, err
	})

	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	src, err := drv.Open(ctx, cfg)
	if err != nil {
		return testFailure(err, start)
	}
	defer src.Close()
	info, err := src.Info(ctx)
	if err != nil {
		return testFailure(err, start)
	}
	return TestResult{OK: true, Hint: "connected", Server: info, Elapsed: time.Since(start)}
}

func testFailure(err error, start time.Time) TestResult {
	r := TestResult{Kind: source.ConnectUnknown, Hint: "could not connect",
		Detail: redact.Error(err), Elapsed: time.Since(start)}
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		r.Kind, r.Hint = ce.Kind, ce.Hint
	}
	if errors.Is(err, context.DeadlineExceeded) {
		r.Kind, r.Hint = source.ConnectUnreachable, "the server did not answer within 15 seconds"
	}
	return r
}

// MissingSecretsError is returned by Open when secrets must be supplied first.
type MissingSecretsError struct{ Names []string }

func (e *MissingSecretsError) Error() string {
	return "app: needs " + strings.Join(e.Names, ", ") + " before connecting"
}

// Open connects to a saved connection and starts watching its health.
func (c *Connections) Open(ctx context.Context, id string, mon MonitorConfig) (*Live, error) {
	conn, ok := c.Get(id)
	if !ok {
		return nil, ErrNotFound
	}
	if missing := c.MissingSecrets(id); len(missing) > 0 {
		return nil, &MissingSecretsError{Names: missing}
	}
	drv, err := source.Lookup(conn.Driver)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	src, err := drv.Open(ctx, conn.ConnectionConfig(c.vault.Get))
	if err != nil {
		return nil, err
	}
	return startLive(id, src, mon), nil
}

func indexOf(conns []store.SavedConnection, id string) int {
	for i, c := range conns {
		if c.ID == id {
			return i
		}
	}
	return -1
}

// mergeNames computes the secret names a connection records: existing, plus
// newly set ones with a value, minus cleared ones.
func mergeNames(existing []string, set map[string]string, clear []string) []string {
	names := map[string]bool{}
	for _, n := range existing {
		names[n] = true
	}
	for k, v := range set {
		if v != "" {
			names[k] = true
		}
	}
	for _, k := range clear {
		if set[k] == "" {
			delete(names, k)
		}
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
