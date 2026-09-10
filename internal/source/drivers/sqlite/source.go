// Package sqlite is the SQLite driver (T1.38), over modernc.org/sqlite: pure
// Go, so the application stays a single binary with no C toolchain.
//
// A connection is a database file. Opening never creates one: a mistyped path
// must fail, not produce an empty database that looks like data went missing.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers "sqlite" with database/sql

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
)

const driverID = "sqlite"

func init() { source.Register(Driver{}) }

// Driver opens SQLite database files.
type Driver struct{}

func (Driver) Describe() source.Descriptor {
	return source.Descriptor{
		ID: driverID, Name: "SQLite", Paradigm: model.ParadigmRelational,
		Fields: []source.Field{{Key: "database", Label: "File", Kind: source.FieldFile, Required: true,
			Help: "The database file. It must exist already."}},
	}
}

// Open opens the file and proves it is an SQLite database, and nothing more
// (source.Driver: test connection must be fast and precise).
func (Driver) Open(ctx context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	path := strings.TrimSpace(cfg.Database)
	if path == "" {
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "Choose a database file."}
	}
	fi, err := os.Stat(path)
	switch {
	case err != nil:
		return nil, &source.ConnectError{Kind: source.ConnectNoDatabase, Hint: "The database file does not exist.", Err: err}
	case fi.IsDir():
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "That is a folder, not a database file."}
	}
	db, err := sql.Open("sqlite", dsn(path, cfg.Guard.ReadOnly))
	if err != nil {
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "The file could not be opened.", Err: err}
	}
	// SQLite reads the header lazily: a file that is not a database fails
	// only when something is read from it, so read the smallest thing.
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema`).Scan(&n); err != nil {
		db.Close()
		return nil, &source.ConnectError{Kind: source.ConnectConfig, Hint: "The file is not an SQLite database.", Err: err}
	}
	return &sqliteSource{db: db, cfg: cfg, path: path, sessions: map[string]*session{},
		identities: map[string]model.RowIdentity{}}, nil
}

// dsn builds the connection string. Pragmas go in it, not in an Exec after
// opening, because database/sql pools connections and pragmas are per
// connection. A read-only connection opens the file read-only and refuses
// writes at the engine too: the guard's classification is the first defence,
// not the only one (NFR-S4).
func dsn(path string, readOnly bool) string {
	esc := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	mode := "rw"
	if readOnly {
		mode = "ro"
	}
	s := "file:" + esc + "?mode=" + mode + "&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	if readOnly {
		s += "&_pragma=query_only(1)"
	}
	return s
}

type sqliteSource struct {
	dialect
	db   *sql.DB
	cfg  source.ConnectionConfig
	path string

	mu         sync.Mutex
	sessions   map[string]*session // for KillQuery
	nextID     int
	identities map[string]model.RowIdentity // browse tiebreak per table
}

var (
	_ source.Source    = (*sqliteSource)(nil)
	_ source.Queryer   = (*sqliteSource)(nil)
	_ source.Sessioner = (*sqliteSource)(nil)
	_ source.Dialect   = (*sqliteSource)(nil)
	_ source.Countable = (*sqliteSource)(nil)
	_ source.Killer    = (*sqliteSource)(nil)
)

func (s *sqliteSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Paradigm: model.ParadigmRelational,
		Query: capability.Query{
			Supported: true, Language: "sqlite", MultiStatement: true,
			Cancel: true, Parameters: true,
		},
		// A local file counts quickly, so the grid gets a real scrollbar.
		Data:   capability.Data{ServerSort: true, ServerFilter: true, ExactCount: true, DistinctValues: true},
		Schema: capability.Schema{ForeignKeys: true},
		Objects: map[model.ObjectKind]bool{
			model.KindFolder: true, model.KindTable: true, model.KindView: true, model.KindColumn: true,
		},
	}
}

func (s *sqliteSource) Info(ctx context.Context) (source.ServerInfo, error) {
	start := time.Now()
	var v string
	if err := s.db.QueryRowContext(ctx, `SELECT sqlite_version()`).Scan(&v); err != nil {
		return source.ServerInfo{}, err
	}
	return source.ServerInfo{Product: "SQLite", Version: v, Latency: time.Since(start),
		Attrs: map[string]string{"file": s.path}}, nil
}

func (s *sqliteSource) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *sqliteSource) Close() error                   { return s.db.Close() }

// Count is the number of rows a browse would return (source.Countable).
func (s *sqliteSource) Count(ctx context.Context, ref model.ObjectRef, opt source.BrowseOptions) (int64, error) {
	stmt, err := s.buildCount(ref, opt)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := s.db.QueryRowContext(ctx, stmt.SQL, stmt.Args...).Scan(&n); err != nil {
		return 0, statementError(err)
	}
	return n, nil
}

// KillQuery interrupts what a session is running (source.Killer). SQLite runs
// in this process, so interrupting is cancelling the statement's context,
// which modernc turns into sqlite3_interrupt.
func (s *sqliteSource) KillQuery(_ context.Context, handle string) error {
	s.mu.Lock()
	ss := s.sessions[handle]
	s.mu.Unlock()
	if ss == nil {
		return fmt.Errorf("sqlite: no session %q", handle)
	}
	ss.kill()
	return nil
}

func (s *sqliteSource) Session(ctx context.Context) (source.Session, error) {
	c, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.nextID++
	ss := &session{src: s, conn: c, id: strconv.Itoa(s.nextID)}
	s.sessions[ss.id] = ss
	s.mu.Unlock()
	return ss, nil
}

func (s *sqliteSource) forget(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// Query runs one statement on a session of its own, released when the
// result is closed.
func (s *sqliteSource) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	ss, err := s.Session(ctx)
	if err != nil {
		return nil, err
	}
	res, err := ss.Query(ctx, stmt)
	if err != nil || res.Rows == nil {
		ss.Close()
		return res, err
	}
	res.Rows = &ownedStream{RowStream: res.Rows, owner: ss}
	return res, nil
}

// QueryMulti runs a script on a session of its own, released when the script
// ends or, if its last result is still streaming, when that result closes.
func (s *sqliteSource) QueryMulti(ctx context.Context, script string, confirmed bool) (<-chan source.ScriptResult, error) {
	ss, err := s.Session(ctx)
	if err != nil {
		return nil, err
	}
	in, err := ss.QueryMulti(ctx, script, confirmed)
	if err != nil {
		ss.Close()
		return nil, err
	}
	out := make(chan source.ScriptResult, cap(in))
	go func() {
		defer close(out)
		owned := false
		for r := range in {
			if r.Result != nil && r.Result.Rows != nil {
				if _, buffered := r.Result.Rows.(*sliceStream); !buffered {
					r.Result.Rows = &ownedStream{RowStream: r.Result.Rows, owner: ss}
					owned = true
				}
			}
			out <- r
		}
		if !owned {
			ss.Close()
		}
	}()
	return out, nil
}

// statementError carries an SQLite failure as the contract's StatementError.
// SQLite gives no error position the driver can read, so the editor marks
// nothing; the message still names the problem.
func statementError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var se *source.StatementError
	if errors.As(err, &se) {
		return err
	}
	return &source.StatementError{Message: source.Message{Level: source.MessageError,
		Text: strings.TrimPrefix(err.Error(), "SQL logic error: ")}, Err: err}
}
