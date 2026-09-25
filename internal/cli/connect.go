package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/app/connstr"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
)

// Saying which database, on a command line (FR-16.1).
//
// Three ways, because the two kinds of caller want different things. A
// pipeline has a connection string in a secret and no keychain to read; a
// person at their own machine has the connections they already saved. Neither
// should have to keep the other's form.
//
// A password is never a flag. A command line is readable by everything else on
// the machine, and a password in one is a password in every process list and
// every shell history. IKIGAI_PASSWORD and IKIGAI_URL are how one arrives.

// target is how a command was told which database to work on.
type target struct {
	url        string
	connection string
	driver     string
	host       string
	port       int
	database   string
	user       string
	readOnly   bool
	confirmed  bool
}

// flags declares the connection flags on a command's flag set. Every command
// takes them, because every command works on a database.
func (t *target) flags(fs *flag.FlagSet) {
	fs.StringVar(&t.url, "url", "", "connection string; IKIGAI_URL is used when this is absent")
	fs.StringVar(&t.connection, "connection", "", "a connection saved in the application, by name or ID")
	fs.StringVar(&t.driver, "driver", "", "driver ID, where no URL names one (sqlite, postgres, mysql…)")
	fs.StringVar(&t.host, "host", "", "server host")
	fs.IntVar(&t.port, "port", 0, "server port; the driver's own when 0")
	fs.StringVar(&t.database, "database", "", "database, keyspace or file")
	fs.StringVar(&t.user, "user", "", "user name")
	fs.BoolVar(&t.readOnly, "read-only", false, "refuse every statement that would change data")
	fs.BoolVar(&t.confirmed, "confirm", false, "consent to change a production connection, or to replace rows")
}

// opened is a connection this process holds, and how to say what it is.
type opened struct {
	src source.Source
	// name is what to call it in a message. Never the connection string: what
	// is in one is a password (NFR-S2).
	name  string
	close func()
}

// open makes the connection the flags name.
//
// The three ways are tried in the order somebody would mean them: what is on
// the command line, then what is in the environment, then what was saved. A
// command line that names two of them is a mistake rather than a preference,
// and is refused rather than resolved.
func (t *target) open(ctx context.Context) (*opened, error) {
	url := strings.TrimSpace(t.url)
	explicit := strings.TrimSpace(t.driver) != ""
	saved := strings.TrimSpace(t.connection) != ""
	switch {
	case url != "" && (explicit || saved),
		explicit && saved:
		return nil, misusef("say which database once: --url, --driver, or --connection")
	}
	if url == "" && !explicit && !saved {
		url = strings.TrimSpace(os.Getenv("IKIGAI_URL"))
		if url == "" {
			return nil, misusef("no database: pass --url, --driver with --database, or " +
				"--connection (or set IKIGAI_URL)")
		}
	}
	if saved {
		return t.openSaved(ctx)
	}
	conn, lookup, err := t.describe(url)
	if err != nil {
		return nil, err
	}
	conn.ReadOnly = t.readOnly
	drv, err := source.Lookup(conn.Driver)
	if err != nil {
		// A driver nobody has is a mistake in the arguments, not a failure of
		// the world: nothing was dialled, and the message names what was asked
		// for so that a typo is visible.
		return nil, badArgs{err}
	}
	src, err := drv.Open(ctx, conn.ConnectionConfig(lookup))
	if err != nil {
		return nil, err
	}
	name := conn.Name
	if name == "" {
		name = conn.Driver
	}
	return &opened{src: src, name: name, close: func() { src.Close() }}, nil
}

// describe turns what was asked for into a connection and where its password
// comes from. Nothing here reads a keychain: a connection said in flags or in
// the environment has its password in the environment too.
func (t *target) describe(url string) (store.SavedConnection, func(string, string) (string, error), error) {
	password := os.Getenv("IKIGAI_PASSWORD")
	if url != "" {
		res, err := connstr.Parse(url)
		if err != nil {
			return store.SavedConnection{}, nil, err
		}
		if password == "" {
			password = res.Secrets["password"]
		}
		secrets := res.Secrets
		return res.Conn, func(_, key string) (string, error) {
			if key == "password" {
				return password, nil
			}
			return secrets[key], nil
		}, nil
	}
	conn := store.SavedConnection{
		Driver:   strings.TrimSpace(t.driver),
		Host:     strings.TrimSpace(t.host),
		Port:     t.port,
		Database: strings.TrimSpace(t.database),
		User:     strings.TrimSpace(t.user),
	}
	conn.Name = conn.Driver
	if conn.Database != "" {
		conn.Name = conn.Database
	}
	return conn, func(_, key string) (string, error) {
		if key == "password" {
			return password, nil
		}
		return "", nil
	}, nil
}

// openSaved opens a connection the application holds, through the same
// Connections the window uses: a tunnel, a cloud token and the keychain all
// work here because none of it is reimplemented (ARCH-1).
func (t *target) openSaved(ctx context.Context) (*opened, error) {
	paths, err := store.Resolve()
	if err != nil {
		return nil, err
	}
	settings, _, err := store.OpenSettings(paths.SettingsFile())
	if err != nil {
		return nil, err
	}
	vault := app.NewVault(secrets.OS(), nil)
	if paths.Portable {
		vault = app.NewPortableVault(secrets.NewFile(filepath.Join(paths.Data, "secrets.json")))
	}
	vault.SetLock(app.NewLock(settings.Get().Vault))
	if vault.Locked() && !vault.Lock().Open() {
		// A sealed vault needs a passphrase, and there is nobody to ask. It is
		// not asked for in the environment either: a passphrase there would
		// undo the sealing for every process on the machine (NFR-S7).
		return nil, errors.New("the saved passwords are sealed; unseal them in the application, " +
			"or pass --url with a connection string")
	}
	conns := app.NewConnections(settings, vault, nil)
	want := strings.TrimSpace(t.connection)
	var found *store.SavedConnection
	for _, c := range conns.List() {
		if c.ID == want || strings.EqualFold(c.Name, want) {
			saved := c
			found = &saved
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("no saved connection called %q", want)
	}
	// Read-only is enforced by the connection's own guard, in the data layer
	// (NFR-S4), so asking for it here means opening with the guard tightened —
	// and nothing written to the file a person shares with the application.
	open := conns.Open
	if t.readOnly {
		open = conns.OpenReadOnly
	}
	live, err := open(ctx, found.ID, app.MonitorConfig{})
	if err != nil {
		return nil, err
	}
	return &opened{src: live.Source, name: found.Name, close: func() { live.Close() }}, nil
}
