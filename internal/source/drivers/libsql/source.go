// Package libsql is the libSQL and Turso driver (T4.3).
//
// libSQL is SQLite: the same SQL, the same catalog, the same pragmas, the
// same types — over a wire instead of a file. So this package is a way in
// and nothing else. Everything below it is the SQLite driver's, through
// sqlite.NewSource, because a second copy of that introspection would be a
// second thing to keep right about an engine that is the same engine
// (REQ-DB-4).
//
// The client is pure Go (tursodatabase/libsql-client-go), which keeps a
// remote database as free of a C toolchain as a local file is. The
// requirements named tursodatabase/go-libsql; that one embeds a replica
// through cgo and a Rust archive, which is a different feature — an offline
// replica — and not what connecting to a database is.
package libsql

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/tursodatabase/libsql-client-go/libsql" // registers "libsql" with database/sql

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/drivers/sqlite"
)

const driverID = "libsql"

func init() { source.Register(Driver{}) }

// Driver opens libSQL and Turso databases.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "libSQL / Turso", Paradigm: model.ParadigmRelational,
		URLSchemes: []string{"libsql", "wss", "ws"},
		Fields: []source.Field{
			{Key: "url", Label: "URL", Kind: source.FieldText, Required: true,
				Help: "The database's address: libsql://name-org.turso.io, or http://host:8080 for a server of your own."},
			{Key: "token", Label: "Auth token", Kind: source.FieldPassword, Secret: true,
				Help: "Turso's database token. A server of your own may need none."},
		},
	}
}

// Open dials the database and proves it answers SQL, and nothing more: a
// test connection has to be quick and say precisely what is wrong.
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	t, err := resolve(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(driverID, t.dial)
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "That address could not be opened.", Err: err}
	}
	// The smallest thing there is to read. A wrong address, a wrong token
	// and a server that is not libSQL all answer differently here, and
	// each becomes what it is rather than "could not connect".
	var one int
	if err := db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		db.Close()
		return nil, classify(err, t.noToken)
	}
	addr := t.addr
	// A file is opened query_only as well as guarded; a connection to a
	// server has no such mode, and each request may be its own session, so
	// a pragma would not hold. Read-only here is the guard's alone, which is
	// where it is enforced either way (NFR-S4).
	return sqlite.NewSource(db, cfg, sqlite.Flavour{Product: "libSQL", Where: addr, WhereIs: "url"}), nil
}

// A target is what Open dials: the address to show it by, the string to
// dial it with, and whether a token was found — which is what tells a
// server rejecting a token from one asking for the token nobody gave.
type target struct {
	addr    string
	dial    string
	noToken bool
}

// resolve reads a connection into what to dial. It is where the token is
// fetched, so that the one place a secret is handled can be tested without
// a server to dial.
func resolve(cfg source.ConnectionConfig) (target, error) {
	addr, err := address(cfg)
	if err != nil {
		return target{}, err
	}
	token := ""
	if cfg.Secret != nil {
		// A server of your own may want none, and a missing token is that
		// rather than a failure: what it answers says whether it needed one.
		token, _ = cfg.Secret("token")
	}
	return target{addr: addr, dial: dsn(addr, token), noToken: token == ""}, nil
}

// address is the URL to dial, from the field or from a URL somebody pasted.
func address(cfg source.ConnectionConfig) (string, error) {
	raw := strings.TrimSpace(cfg.Params["url"])
	if raw == "" {
		raw = strings.TrimSpace(cfg.Host)
	}
	if raw == "" {
		return "", &source.ConnectError{Kind: source.ConnectConfig, Hint: "Give the database's URL."}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "That is not an address.", Err: err}
	}
	switch u.Scheme {
	case "libsql", "http", "https", "ws", "wss":
	case "":
		return "", &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "The URL needs a scheme: libsql://, https:// or http://."}
	default:
		return "", &source.ConnectError{Kind: source.ConnectConfig,
			Hint: fmt.Sprintf("%s:// is not an address this driver can open.", u.Scheme)}
	}
	return raw, nil
}

// dsn is the address with the token on it, which is how this client takes
// one. It is never logged: what carries it is handed straight to the driver.
func dsn(addr, token string) string {
	if token == "" {
		return addr
	}
	sep := "?"
	if strings.Contains(addr, "?") {
		sep = "&"
	}
	return addr + sep + "authToken=" + url.QueryEscape(token)
}

// classify turns a dial failure into what it is. The client reports
// everything as text, so this reads the text — and says so, rather than
// pretending to know more than it does.
func classify(err error, noToken bool) error {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "401") || strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "authentication") || strings.Contains(msg, "jwt"):
		hint := "The server rejected that token."
		if noToken {
			hint = "The server wants an auth token."
		}
		return &source.ConnectError{Kind: source.ConnectAuth, Hint: hint, Err: err}
	case strings.Contains(msg, "404") || strings.Contains(msg, "no such database") ||
		strings.Contains(msg, "namespace"):
		return &source.ConnectError{Kind: source.ConnectNoDatabase,
			Hint: "There is no database at that address.", Err: err}
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "timeout") || strings.Contains(msg, "dial") ||
		strings.Contains(msg, "eof") || strings.Contains(msg, "tls"):
		return &source.ConnectError{Kind: source.ConnectUnreachable,
			Hint: "The server could not be reached.", Err: err}
	}
	return &source.ConnectError{Kind: source.ConnectConfig,
		Hint: "That address did not answer as a libSQL database.", Err: err}
}
