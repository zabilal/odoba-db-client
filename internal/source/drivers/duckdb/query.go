//go:build duckdb

package duckdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Read-only enforcement (NFR-S4) is two layers here rather than three.
//
//  1. Classify refuses anything not confidently read-only, before the
//     engine sees it. It cannot see inside a routine body.
//  2. The file itself is opened read-only, and no statement can undo
//     that: access_mode is settled when the database is opened and DuckDB
//     will not have it changed afterwards.
//
// The second is why there is no third. Every server engine here needs a
// transaction wrapped round each statement because its read-only setting
// is a session default that a routine could turn off underneath it. A
// file opened read-only is not a setting; it is how the file is held.

// multiResultCap bounds how many rows of a non-final result QueryMulti
// keeps.
const multiResultCap = 1000

var (
	_ source.Killer  = (*duckSource)(nil)
	_ source.Session = (*duckSession)(nil)
)

// duckSession pins one connection, so that a SET or a temporary table
// made by one statement is there for the next.
type duckSession struct {
	src  *duckSource
	conn *sql.Conn
	id   string

	mu     sync.Mutex
	open   *rowStream // a connection holds one result at a time
	closed bool

	cancelMu sync.Mutex
	cancel   context.CancelFunc // the running statement's, for KillQuery
}

func (ss *duckSession) Handle() string { return ss.id }

// Session opens a pinned connection.
func (s *duckSource) Session(ctx context.Context) (source.Session, error) {
	return s.openSession(ctx)
}

func (s *duckSource) openSession(ctx context.Context) (*duckSession, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	c, err := db.Conn(ctx)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	ss := &duckSession{src: s, conn: c, id: newHandle()}
	s.sessions.Store(ss.id, ss)
	return ss, nil
}

// newHandle names a session. There is no server to ask: a DuckDB database
// is this process, so what stops a statement is this process, and the
// name only has to tell one session from another.
func newHandle() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (ss *duckSession) Close() error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return nil
	}
	ss.closed = true
	if ss.open != nil {
		ss.open.Close()
	}
	ss.src.sessions.Delete(ss.id)
	return ss.conn.Close()
}

// stop ends whatever statement this session is running.
func (ss *duckSession) stop() bool {
	ss.cancelMu.Lock()
	defer ss.cancelMu.Unlock()
	if ss.cancel == nil {
		return false
	}
	ss.cancel()
	return true
}

func (ss *duckSession) watch(cancel context.CancelFunc) {
	ss.cancelMu.Lock()
	ss.cancel = cancel
	ss.cancelMu.Unlock()
}

// Query runs one statement on the session's connection.
func (ss *duckSession) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	if err := ss.src.cfg.Guard.AllowStatement(ss.src.Classify(stmt.SQL),
		unboundedIn(stmt.SQL), stmt.Confirmed); err != nil {
		return nil, err
	}
	text, args, err := bindNamed(stmt)
	if err != nil {
		return nil, err
	}

	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return nil, errClosed
	}
	if ss.open != nil {
		ss.open.Close() // a connection holds one result; the new run supersedes it
		ss.open = nil
	}

	// The statement runs on a context this session can end, which is what
	// stopping it from elsewhere ends (source.Killer).
	runCtx, cancel := context.WithCancel(ctx)
	ss.watch(cancel)

	start := time.Now()
	rows, err := ss.conn.QueryContext(runCtx, text, args...)
	if err != nil {
		cancel()
		return nil, statementError(err, runCtx)
	}
	cols, err := rows.Columns()
	if err != nil {
		rows.Close()
		cancel()
		return nil, statementError(err, runCtx)
	}
	// DuckDB answers a statement that changes rows with one column saying
	// how many, so a result of Count is read as a count rather than shown
	// as a row somebody has to interpret.
	if len(cols) == 1 && cols[0] == "Count" {
		affected, err := countOf(rows)
		rows.Close()
		cancel()
		if err != nil {
			return nil, statementError(err, runCtx)
		}
		return &source.Result{Affected: affected, Duration: time.Since(start)}, nil
	}
	if len(cols) == 0 {
		rows.Close()
		cancel()
		return &source.Result{Affected: -1, Duration: time.Since(start)}, nil
	}

	stream, err := newRowStream(rows, model.ObjectRef{}, model.RowIdentity{Kind: model.IdentityNone}, nil)
	if err != nil {
		cancel()
		return nil, statementError(err, runCtx)
	}
	stream.onClose = cancel
	ss.open = stream
	return &source.Result{Rows: stream, Affected: -1, Duration: time.Since(start)}, nil
}

// countOf reads the number a change reports.
func countOf(rows *sql.Rows) (int64, error) {
	if !rows.Next() {
		return -1, rows.Err()
	}
	var n int64
	if err := rows.Scan(&n); err != nil {
		return -1, err
	}
	return n, rows.Err()
}

// QueryMulti runs a script's statements in order on this one connection,
// which is what makes a SET carry from one statement to the next. It
// stops at the first error.
func (ss *duckSession) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	confirmed := opts.Confirmed
	stmts := ss.src.SplitScript(script)
	for _, st := range stmts {
		if err := ss.src.cfg.Guard.AllowStatement(ss.src.Classify(st.Text),
			unboundedIn(st.Text), confirmed); err != nil {
			return nil, err
		}
	}

	out := make(chan source.ScriptResult, len(stmts))
	go func() {
		defer close(out)
		current := 0
		defer panics.Catch("running a statement", func(err error) {
			st := stmts[current]
			out <- source.ScriptResult{Index: current, Offset: st.Offset, Statement: st.Text, Err: err}
		})
		for i, st := range stmts {
			current = i
			r := source.ScriptResult{Index: i, Offset: st.Offset, Statement: st.Text}
			if err := ctx.Err(); err != nil {
				r.Err = err
				out <- r
				return
			}
			r.Result, r.Err = ss.Query(ctx, source.Statement{SQL: st.Text, Confirmed: confirmed, Named: opts.Named})
			if r.Err == nil && r.Result.Rows != nil && i < len(stmts)-1 {
				// The next statement needs this connection, which cannot
				// hold two open results, so a non-final result is
				// buffered — up to a bound (NFR-P11).
				buf, truncated, err := buffer(ctx, r.Result.Rows, multiResultCap)
				r.Result.Rows, r.Err = buf, err
				if truncated {
					r.Result.Messages = append(r.Result.Messages, source.Message{
						Level: source.MessageWarning,
						Text:  fmt.Sprintf("showing the first %d rows; run this statement alone to see all of them", multiResultCap),
					})
				}
			}
			out <- r
			if r.Err != nil {
				return
			}
		}
	}()
	return out, nil
}

// Query runs one statement on a connection of its own, released when the
// result is closed.
func (s *duckSource) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	ss, err := s.openSession(ctx)
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

// QueryMulti runs a script on a connection of its own — one connection
// for the whole script, never one per statement.
func (s *duckSource) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	ss, err := s.openSession(ctx)
	if err != nil {
		return nil, err
	}
	in, err := ss.QueryMulti(ctx, script, opts)
	if err != nil {
		ss.Close()
		return nil, err
	}
	out := make(chan source.ScriptResult, cap(in))
	go func() {
		defer close(out)
		defer panics.Catch("handing on results", func(error) { ss.Close() })
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

// KillQuery stops whatever a session is running.
//
// There is no second connection to send a cancel down, because there is
// no server: a DuckDB database is this process. So a session keeps the
// means of stopping its own statement, and this finds the session by the
// name whatever wants it stopped knows it by.
func (s *duckSource) KillQuery(ctx context.Context, handle string) error {
	v, ok := s.sessions.Load(handle)
	if !ok {
		return fmt.Errorf("duckdb: there is no session %s", handle)
	}
	if !v.(*duckSession).stop() {
		return fmt.Errorf("duckdb: session %s is not running a statement", handle)
	}
	return nil
}

// ownedStream releases its owner when closed.
type ownedStream struct {
	model.RowStream
	owner io.Closer
}

func (o *ownedStream) Close() error {
	err := o.RowStream.Close()
	o.owner.Close()
	return err
}

// Identity is the result's: owning its connection does not change how its
// rows are told apart (ADR-0036).
func (o *ownedStream) Identity() model.RowIdentity { return model.IdentityOf(o.RowStream) }

// sliceStream serves rows already in memory.
type sliceStream struct {
	cols []model.ColumnDef
	rows []model.Row
	i    int
	id   model.RowIdentity
}

func (s *sliceStream) Columns() []model.ColumnDef  { return s.cols }
func (s *sliceStream) Close() error                { return nil }
func (s *sliceStream) Identity() model.RowIdentity { return s.id }

func (s *sliceStream) Next(ctx context.Context) (model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.i >= len(s.rows) {
		return nil, io.EOF
	}
	s.i++
	return s.rows[s.i-1], nil
}

// buffer reads up to n rows into memory, saying whether it stopped short.
func buffer(ctx context.Context, rs model.RowStream, n int) (model.RowStream, bool, error) {
	defer rs.Close()
	out := &sliceStream{cols: rs.Columns(), id: model.IdentityOf(rs)}
	for len(out.rows) <= n {
		row, err := rs.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		out.rows = append(out.rows, row)
	}
	out.rows = out.rows[:n]
	return out, true, nil
}

// bindNamed rewrites :name parameters to the engine's own (FR-5.7).
//
// The rewrite is lexical, through the same lexer as everything else, so a
// ":name" inside a string or a comment is left alone and a "::VARCHAR"
// cast is not mistaken for a parameter. DuckDB binds by position, so a
// name used twice is bound twice.
func bindNamed(stmt source.Statement) (string, []any, error) {
	if len(stmt.Named) == 0 {
		return stmt.SQL, stmt.Args, nil
	}
	if len(stmt.Args) > 0 {
		return "", nil, errors.New("duckdb: a statement cannot mix positional and named parameters")
	}
	text, args, err := sqlscript.BindNamed(sqllex.PostgreSQL, stmt.SQL, stmt.Named,
		func(int) string { return "?" }, false)
	if err != nil {
		return "", nil, fmt.Errorf("duckdb: %w", err)
	}
	return text, args, nil
}
