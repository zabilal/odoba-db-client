package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Explicit transactions (FR-5.14).
//
// A transaction lives on one server connection, so this belongs to a
// session — the thing that pins one — and not to the source, which hands out
// whichever connection is free.
//
// Whether one is open is asked of the connection rather than kept in a flag
// here. PostgreSQL reports it in every message it sends, and it is the only
// answer that stays true when a statement inside the transaction fails: from
// then on every statement answers "current transaction is aborted" and only
// a rollback ends it. Somebody looking at a window that said "open" would be
// told a transaction they could still commit, which would not be true.

var _ source.Transactor = (*pgSession)(nil)

// Transaction status as PostgreSQL reports it in its ready-for-query
// message: idle, in a transaction, or in one that has failed.
const (
	txIdle   = 'I'
	txInside = 'T'
	txFailed = 'E'
)

func (ss *pgSession) Begin(ctx context.Context) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.closed {
		return errClosed
	}
	if ss.tx != nil {
		return fmt.Errorf("postgres: a transaction is already open")
	}
	// Beginning one changes nothing by itself. What runs inside it is
	// guarded as it always is, and on a read-only connection the
	// transaction itself refuses writes the lexer could not see.
	opts := pgx.TxOptions{}
	if ss.src.cfg.Guard.ReadOnly {
		opts.AccessMode = pgx.ReadOnly
	}
	tx, err := ss.conn.BeginTx(ctx, opts)
	if err != nil {
		return statementError(err)
	}
	ss.tx = tx
	return nil
}

func (ss *pgSession) Commit(ctx context.Context) error {
	return ss.end(ctx, "commit", func(tx pgx.Tx) error { return tx.Commit(ctx) })
}

func (ss *pgSession) Rollback(ctx context.Context) error {
	return ss.end(ctx, "roll back", func(tx pgx.Tx) error { return tx.Rollback(ctx) })
}

// end finishes the transaction one way or the other.
//
// The transaction is let go whatever the server answered: a commit that
// failed has ended it too, and holding on to it would leave the window
// saying one was open when none is.
func (ss *pgSession) end(ctx context.Context, what string, fn func(pgx.Tx) error) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.tx == nil {
		return fmt.Errorf("postgres: there is no transaction to %s", what)
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

// where a statement runs: inside the explicit transaction, or on the
// connection itself. Called with the session's lock held.
func (ss *pgSession) where() querier {
	if ss.tx != nil {
		return ss.tx
	}
	return ss.conn
}

// Transaction is what the connection says, not what this remembers.
func (ss *pgSession) Transaction() source.TxState {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.tx == nil {
		return source.TxNone
	}
	switch ss.conn.Conn().PgConn().TxStatus() {
	case txFailed:
		return source.TxFailed
	case txIdle:
		// The server has ended it underneath us — an idle_in_transaction
		// timeout, or a statement of somebody's own that committed.
		return source.TxNone
	case txInside:
		return source.TxOpen
	}
	return source.TxOpen
}
