package mysql

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

// multiResultCap bounds a non-final result of a script, which must be read
// into memory before the next statement can use the connection (NFR-P11).
const multiResultCap = 10000

var errClosed = errors.New("mysql: session is closed")

// session is one pinned connection, a query tab's, so user variables,
// temporary tables and an open transaction survive between runs.
type session struct {
	src    *mysqlSource
	conn   *sql.Conn
	connID int64

	mu     sync.Mutex
	open   *rowStream
	closed bool
}

func (ss *session) Handle() string { return strconv.FormatInt(ss.connID, 10) }

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
	return ss.conn.Close()
}

// watch stops the session's running statement with KILL QUERY when ctx ends
// before stop is called. Not by cancelling the driver's context: go-sql-driver
// closes a connection whose context is cancelled, and the session — its
// variables, temporary tables, open transaction — would go with it. Cancel
// keeps the session, as on PostgreSQL (ADR-0008).
func (ss *session) watch(ctx context.Context) (stop func()) {
	done := make(chan struct{})
	go func() {
		defer panics.Catch("stopping a statement", func(error) {})
		select {
		case <-ctx.Done():
			select {
			case <-done: // finished just as ctx ended: nothing to stop
			default:
				ss.src.kill(ss.connID)
			}
		case <-done:
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// Query runs one statement. One without rows reports how many it changed;
// one with rows streams them.
func (ss *session) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	access := ss.src.Classify(stmt.SQL)
	if err := ss.src.cfg.Guard.Allow(access, stmt.Confirmed); err != nil {
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

	stop := ss.watch(ctx)
	run := context.WithoutCancel(ctx)
	start := time.Now()
	rows, err := ss.conn.QueryContext(run, text, args...)
	if err != nil {
		stop()
		return nil, statementError(err, ctx)
	}
	if err := ctx.Err(); err != nil {
		// Stopped while it ran. Some statements return normally when killed —
		// an interrupted SLEEP() or BENCHMARK() yields a row — and showing
		// that row would pass a stopped query off as a finished one.
		rows.Close()
		stop()
		return nil, err
	}
	cols, _ := rows.Columns()
	if len(cols) == 0 {
		for rows.Next() {
		}
		err := rows.Err()
		rows.Close()
		stop()
		if err != nil {
			return nil, statementError(err, ctx)
		}
		if err := ctx.Err(); err != nil {
			return nil, err // stopped: whatever it changed is not a count to report as done
		}
		affected := int64(-1)
		if access == source.AccessWrite {
			// database/sql hides the count for a statement run as a query;
			// ROW_COUNT() on the same connection has it.
			ss.conn.QueryRowContext(run, `SELECT ROW_COUNT()`).Scan(&affected)
		}
		return &source.Result{Affected: affected, Duration: time.Since(start)}, nil
	}
	stream, err := newRowStream(rows, model.ObjectRef{}, model.RowIdentity{Kind: model.IdentityNone})
	if err != nil {
		stop()
		return nil, statementError(err, ctx)
	}
	stream.ctx = ctx
	stream.onClose = stop
	// Closing a half-read result makes the driver read the rest; stopping the
	// query first makes that immediate however many rows were left.
	stream.abandon = func() { ss.src.kill(ss.connID) }
	ss.open = stream
	return &source.Result{Rows: stream, Affected: -1, Duration: time.Since(start)}, nil
}

// bindNamed rewrites :name parameters to ? (FR-5.7), a value to each use:
// MySQL numbers its placeholders by place, so a name used twice is bound
// twice.
func bindNamed(stmt source.Statement) (string, []any, error) {
	if len(stmt.Named) == 0 {
		return stmt.SQL, stmt.Args, nil
	}
	if len(stmt.Args) > 0 {
		return "", nil, errors.New("mysql: a statement cannot mix positional and named parameters")
	}
	sql, args, err := sqlscript.BindNamed(sqllex.MySQL, stmt.SQL, stmt.Named, func(int) string { return "?" }, false)
	if err != nil {
		return "", nil, fmt.Errorf("mysql: %w", err)
	}
	return sql, args, nil
}

// QueryMulti runs a script. Every statement is checked against the guard
// before any runs (source.Queryer).
func (ss *session) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	confirmed := opts.Confirmed
	stmts := ss.src.SplitScript(script)
	for _, st := range stmts {
		if err := ss.src.cfg.Guard.Allow(ss.src.Classify(st.Text), confirmed); err != nil {
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
