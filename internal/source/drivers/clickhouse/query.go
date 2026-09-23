package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Running what somebody typed (FR-5.1 … FR-5.5).
//
// A statement runs on a pinned connection, so that what it leaves behind —
// a SET, a temporary table — is still there for the next one. That is what
// a query tab means by a session.
//
// Every statement a session runs is named after the session and numbered
// within it, so there is a name to stop it by from another connection
// (source.Killer). Numbered rather than named once: a statement somebody
// stopped is still running at the server for a moment afterwards, and
// ClickHouse refuses a name that a running statement already has.
//
// Stopping a statement costs the connection it was running on: the driver
// tells the server to cancel and then has nothing usable left, and
// database/sql retires it. A tab whose stop button left it unable to run
// anything again would be worse than one that lost a temporary table, so
// the session takes a new connection — keeping its name, which is what
// anything stopping it knows it by.

// multiResultCap bounds a non-final result of a script, which must be read
// into memory before the next statement can use the connection (NFR-P11).
const multiResultCap = 10000

var errClosed = errors.New("clickhouse: session is closed")

// session is one pinned connection, a query tab's.
type session struct {
	src  *clickhouseSource
	conn *sql.Conn
	id   string

	mu     sync.Mutex
	open   *rowStream
	closed bool
	seq    int

	// stopped records that a statement was stopped on this connection,
	// which is the end of it. The next statement takes a new one.
	stopped atomic.Bool
}

// Handle is the session's name, read without waiting on the statement that
// is running: what wants the name is whatever is about to stop that
// statement, and it cannot wait for it to end. The name is given once and
// never changes, so there is nothing to wait for.
func (ss *session) Handle() string { return ss.id }

// renew replaces the connection a stopped statement took with it. It is
// called with ss.mu held.
//
// A failure to open one is left for the next statement to report: this is
// the tail of a statement somebody stopped, and a second error about it
// would say nothing they do not already know.
func (ss *session) renew() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := ss.src.db.Conn(ctx)
	if err != nil {
		return
	}
	old := ss.conn
	ss.conn = c
	old.Close()
}

func (ss *session) Close() error {
	ss.mu.Lock()
	if ss.closed {
		ss.mu.Unlock()
		return nil
	}
	ss.closed = true
	if ss.open != nil {
		ss.open.Close()
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
		ss.open.Close()
		ss.open = nil
	}
	if ss.stopped.Swap(false) {
		ss.renew()
	}

	start := time.Now()
	if !returnsRows(text) {
		// A statement with no result set has to be sent as one: asking this
		// driver for rows a statement does not have ends the connection,
		// not just the statement.
		res, err := ss.conn.ExecContext(ss.named(ctx), text, args...)
		if err != nil {
			if ctx.Err() != nil {
				ss.stopped.Store(true)
			}
			return nil, statementError(err, ctx)
		}
		if err := ctx.Err(); err != nil {
			return nil, err // stopped: what it changed is not a count to report as done
		}
		// ClickHouse reports what a statement wrote as progress rather than
		// as a count of rows changed, and an INSERT … SELECT writes rows
		// nobody counted. Saying nothing is better than saying zero.
		affected := int64(-1)
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			affected = n
		}
		return &source.Result{Affected: affected, Duration: time.Since(start)}, nil
	}
	rows, err := ss.conn.QueryContext(ss.named(ctx), text, args...)
	if err != nil {
		if ctx.Err() != nil {
			ss.stopped.Store(true)
		}
		return nil, statementError(err, ctx)
	}
	stream, err := newRowStream(rows, model.ObjectRef{})
	if err != nil {
		return nil, statementError(err, ctx)
	}
	stream.ctx = ctx
	// A result read to its end leaves the connection as it was; one cut
	// short by a stop takes it with it.
	stream.onClose = func() {
		if ctx.Err() != nil {
			ss.stopped.Store(true)
		}
	}
	ss.open = stream
	return &source.Result{Rows: stream, Affected: -1, Duration: time.Since(start)}, nil
}

// rowStatements are the words a statement that answers with rows begins
// with. Everything else is sent as a statement with no result.
//
// Which it is has to be decided before it is sent: clickhouse-go answers a
// request for rows that a statement does not have by ending the connection.
// Being wrong the other way costs a result nobody sees, which is the milder
// of the two, so only the words that certainly answer are listed.
var rowStatements = map[string]bool{
	"select": true, "with": true, "show": true, "describe": true, "desc": true,
	"explain": true, "exists": true, "check": true, "values": true,
}

// returnsRows reports whether a statement answers with a result set.
func returnsRows(sql string) bool {
	w := words(sql)
	return len(w) > 0 && rowStatements[w[0]]
}

// named gives the statement a name beginning with the session's, so that
// KillQuery has something to stop. Called with ss.mu held.
func (ss *session) named(ctx context.Context) context.Context {
	ss.seq++
	return ch.Context(ctx, ch.WithQueryID(ss.id+"-"+strconv.Itoa(ss.seq)))
}

// bindNamed rewrites :name parameters to ? (FR-5.7). ClickHouse's own
// placeholders are positional here, so a name used twice is bound twice.
func bindNamed(stmt source.Statement) (string, []any, error) {
	if len(stmt.Named) == 0 {
		return stmt.SQL, stmt.Args, nil
	}
	if len(stmt.Args) > 0 {
		return "", nil, errors.New("clickhouse: a statement cannot mix positional and named parameters")
	}
	text, args, err := sqlscript.BindNamed(sqllex.ClickHouse, stmt.SQL, stmt.Named,
		func(int) string { return "?" }, false)
	if err != nil {
		return "", nil, fmt.Errorf("clickhouse: %w", err)
	}
	return text, args, nil
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

// Session pins a connection and gives it a name of its own.
func (s *clickhouseSource) Session(ctx context.Context) (source.Session, error) {
	c, err := s.db.Conn(ctx)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	return &session{src: s, conn: c, id: "ikigai-" + uuid.NewString()}, nil
}

// Query runs one statement on a session of its own, released when the
// result is closed.
func (s *clickhouseSource) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
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
func (s *clickhouseSource) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
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
	o.owner.Close()
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

// Distinct lists a column's values among the rows the filters select, most
// frequent first (source.DistinctLister, FR-3.4).
func (s *clickhouseSource) Distinct(ctx context.Context, ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) ([]source.DistinctValue, error) {
	stmt, err := s.buildDistinct(ref, column, opt, limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	st, err := newRowStream(rows, ref)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	var out []source.DistinctValue
	for {
		row, err := st.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		n, _ := row[1].(int64)
		out = append(out, source.DistinctValue{Value: row[0], Count: n})
	}
}
