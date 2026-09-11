package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Read-only enforcement (NFR-S4) is three layers, because no single one is
// enough:
//
//  1. Classify refuses anything not confidently read-only, before it reaches
//     the server. It cannot see inside a function body.
//  2. On a read-only connection every statement runs inside an explicit
//     READ ONLY transaction, so the server refuses writes the lexer could not
//     see. An explicit transaction is immune to the session default being
//     changed underneath it, which is what set_config would otherwise do.
//  3. default_transaction_read_only is set at connect time, as a backstop for
//     any path that forgets layer 2.

// multiResultCap bounds how many rows of a non-final result QueryMulti keeps.
const multiResultCap = 1000

var (
	_ source.Queryer   = (*pgSource)(nil)
	_ source.Sessioner = (*pgSource)(nil)
	_ source.Killer    = (*pgSource)(nil)
	_ source.Session   = (*pgSession)(nil)
)

// pgSession pins one pooled connection.
type pgSession struct {
	src  *pgSource
	conn *pgxpool.Conn
	sink *noticeSink

	mu     sync.Mutex
	open   *rowStream
	closed bool
}

// Session opens a pinned connection on the primary database.
func (s *pgSource) Session(ctx context.Context) (source.Session, error) {
	return s.openSession(ctx)
}

func (s *pgSource) openSession(ctx context.Context) (*pgSession, error) {
	p, err := s.pool(ctx, s.primary)
	if err != nil {
		return nil, err
	}
	c, err := p.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	ss := &pgSession{src: s, conn: c, sink: &noticeSink{}}
	s.notices.Store(c.Conn().PgConn(), ss.sink)
	return ss, nil
}

func (ss *pgSession) Handle() string {
	return strconv.FormatUint(uint64(ss.conn.Conn().PgConn().PID()), 10)
}

func (ss *pgSession) Close() error {
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
func (ss *pgSession) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
	if err := ss.src.cfg.Guard.Allow(ss.src.Classify(stmt.SQL), stmt.Confirmed); err != nil {
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

	type querier interface {
		Query(context.Context, string, ...any) (pgx.Rows, error)
	}
	var q querier = ss.conn
	var tx pgx.Tx
	if ss.src.cfg.Guard.ReadOnly {
		if tx, err = ss.conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}); err != nil {
			return nil, err
		}
		q = tx
	}

	// DescribeExec rather than the cached-statement default. Ad-hoc SQL is
	// rarely repeated, and a cached plan goes stale the moment the user alters
	// the table it reads ("cached plan must not change result type").
	start := time.Now()
	rows, err := q.Query(ctx, sql, append([]any{pgx.QueryExecModeDescribeExec, resultFormats}, args...)...)
	if err != nil {
		endTx(tx)
		return nil, statementError(err)
	}

	if len(rows.FieldDescriptions()) == 0 {
		for rows.Next() {
		}
		rows.Close()
		err := rows.Err()
		endTx(tx)
		if err != nil {
			return nil, statementError(err)
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
// which is what makes SET and temporary tables carry from one statement to
// the next. It stops at the first error.
func (ss *pgSession) QueryMulti(ctx context.Context, script string, confirmed bool) (<-chan source.ScriptResult, error) {
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
			r.Result, r.Err = ss.Query(ctx, source.Statement{SQL: st.Text, Confirmed: confirmed})
			if r.Err == nil && r.Result.Rows != nil && i < len(stmts)-1 {
				// The next statement needs this connection, which cannot hold
				// two open results, so a non-final result is buffered — up to
				// a bound, because NFR-P11 forbids unbounded reads.
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
func (s *pgSource) Query(ctx context.Context, stmt source.Statement) (*source.Result, error) {
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
func (s *pgSource) QueryMulti(ctx context.Context, script string, confirmed bool) (<-chan source.ScriptResult, error) {
	ss, err := s.openSession(ctx)
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

// KillQuery cancels whatever the given backend is running, from a second
// connection — which is how a statement is cancelled when the connection
// running it is busy waiting for the result.
func (s *pgSource) KillQuery(ctx context.Context, handle string) error {
	pid, err := strconv.ParseInt(handle, 10, 32)
	if err != nil {
		return fmt.Errorf("postgres: invalid session handle %q", handle)
	}
	p, err := s.pool(ctx, s.primary)
	if err != nil {
		return err
	}
	var ok bool
	if err := p.QueryRow(ctx, "SELECT pg_cancel_backend($1)", int32(pid)).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("postgres: backend %d is not running a query this user can cancel", pid)
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

// sliceStream serves rows already in memory.
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

// buffer reads up to n rows into memory and closes the source stream.
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

// bindNamed rewrites :name parameters to $n (FR-5.7).
//
// The rewrite is lexical, through the same lexer as everything else, so a
// ":name" inside a string or comment is left alone and a "::text" cast is not
// mistaken for a parameter. A name used twice binds to one value.
func bindNamed(stmt source.Statement) (string, []any, error) {
	if len(stmt.Named) == 0 {
		return stmt.SQL, stmt.Args, nil
	}
	if len(stmt.Args) > 0 {
		return "", nil, errors.New("postgres: a statement cannot mix positional and named parameters")
	}
	lx := sqllex.NewLexer(sqllex.PostgreSQL)
	var st sqllex.State
	var sb strings.Builder
	var args []any
	index := map[string]int{}

	for li, line := range strings.Split(stmt.SQL, "\n") {
		if li > 0 {
			sb.WriteByte('\n')
		}
		toks, next := lx.LexLine(line, st)
		for _, tk := range toks {
			text := line[tk.Start:tk.End]
			if tk.Kind == sqllex.TokParameter && strings.HasPrefix(text, ":") {
				name := text[1:]
				n, seen := index[name]
				if !seen {
					v, ok := stmt.Named[name]
					if !ok {
						return "", nil, fmt.Errorf("postgres: no value supplied for :%s", name)
					}
					args = append(args, v)
					n = len(args)
					index[name] = n
				}
				sb.WriteString("$" + strconv.Itoa(n))
				continue
			}
			sb.WriteString(text)
		}
		st = next
	}
	return sb.String(), args, nil
}

// statementError lifts a server error into the contract's form, carrying the
// position the editor underlines.
func statementError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return err
	}
	return &source.StatementError{
		Message: source.Message{Level: source.MessageError, Text: pgErr.Message,
			Code: pgErr.Code, Position: int(pgErr.Position)},
		Detail: pgErr.Detail, Hint: pgErr.Hint, Err: err,
	}
}

// endTx ends a read-only wrapper transaction by committing it.
//
// Commit, not rollback. A READ ONLY transaction cannot have written anything,
// so committing is safe, but SET and set_config are transactional: rolling
// back would silently undo a legitimate "SET search_path" on a read-only
// connection, and the next statement would run somewhere else. A set_config
// that switches the session default off does survive the commit, and that is
// harmless, because every following statement gets its own explicit READ ONLY
// transaction regardless of the default. That is the point of layer 2.
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
