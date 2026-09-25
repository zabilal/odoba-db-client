package firebird

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
// Firebird does not poison a transaction when a statement inside it fails:
// the statement fails and the transaction carries on, so there is no failed
// state to report here and reporting one would be inventing it.

var _ source.Transactor = (*session)(nil)

func (ss *session) Begin(ctx context.Context) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return errClosed
	}
	if ss.tx != nil {
		return fmt.Errorf("firebird: a transaction is already open")
	}
	// Read-only is asked for on the transaction as well as held by the guard.
	// Firebird has a read-only transaction of its own — the only place the
	// engine can be told to refuse a write — so a connection nobody may write
	// to says so to the server too, and the guard is then the first of two
	// defences rather than the only one (NFR-S4).
	opts := &sql.TxOptions{ReadOnly: ss.src.cfg.Guard.ReadOnly}
	tx, err := ss.conn.BeginTx(ctx, opts)
	if err != nil {
		return statementError(err, ctx, "")
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
// The transaction is let go whatever the server answered: a commit that
// failed has ended it too, and holding on to it would leave the window saying
// one was open when none is.
func (ss *session) endTx(what string, fn func(*sql.Tx) error) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.tx == nil {
		return fmt.Errorf("firebird: there is no transaction to %s", what)
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
		return statementError(err, nil, "")
	}
	return nil
}

// Transaction is whether one is open. Firebird has no state between open and
// ended that this can see.
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
