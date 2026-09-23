package app

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Explicit transactions, through a query session (FR-5.14).
//
// A transaction lives on one server connection, so it belongs to the session
// a query tab pins and not to the connection, which hands out whichever
// connection is free. A tab with a transaction open is a tab holding locks,
// which is why the window says so until it ends.

// ErrNoTransactions is a connection that will not hold one.
var ErrNoTransactions = errNoTransactions{}

type errNoTransactions struct{}

func (errNoTransactions) Error() string {
	return "this connection does not hold explicit transactions"
}

// transactor is the session's transaction control, where it has any.
//
// A source with no sessions has no session here, and a nil interface asserts
// to nothing, so there is nothing to check for first.
func (qs *QuerySession) transactor() (source.Transactor, bool) {
	t, ok := qs.session.(source.Transactor)
	return t, ok
}

// CanTransact reports whether this session holds explicit transactions.
func (qs *QuerySession) CanTransact() bool {
	_, ok := qs.transactor()
	return ok && qs.src.Capabilities().Query.Transactions
}

// Begin opens a transaction.
func (qs *QuerySession) Begin(ctx context.Context) (err error) {
	defer panics.Recover(&err, "beginning a transaction")
	t, ok := qs.transactor()
	if !ok {
		return ErrNoTransactions
	}
	return t.Begin(ctx)
}

// Commit ends the transaction, keeping what it did.
func (qs *QuerySession) Commit(ctx context.Context) (err error) {
	defer panics.Recover(&err, "committing a transaction")
	t, ok := qs.transactor()
	if !ok {
		return ErrNoTransactions
	}
	return t.Commit(ctx)
}

// Rollback ends the transaction, undoing what it did.
func (qs *QuerySession) Rollback(ctx context.Context) (err error) {
	defer panics.Recover(&err, "rolling back a transaction")
	t, ok := qs.transactor()
	if !ok {
		return ErrNoTransactions
	}
	return t.Rollback(ctx)
}

// Transaction is whether one is open, and whether it can still be
// committed. A connection that holds none is never in one.
func (qs *QuerySession) Transaction() source.TxState {
	t, ok := qs.transactor()
	if !ok {
		return source.TxNone
	}
	return t.Transaction()
}
