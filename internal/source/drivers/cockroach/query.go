package cockroach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Read-only enforcement (NFR-S4) is three layers, because no single one is
// enough:
//
//  1. Classify refuses anything not confidently read-only, before it
//     reaches the server. It cannot see inside a routine body.
//  2. On a read-only connection every statement runs inside an explicit
//     READ ONLY transaction, so the server refuses writes the lexer could
//     not see. An explicit transaction is immune to the session default
//     being changed underneath it, which is what set_config would do.
//  3. default_transaction_read_only is set as the connection opens, as a
//     backstop for any path that forgets layer 2.

// multiResultCap bounds how many rows of a non-final result QueryMulti
// keeps.
const multiResultCap = 1000

var (
	_ source.Killer  = (*crdbSource)(nil)
	_ source.Session = (*crdbSession)(nil)
)

// querier is whatever a statement is sent through: a pooled connection, or
// the read-only transaction wrapped round a single statement.
type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// crdbSession pins one pooled connection, so that a SET or a temporary
// table made by one statement is there for the next.
type crdbSession struct {
	src    *crdbSource
	conn   *pgxpool.Conn
	sink   *noticeSink
	handle string

	mu     sync.Mutex
	open   *rowStream
	closed bool
}

// Session opens a pinned connection.
func (s *crdbSource) Session(ctx context.Context) (source.Session, error) {
	return s.openSession(ctx)
}

func (s *crdbSource) openSession(ctx context.Context) (*crdbSession, error) {
	p, err := s.conn()
	if err != nil {
		return nil, err
	}
	c, err := p.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	ss := &crdbSession{src: s, conn: c, sink: &noticeSink{}}
	// The session's own name, asked for once as it opens.
	//
	// Not the backend process id, which CockroachDB reports but has no use
	// for: there is no pg_cancel_backend here. What stops a statement is
	// CANCEL QUERY, and what finds the statement to stop is the session it
	// is running in (ADR-0145).
	if err := c.QueryRow(ctx, "SHOW session_id").Scan(&ss.handle); err != nil {
		c.Release()
		return nil, statementError(err, ctx)
	}
	s.notices.Store(c.Conn().PgConn(), ss.sink)
	return ss, nil
}

// Handle is the session's name, which is what anything stopping its
// statement knows it by. It never changes while the session is open, so it
// is readable while a statement is running — which is the only moment
// anything wants it.
func (ss *crdbSession) Handle() string { return ss.handle }

func (ss *crdbSession) Close() error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return nil
	}
	ss.closed = true
	if ss.open != nil {
		ss.open.Close()
	}
	ss.src.notices.Delete(ss.conn.Conn().PgConn())
	ss.conn.Release()
	return nil
}

// Query runs one statement on the session's connection.
func (ss *crdbSession) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	if err := ss.src.cfg.Guard.AllowStatement(ss.src.Classify(stmt.SQL),
		unboundedIn(stmt.SQL), stmt.Confirmed); err != nil {
		return nil, err
	}
	sql, args, err := bindNamed(stmt)
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
	ss.sink.drain()

	var q querier = ss.conn
	var tx pgx.Tx
	if ss.src.cfg.Guard.ReadOnly {
		if tx, err = ss.conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}); err != nil {
			return nil, err
		}
		q = tx
	}

	// DescribeExec rather than the cached-statement default. Ad-hoc SQL is
	// rarely repeated, and a cached plan goes stale the moment somebody
	// alters the table it reads.
	start := time.Now()
	rows, err := q.Query(ctx, sql, append([]any{pgx.QueryExecModeDescribeExec, resultFormats}, args...)...)
	if err != nil {
		endTx(tx)
		return nil, statementError(err, ctx)
	}

	if len(rows.FieldDescriptions()) == 0 {
		for rows.Next() {
		}
		rows.Close()
		err := rows.Err()
		endTx(tx)
		if err != nil {
			return nil, statementError(err, ctx)
		}
		return &source.Result{Affected: affected(rows.CommandTag()),
			Duration: time.Since(start), Messages: ss.sink.drain()}, nil
	}

	stream := ss.src.newRowStream(ctx, rows, ss.src.primary, model.ObjectRef{},
		model.RowIdentity{Kind: model.IdentityNone})
	stream.onClose = func() { endTx(tx) }
	ss.open = stream
	return &source.Result{Rows: stream, Affected: -1,
		Duration: time.Since(start), Messages: ss.sink.drain()}, nil
}

// QueryMulti runs a script's statements in order on this one connection,
// which is what makes a SET carry from one statement to the next. It stops
// at the first error.
func (ss *crdbSession) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
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
				// The next statement needs this connection, which cannot
				// hold two open results, so a non-final result is buffered
				// — up to a bound, because NFR-P11 forbids unbounded reads.
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
// result is closed. Callers that need session state use Session instead.
func (s *crdbSource) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
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

// QueryMulti runs a script on a connection of its own — one connection for
// the whole script, never one per statement.
func (s *crdbSource) QueryMulti(ctx context.Context, script string, opts source.ScriptOptions) (<-chan source.ScriptResult, error) {
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
			// Only a still-streaming result holds the connection; buffered
			// ones do not. Whoever closes the streaming one releases it.
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

// KillQuery stops whatever a session is running, from a second connection
// — which is how a statement is stopped when the connection running it is
// waiting for its result.
//
// CockroachDB has no pg_cancel_backend. What it has is CANCEL QUERIES over
// a result, so the statements to stop are chosen by the session they are
// running in, and the server says how many it stopped.
func (s *crdbSource) KillQuery(ctx context.Context, handle string) error {
	if handle == "" {
		return errors.New("cockroach: a session handle is needed to stop a statement")
	}
	p, err := s.conn()
	if err != nil {
		return err
	}
	tag, err := p.Exec(ctx, `CANCEL QUERIES SELECT query_id FROM [SHOW CLUSTER QUERIES]
		WHERE session_id = $1`, handle)
	if err != nil {
		return statementError(err, ctx)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("cockroach: session %s is not running a statement this user can stop", handle)
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

// bindNamed rewrites :name parameters to $n (FR-5.7).
//
// The rewrite is lexical, through the same lexer as everything else, so a
// ":name" inside a string or a comment is left alone and a "::text" cast
// is not mistaken for a parameter. A name used twice binds to one value.
func bindNamed(stmt source.Statement) (string, []any, error) {
	if len(stmt.Named) == 0 {
		return stmt.SQL, stmt.Args, nil
	}
	if len(stmt.Args) > 0 {
		return "", nil, errors.New("cockroach: a statement cannot mix positional and named parameters")
	}
	sql, args, err := sqlscript.BindNamed(sqllex.PostgreSQL, stmt.SQL, stmt.Named,
		func(n int) string { return "$" + strconv.Itoa(n) }, true)
	if err != nil {
		return "", nil, fmt.Errorf("cockroach: %w", err)
	}
	return sql, args, nil
}

// statementError lifts a server error into the contract's form, carrying
// the position the editor underlines (FR-5.10).
func statementError(err error, ctx context.Context) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	// 57014 is the server's word for a statement somebody stopped.
	if pgErr.Code == "57014" {
		return context.Canceled
	}
	return &source.StatementError{
		Message: source.Message{Level: source.MessageError, Text: pgErr.Message,
			Code: pgErr.Code, Position: int(pgErr.Position)},
		Detail: pgErr.Detail, Hint: pgErr.Hint, Err: err,
	}
}

// endTx ends a read-only wrapper transaction by committing it.
//
// Commit, not rollback. A READ ONLY transaction cannot have written
// anything, so committing is safe, while SET is transactional: rolling
// back would silently undo a legitimate "SET search_path" on a read-only
// connection, and the next statement would run somewhere else.
//
// It uses its own context: the query's may already be cancelled, and the
// connection must be left clean regardless.
func endTx(tx pgx.Tx) {
	if tx == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tx.Commit(ctx); err != nil {
		_ = tx.Rollback(ctx) // an aborted transaction cannot commit
	}
}

// affected returns the row count a command tag reports, or -1 for commands
// that report none — "CREATE TABLE" reports nothing, not zero rows.
func affected(tag pgconn.CommandTag) int64 {
	s := tag.String()
	if s == "" || s[len(s)-1] < '0' || s[len(s)-1] > '9' {
		return -1
	}
	return tag.RowsAffected()
}
