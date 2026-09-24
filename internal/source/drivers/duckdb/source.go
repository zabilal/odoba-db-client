//go:build duckdb

package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/marcboeker/go-duckdb/v2" // registers "duckdb" with database/sql

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

const (
	driverID = "duckdb"

	// language is what the editor highlights and completes by. DuckDB's
	// SQL is PostgreSQL's with additions, which is what the lexer's
	// fallback already reads it as.
	language = "duckdb"
)

var errClosed = errors.New("duckdb: connection is closed")

// Driver opens DuckDB database files.
type Driver struct{}

func init() { source.Register(Driver{}) }

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "DuckDB", Paradigm: model.ParadigmRelational,
		Fields: []source.Field{{Key: "database", Label: "File", Kind: source.FieldFile, Required: true,
			Help: "The database file. It must exist already."}},
	}
}

// duckSource is one open database file.
//
// One connection, and more than one database in it: a file somebody has
// ATTACHed is read through the same connection by naming it, so the
// explorer's top level is the databases this connection holds rather than
// the one file it was opened on (ADR-0146).
type duckSource struct {
	dialect

	cfg  source.ConnectionConfig
	db   *sql.DB
	path string

	// primary is the database an unqualified name means, which is where
	// the explorer says somebody is.
	primary string
	version string

	mu     sync.Mutex
	closed bool

	// sessions are the pinned connections, by the name anything stopping
	// their statement knows them by. There is no server to ask, so the
	// means of stopping one is kept here.
	sessions sync.Map // handle -> *duckSession

	// tables remembers what each table's columns are, because a browse
	// needs their types before it can write its own SELECT, and paging
	// asks on every page.
	tables sync.Map // "db\x00schema\x00table" -> *tableInfo
}

var (
	_ source.Source         = (*duckSource)(nil)
	_ source.Dialect        = (*duckSource)(nil)
	_ source.Queryer        = (*duckSource)(nil)
	_ source.Sessioner      = (*duckSource)(nil)
	_ source.DistinctLister = (*duckSource)(nil)
	_ source.Writer         = (*duckSource)(nil)
)

// Open opens the file and proves it is a DuckDB database, and nothing
// more (source.Driver: a test connection must be fast and precise).
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	path := strings.TrimSpace(cfg.Database)
	if path == "" {
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "Choose a database file."}
	}
	// Opening never makes one. DuckDB would create a file it cannot find,
	// so a mistyped path would open an empty database that looks exactly
	// like data gone missing.
	fi, err := os.Stat(path)
	switch {
	case err != nil:
		return nil, &source.ConnectError{Kind: source.ConnectNoDatabase,
			Hint: "The database file does not exist.", Err: err}
	case fi.IsDir():
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "That is a folder, not a database file."}
	}

	db, err := sql.Open(driverID, dsn(path, cfg.Guard.ReadOnly))
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "The file could not be opened.", Err: err}
	}
	db.SetConnMaxIdleTime(5 * time.Minute)
	s := &duckSource{cfg: cfg, db: db, path: path}
	if err := db.QueryRowContext(ctx,
		`SELECT version(), current_database()`).Scan(&s.version, &s.primary); err != nil {
		db.Close()
		return nil, classifyConnectError(err)
	}
	return s, nil
}

// dsn builds the connection string: the path, and the settings that belong
// to the connection rather than to a statement.
//
// A read-only connection opens the file read-only as well, so that the
// engine refuses what the classifier could not see. That is the same
// bargain the SQLite driver strikes, and here it also means the file's
// write lock is never taken — somebody reading a database another process
// is writing does not stop it writing (NFR-S4).
func dsn(path string, readOnly bool) string {
	q := url.Values{}
	if readOnly {
		q.Set("access_mode", "READ_ONLY")
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

func (s *duckSource) conn() (*sql.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errClosed
	}
	return s.db, nil
}

func (s *duckSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmRelational,
		// Databases and schemas both: a connection holds the file it was
		// opened on and any other somebody has attached to it, and each
		// holds schemas.
		Structure: capability.Structure{MultipleDatabases: true, Schemas: true},
		Query: capability.Query{Supported: true, Language: language, MultiStatement: true,
			Cancel: true, Parameters: true},
		Data: capability.Data{ServerSort: true, ServerFilter: true, DistinctValues: true,
			ApproximateCount: true, Insert: true, Update: true, Delete: true,
			TransactionalWrite: true},
		Schema: capability.Schema{ForeignKeys: true},
		Objects: map[model.ObjectKind]bool{
			model.KindDatabase: true, model.KindSchema: true, model.KindFolder: true,
			model.KindTable: true, model.KindView: true,
			model.KindColumn: true, model.KindIndex: true, model.KindSequence: true,
		},
	}
}

func (s *duckSource) Info(ctx context.Context) (source.ServerInfo, error) {
	return source.ServerInfo{
		Product: "DuckDB", Version: s.version,
		Attrs: map[string]string{"file": s.path},
	}, nil
}

func (s *duckSource) Ping(ctx context.Context) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	return db.PingContext(ctx)
}

// Close releases the file. Safe to call more than once: database/sql
// closes a pool once however often it is asked, so there is nothing here
// to remember.
func (s *duckSource) Close() error {
	s.mu.Lock()
	db := s.db
	s.closed = true
	s.mu.Unlock()
	if db == nil {
		return nil
	}
	return db.Close()
}

// classifyConnectError says what went wrong in terms somebody can act on
// (FR-1.4).
//
// There is no network here and no login: what can go wrong is the file.
// Another process holding it for writing is the one worth naming, because
// it is the one thing about this engine that surprises people — a DuckDB
// file has one writer at a time.
func classifyConnectError(err error) error {
	var ce *source.ConnectError
	if errors.As(err, &ce) {
		return err
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "different configuration"):
		// One process holds a file one way. A connection already open
		// read-write cannot be joined by one open read-only, and the
		// other way about, because they are the same database instance
		// underneath (ADR-0146).
		return &source.ConnectError{Kind: source.ConnectRefused,
			Hint: "This file is already open in this application another way. DuckDB holds a file one way at a time: close the other connection to it first.",
			Err:  err}
	case strings.Contains(text, "lock"):
		return &source.ConnectError{Kind: source.ConnectRefused,
			Hint: "Another program has this file open for writing. DuckDB allows one writer at a time; open it read-only to read alongside.",
			Err:  err}
	case strings.Contains(text, "not a valid duckdb database") ||
		strings.Contains(text, "not a database"):
		return &source.ConnectError{Kind: source.ConnectConfig,
			Hint: "The file is not a DuckDB database.", Err: err}
	case strings.Contains(text, "permission denied"):
		return &source.ConnectError{Kind: source.ConnectAuth,
			Hint: "This user may not read that file.", Err: err}
	}
	return &source.ConnectError{Kind: source.ConnectUnknown, Hint: "The file could not be opened.", Err: err}
}

// statementError carries a server failure as the contract's
// StatementError (FR-5.10).
func statementError(err error, ctx context.Context) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return &source.StatementError{Err: err, Message: source.Message{
		Level: source.MessageError, Text: firstLine(err.Error()), Code: errorCode(err)}}
}

// firstLine is the message without the lines of context under it, which
// DuckDB draws with an arrow under the offending word and which are wider
// than the place the message is shown.
func firstLine(msg string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(msg), "\n")
	return strings.TrimSpace(line)
}

// errorCode is the kind DuckDB names its errors by — "Catalog Error",
// "Binder Error", "Constraint Error" — which is the nearest thing it has
// to the code every other engine here gives (FR-5.10).
func errorCode(err error) string {
	name, _, found := strings.Cut(err.Error(), ":")
	if !found || !strings.HasSuffix(strings.TrimSpace(name), "Error") {
		return ""
	}
	return strings.TrimSpace(name)
}
