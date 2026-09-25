package firebird

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
)

// multiResultCap bounds a non-final result of a script, which must be read
// into memory before the next statement can use the connection (NFR-P11).
const multiResultCap = 10000

var errClosed = errors.New("firebird: session is closed")

// session is one pinned connection: an editor tab's, so a temporary object, a
// SET and an open transaction survive between runs.
type session struct {
	src  *firebirdSource
	conn *sql.Conn
	id   string

	mu     sync.Mutex
	open   *rowStream // a connection holds one result at a time
	closed bool
	// tx is the explicit transaction somebody opened, while one is open
	// (transaction.go). Every statement runs inside it until it ends.
	tx *sql.Tx

	cancelMu sync.Mutex
	cancel   context.CancelFunc // the running statement's, for KillQuery
}

// Session pins a connection for an editor tab.
func (s *firebirdSource) Session(ctx context.Context) (source.Session, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, statementError(err, ctx, "")
	}
	s.mu.Lock()
	s.nextID++
	ss := &session{src: s, conn: conn, id: strconv.Itoa(s.nextID)}
	s.sessions[ss.id] = ss
	s.mu.Unlock()
	return ss, nil
}

// forget drops a session from the table KillQuery looks in.
func (s *firebirdSource) forget(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

// Query runs one statement on a connection of its own, for the callers that
// have no session of their own to run it on.
func (s *firebirdSource) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	ss, err := s.Session(ctx)
	if err != nil {
		return nil, err
	}
	res, err := ss.Query(ctx, stmt)
	if err != nil {
		ss.Close()
		return nil, err
	}
	if res.Rows == nil {
		ss.Close()
		return res, nil
	}
	// The rows are still on that connection, so it is the result's to release.
	res.Rows = &ownedStream{RowStream: res.Rows, owner: ss}
	return res, nil
}

// QueryMulti runs a script on a connection of its own.
func (s *firebirdSource) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
	ss, err := s.Session(ctx)
	if err != nil {
		return nil, err
	}
	out, err := ss.QueryMulti(ctx, script, opts)
	if err != nil {
		ss.Close()
		return nil, err
	}
	// The session outlives this call and is closed when the script ends.
	relay := make(chan source.ScriptResult, cap(out))
	go func() {
		defer close(relay)
		defer ss.Close()
		for r := range out {
			relay <- r
		}
	}()
	return relay, nil
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
	if ss.tx != nil {
		// A connection closing with a transaction open on it would hold its
		// locks until the server noticed. Rolling back is the safe end: a
		// transaction nobody committed was not meant to commit.
		_ = ss.tx.Rollback()
		ss.tx = nil
	}
	if ss.open != nil {
		ss.open.Close()
	}
	ss.mu.Unlock()
	ss.src.forget(ss.id)
	return ss.conn.Close()
}

// Query runs one statement. A statement that returns no rows reports how many
// it changed; one that does, INSERT … RETURNING included, streams them.
func (ss *session) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	access := ss.src.Classify(stmt.SQL)
	if err := ss.src.cfg.Guard.AllowStatement(access, unboundedIn(stmt.SQL), stmt.Confirmed); err != nil {
		return nil, err
	}
	args := append([]any(nil), stmt.Args...)
	for k, v := range stmt.Named {
		args = append(args, sql.Named(k, v)) // the library binds :name itself
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

	start := time.Now()
	rows, err := ss.where().QueryContext(qctx, stmt.SQL, args...)
	if err != nil {
		cancel()
		return nil, statementError(err, ctx, stmt.SQL)
	}
	cols, _ := rows.Columns()
	if len(cols) == 0 {
		for rows.Next() {
		}
		err := rows.Err()
		rows.Close()
		cancel()
		if err != nil {
			return nil, statementError(err, ctx, stmt.SQL)
		}
		// How many rows a statement changed is not available: the library
		// reports it from Exec, and everything here is a query so that a
		// statement returning rows and one that does not can be told apart
		// by what came back rather than by guessing from its text first.
		return &source.Result{Affected: -1, Duration: time.Since(start)}, nil
	}
	stream, err := newRowStream(rows, model.ObjectRef{}, model.RowIdentity{Kind: model.IdentityNone})
	if err != nil {
		cancel()
		return nil, statementError(err, ctx, stmt.SQL)
	}
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
// rows are told apart.
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
