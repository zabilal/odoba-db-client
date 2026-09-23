package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Explicit transactions (FR-5.14).
//
// A transaction lives on one connection, so this belongs to a session — the
// thing that pins one — and not to the source.
//
// SQLite does not poison a transaction when a statement inside it fails: the
// statement fails and the transaction carries on, so there is no failed
// state to report here and reporting one would be inventing it.

var _ source.Transactor = (*session)(nil)

func (ss *session) Begin(ctx context.Context) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return errClosed
	}
	if ss.tx != nil {
		return fmt.Errorf("sqlite: a transaction is already open")
	}
	// Beginning one changes nothing by itself, and what runs inside it is
	// guarded as it always is.
	//
	// No read-only option is asked for here. A read-only connection opens
	// the file read-only with query_only set (classify.go), so the engine
	// refuses a write inside a transaction as it does outside one; asking
	// again on the transaction would be a second claim about the same thing
	// that nothing could tell apart.
	tx, err := ss.conn.BeginTx(ctx, nil)
	if err != nil {
		return statementError(err)
	}
	ss.tx = tx
	return nil
}

func (ss *session) Commit(ctx context.Context) error {
	return ss.endTx("commit", (*sql.Tx).Commit)
}

func (ss *session) Rollback(ctx context.Context) error {
	return ss.endTx("roll back", (*sql.Tx).Rollback)
}

// endTx finishes the transaction one way or the other.
//
// The transaction is let go whatever the file answered: a commit that failed
// has ended it too, and holding on to it would leave the window saying one
// was open when none is.
func (ss *session) endTx(what string, fn func(*sql.Tx) error) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.tx == nil {
		return fmt.Errorf("sqlite: there is no transaction to %s", what)
	}
	if ss.open != nil {
		// A result still streaming belongs to the transaction that is
		// ending; reading more of it afterwards would read from nothing.
		ss.open.Close()
		ss.open = nil
	}
	tx := ss.tx
	ss.tx = nil
	if err := fn(tx); err != nil {
		return statementError(err)
	}
	return nil
}

// Transaction is whether one is open. SQLite has no state between open and
// ended.
func (ss *session) Transaction() source.TxState {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.tx == nil {
		return source.TxNone
	}
	return source.TxOpen
}

// runner is where a statement runs: inside the open transaction, or on the
// connection itself.
type runner interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// where a statement runs. Called with the session's lock held.
func (ss *session) where() runner {
	if ss.tx != nil {
		return ss.tx
	}
	return ss.conn
}
