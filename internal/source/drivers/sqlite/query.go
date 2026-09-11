package sqlite

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
)

// multiResultCap bounds a non-final result of a script, which must be read
// into memory before the next statement can use the connection (NFR-P11).
const multiResultCap = 10000

var errClosed = errors.New("sqlite: session is closed")

// session is one pinned connection: an editor tab's, so temporary tables,
// PRAGMAs and an open transaction survive between runs.
type session struct {
	src  *sqliteSource
	conn *sql.Conn
	id   string

	mu     sync.Mutex
	open   *rowStream // a connection holds one result at a time
	closed bool

	cancelMu sync.Mutex
	cancel   context.CancelFunc // the running statement's, for KillQuery
}

func (ss *session) Handle() string { return ss.id }

func (ss *session) kill() {
	ss.cancelMu.Lock()
	defer ss.cancelMu.Unlock()
	if ss.cancel != nil {
		ss.cancel()
	}
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
	ss.src.forget(ss.id)
	return ss.conn.Close()
}

// Query runs one statement. A statement that returns no rows reports how
// many it changed; one that does, INSERT … RETURNING included, streams them.
func (ss *session) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	access := ss.src.Classify(stmt.SQL)
	if err := ss.src.cfg.Guard.Allow(access, stmt.Confirmed); err != nil {
		return nil, err
	}
	args := append([]any(nil), stmt.Args...)
	for k, v := range stmt.Named {
		args = append(args, sql.Named(k, v)) // SQLite binds :name itself
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

	qctx, cancel := context.WithCancel(ctx)
	ss.cancelMu.Lock()
	ss.cancel = cancel
	ss.cancelMu.Unlock()

	info := ss.columnInfo(stmt.SQL) // where each column is read from, asked before it runs
	start := time.Now()
	rows, err := ss.conn.QueryContext(qctx, stmt.SQL, args...)
	if err != nil {
		cancel()
		return nil, statementError(err)
	}
	cols, _ := rows.Columns()
	if len(cols) == 0 {
		for rows.Next() {
		}
		err := rows.Err()
		rows.Close()
		cancel()
		if err != nil {
			return nil, statementError(err)
		}
		affected := int64(-1)
		if access == source.AccessWrite {
			// database/sql hides the count for a statement run as a query;
			// changes() on the same connection has it.
			ss.conn.QueryRowContext(ctx, `SELECT changes()`).Scan(&affected)
		}
		return &source.Result{Affected: affected, Duration: time.Since(start)}, nil
	}
	stream, err := newRowStream(rows, model.ObjectRef{}, model.RowIdentity{Kind: model.IdentityNone})
	if err != nil {
		cancel()
		return nil, statementError(err)
	}
	ss.src.resultOrigins(ctx, stream, info) // origins.go
	stream.onClose = cancel
	ss.open = stream
	return &source.Result{Rows: stream, Affected: -1, Duration: time.Since(start)}, nil
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
	id   model.RowIdentity // the result's, kept with its rows
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

// buffer reads up to n rows into memory and closes the source stream.
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
