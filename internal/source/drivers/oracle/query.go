package oracle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Running what somebody typed (FR-5.1 … FR-5.4).
//
// A statement runs on a pinned connection, so that what it leaves behind —
// a session setting, a temporary table's rows — is still there for the next
// one. That is what a query tab means by a session.
//
// Each statement commits itself, as it does on every other engine here: an
// explicit transaction is the Transactor interface's business (ADR-0137),
// and this driver does not implement it yet. So a DML statement run from a
// query tab is written when it returns, and Oracle's own habit of holding
// one open until COMMIT does not apply through this driver.

// multiResultCap bounds a non-final result of a script, which must be read
// into memory before the next statement can use the connection (NFR-P11).
const multiResultCap = 10000

var errClosed = errors.New("oracle: session is closed")

// session is one pinned connection, a query tab's.
type session struct {
	src  *oracleSource
	conn *sql.Conn
	sid  string

	mu     sync.Mutex
	open   *rowStream
	closed bool
}

func (ss *session) Handle() string { return ss.sid }

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
	conn := ss.conn
	ss.mu.Unlock()
	if err := conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		return err
	}
	return nil
}

// rowStatements are the words a statement that answers with rows begins
// with. Everything else is sent as a statement with no result, which is
// also how its count of changed rows is had.
var rowStatements = map[string]bool{"select": true, "with": true}

func returnsRows(sql string) bool {
	w := words(sql)
	return len(w) > 0 && rowStatements[w[0]]
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

	start := time.Now()
	if !returnsRows(text) {
		res, err := ss.conn.ExecContext(ctx, text, args...)
		if err != nil {
			return nil, statementError(err, ctx)
		}
		if err := ctx.Err(); err != nil {
			return nil, err // stopped: what it changed is not a count to report as done
		}
		affected := int64(-1)
		if n, err := res.RowsAffected(); err == nil && access == source.AccessWrite {
			affected = n
		}
		return &source.Result{Affected: affected, Duration: time.Since(start)}, nil
	}
	rows, err := ss.conn.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	stream, err := newRowStream(rows, model.ObjectRef{}, model.RowIdentity{Kind: model.IdentityNone})
	if err != nil {
		return nil, statementError(err, ctx)
	}
	stream.ctx = ctx
	ss.open = stream
	return &source.Result{Rows: stream, Affected: -1, Duration: time.Since(start)}, nil
}

// bindNamed rewrites :name parameters to :1, :2 (FR-5.7).
//
// Oracle's own placeholders are named, and a name used twice is one
// parameter given once — which is what it already means in a statement
// somebody typed.
func bindNamed(stmt source.Statement) (string, []any, error) {
	if len(stmt.Named) == 0 {
		return stmt.SQL, stmt.Args, nil
	}
	if len(stmt.Args) > 0 {
		return "", nil, errors.New("oracle: a statement cannot mix positional and named parameters")
	}
	text, args, err := sqlscript.BindNamed(sqllex.Oracle, stmt.SQL, stmt.Named, dialect{}.Placeholder, true)
	if err != nil {
		return "", nil, fmt.Errorf("oracle: %w", err)
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

// Session pins a connection and asks the server what it is called.
func (s *oracleSource) Session(ctx context.Context) (source.Session, error) {
	c, err := s.db.Conn(ctx)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	var sid string
	if err := c.QueryRowContext(ctx,
		`SELECT sys_context('userenv', 'sid') FROM dual`).Scan(&sid); err != nil {
		c.Close()
		return nil, statementError(err, ctx)
	}
	return &session{src: s, conn: c, sid: sid}, nil
}

// Query runs one statement on a session of its own, released when the
// result is closed.
func (s *oracleSource) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
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
func (s *oracleSource) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
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

// countOf reads a count. Oracle hands every NUMBER over as its own text,
// and COUNT(*) is a NUMBER like any other, so what comes back is an exact
// number rather than a Go integer.
func countOf(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case model.Decimal:
		n, _ := strconv.ParseInt(string(x), 10, 64)
		return n
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	case float64:
		return int64(x)
	}
	return 0
}

// Distinct lists a column's values among the rows the filters select, most
// frequent first (source.DistinctLister, FR-3.4).
func (s *oracleSource) Distinct(ctx context.Context, ref model.ObjectRef, column string, opt source.BrowseOptions, limit int) ([]source.DistinctValue, error) {
	stmt, err := s.buildDistinct(ref, column, opt, limit)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, stmt.SQL, stmt.Args...)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	st, err := newRowStream(rows, ref, model.RowIdentity{Kind: model.IdentityNone})
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
		out = append(out, source.DistinctValue{Value: row[0], Count: countOf(row[1])})
	}
}
