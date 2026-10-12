package trino

import (
	"context"
	"database/sql"
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

// Running what somebody typed (FR-5.1 … FR-5.5).
//
// A statement runs on a pinned connection, so that what it leaves behind — a
// USE, a SET SESSION, a prepared statement — is still there for the next one.
// That is what a query tab means by a session.
//
// A session with Trino is not a socket. The server keeps nothing between
// statements: what a session is travels in each request's headers, and the
// client keeps those per connection. So pinning the client's connection is
// what pins the session, and a statement somebody stops costs the connection
// nothing — there is no half-read reply left on a wire to make it unusable.

// multiResultCap bounds a non-final result of a script, which must be read
// into memory before the next statement can use the connection (NFR-P11).
const multiResultCap = 10000

var errClosed = errors.New("trino: session is closed")

// session is one pinned connection, a query tab's.
type session struct {
	src  *trinoSource
	conn *sql.Conn

	mu     sync.Mutex
	open   *rowStream
	closed bool
}

// Handle is the name a statement is stopped by from another connection, and
// this has none: a statement here is stopped by ending its context, which the
// client tells the coordinator about (see Capabilities).
func (ss *session) Handle() string { return "" }

func (ss *session) Close() error {
	ss.mu.Lock()
	if ss.closed {
		ss.mu.Unlock()
		return nil
	}
	ss.closed = true
	if ss.open != nil {
		_ = ss.open.Close()
	}
	ss.mu.Unlock()
	if err := ss.conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		return err
	}
	return nil
}

// Query runs one statement. One without rows reports how many it changed;
// one with rows streams them.
func (ss *session) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	access := ss.src.Classify(stmt.SQL)
	if err := ss.src.cfg.Guard.AllowStatement(access, unboundedIn(stmt.SQL), stmt.Confirmed); err != nil {
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
		_ = ss.open.Close()
		ss.open = nil
	}

	start := time.Now()
	if !returnsRows(text) {
		res, err := ss.conn.ExecContext(ctx, text, args...)
		if err != nil {
			return nil, positioned(err, ctx, text)
		}
		if err := ctx.Err(); err != nil {
			return nil, err // stopped: what it changed is not a count to report as done
		}
		// What Trino says a statement changed, where it says: a CREATE or a
		// USE changes no rows and says nothing, and nothing is not nought.
		affected := int64(-1)
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			affected = n
		}
		return &source.Result{Affected: affected, Duration: time.Since(start)}, nil
	}
	rows, err := ss.conn.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, positioned(err, ctx, text)
	}
	stream, err := newRowStream(rows, model.ObjectRef{})
	if err != nil {
		return nil, positioned(err, ctx, text)
	}
	stream.ctx = ctx
	ss.open = stream
	return &source.Result{Rows: stream, Affected: -1, Duration: time.Since(start)}, nil
}

// rowStatements are the words a statement that answers with rows begins
// with. Everything else is sent as a statement with no result.
//
// Being wrong one way costs a result nobody sees and being wrong the other
// shows an empty one, so only the words that certainly answer are listed.
// EXECUTE is among them because what it runs is usually a query, and a
// statement asked for its rows that has none says so harmlessly here.
var rowStatements = map[string]bool{
	"select": true, "with": true, "show": true, "describe": true, "desc": true,
	"explain": true, "values": true, "table": true, "execute": true,
}

// returnsRows reports whether a statement answers with a result set.
func returnsRows(sql string) bool {
	w := words(sql)
	return len(w) > 0 && rowStatements[w[0]]
}

// bindNamed rewrites :name parameters to ? (FR-5.7). Trino's placeholders are
// positional, so a name used twice is bound twice.
func bindNamed(stmt source.Statement) (string, []any, error) {
	b := &builder{}
	if len(stmt.Named) == 0 {
		for _, a := range stmt.Args {
			b.bind(a)
		}
		return stmt.SQL, b.args, nil
	}
	if len(stmt.Args) > 0 {
		return "", nil, errors.New("trino: a statement cannot mix positional and named parameters")
	}
	text, args, err := sqlscript.BindNamed(sqllex.Trino, stmt.SQL, stmt.Named,
		func(int) string { return "?" }, false)
	if err != nil {
		return "", nil, fmt.Errorf("trino: %w", err)
	}
	for _, a := range args {
		b.bind(a)
	}
	return text, b.args, nil
}

// QueryMulti runs a script. Every statement is checked against the guard
// before any runs (source.Queryer).
func (ss *session) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
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
		// A panic fails the statement it happened in, as its error: the
		// buffer holds one more result than have been sent (NFR-R1).
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
				buf, truncated, err := buffer(ctx, r.Result.Rows, multiResultCap)
				r.Result.Rows, r.Err = buf, err
				if truncated {
					r.Result.Messages = append(r.Result.Messages, source.Message{Level: source.MessageWarning,
						Text: fmt.Sprintf("showing the first %d rows; run this statement alone to see all of them", multiResultCap)})
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

// Session pins a connection, and with it the catalog, the schema and the
// properties a tab has set.
func (s *trinoSource) Session(ctx context.Context) (source.Session, error) {
	c, err := s.db.Conn(ctx)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	return &session{src: s, conn: c}, nil
}

// Query runs one statement on a session of its own, released when the
// result is closed.
func (s *trinoSource) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
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

// QueryMulti runs a script on a session of its own, released when the
// script ends or, if its last result is still streaming, when that closes.
func (s *trinoSource) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	ss, err := s.Session(ctx)
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

type ownedStream struct {
	model.RowStream
	owner io.Closer
}

func (o *ownedStream) Close() error {
	err := o.RowStream.Close()
	_ = o.owner.Close()
	return err
}

type sliceStream struct {
	cols []model.ColumnDef
	rows []model.Row
	i    int
}

func (s *sliceStream) Columns() []model.ColumnDef { return s.cols }
func (s *sliceStream) Close() error               { return nil }
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

func buffer(ctx context.Context, rs model.RowStream, n int) (model.RowStream, bool, error) {
	defer rs.Close()
	out := &sliceStream{cols: rs.Columns()}
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
