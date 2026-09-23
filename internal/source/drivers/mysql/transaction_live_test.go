//go:build conformance

package mysql

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Explicit transactions (FR-5.14), on both servers.
//
// What a transaction does is the server's business, so none of this can be
// settled against a fake. What is claimed is that a transaction opened here
// is the one every later statement runs in, and that ending it does what its
// name says.

func txSession(t *testing.T, srv server, guard source.Guard) *session {
	t.Helper()
	ss, err := open(t, srv, guard).Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	return ss.(*session)
}

func exec(t *testing.T, ss *session, sql string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := ss.Query(ctx, source.Statement{SQL: sql})
	if err != nil {
		return err
	}
	if res.Rows != nil {
		res.Rows.Close()
	}
	return nil
}

func rowCount(t *testing.T, ss *session, sql string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := ss.Query(ctx, source.Statement{SQL: sql})
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	defer res.Rows.Close()
	row, err := res.Rows.Next(ctx)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	n, _ := row[0].(int64)
	return n
}

// What a transaction did is there while it is open, gone when it is rolled
// back, and invisible to anybody else until it is committed.
func TestLiveATransactionIsItsOwnUntilItIsCommitted(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		ss := txSession(t, srv, source.Guard{})
		other := txSession(t, srv, source.Guard{})
		ctx := context.Background()
		before := rowCount(t, other, `SELECT count(*) FROM ikigai_it.writes`)

		if err := ss.Begin(ctx); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if got := ss.Transaction(); got != source.TxOpen {
			t.Errorf("after Begin the transaction is %v", got)
		}
		if err := exec(t, ss, `INSERT INTO ikigai_it.writes (name, n) VALUES ('rolled', 1)`); err != nil {
			t.Fatalf("inserting: %v", err)
		}
		if got := rowCount(t, ss, `SELECT count(*) FROM ikigai_it.writes`); got != before+1 {
			t.Errorf("inside the transaction there are %d rows, want %d", got, before+1)
		}
		if got := rowCount(t, other, `SELECT count(*) FROM ikigai_it.writes`); got != before {
			t.Errorf("another connection sees %d rows, want the %d there were", got, before)
		}
		if err := ss.Rollback(ctx); err != nil {
			t.Fatalf("Rollback: %v", err)
		}
		if got := ss.Transaction(); got != source.TxNone {
			t.Errorf("after Rollback the transaction is %v", got)
		}
		if got := rowCount(t, ss, `SELECT count(*) FROM ikigai_it.writes`); got != before {
			t.Errorf("after rolling back there are %d rows, want %d", got, before)
		}

		// And a commit keeps it, where another connection can see it.
		if err := ss.Begin(ctx); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if err := exec(t, ss, `INSERT INTO ikigai_it.writes (name, n) VALUES ('kept', 1)`); err != nil {
			t.Fatalf("inserting: %v", err)
		}
		if err := ss.Commit(ctx); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		if got := rowCount(t, other, `SELECT count(*) FROM ikigai_it.writes`); got != before+1 {
			t.Errorf("another connection sees %d rows after the commit, want %d", got, before+1)
		}
	})
}

// A statement that fails inside a transaction does not end it on these
// servers: the statement fails and the transaction carries on, so saying it
// had failed would be inventing a state neither engine has.
func TestLiveAFailedStatementDoesNotEndTheTransaction(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		ss := txSession(t, srv, source.Guard{})
		ctx := context.Background()
		if err := ss.Begin(ctx); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		defer ss.Rollback(ctx)
		if err := exec(t, ss, `SELECT nothing_of_the_sort()`); err == nil {
			t.Fatal("a statement that cannot run did")
		}
		if got := ss.Transaction(); got != source.TxOpen {
			t.Errorf("after a failed statement the transaction is %v, want it still open", got)
		}
		if err := exec(t, ss, `SELECT 1`); err != nil {
			t.Errorf("the next statement: %v", err)
		}
	})
}

// Ending a transaction that never began is a mistake worth saying.
//
// Beginning one twice is settled without a server (transaction_test.go): on
// a connection the first transaction is holding, asking for a second is
// something these drivers wait on rather than refuse, so what is claimed is
// that it never gets that far.
func TestLiveWhatCannotBeBegunOrEnded(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		ss := txSession(t, srv, source.Guard{})
		ctx := context.Background()
		if err := ss.Commit(ctx); err == nil || !strings.Contains(err.Error(), "no transaction") {
			t.Errorf("committing nothing said %v", err)
		}
		if err := ss.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "no transaction") {
			t.Errorf("rolling back nothing said %v", err)
		}
	})
}

// A read-only connection refuses a write inside a transaction as it does
// outside one: the connection is opened with the server's own read-only
// variable set, which every transaction on it inherits.
func TestLiveAReadOnlyTransactionRefusesAWrite(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		ss := txSession(t, srv, source.Guard{ReadOnly: true})
		ctx := context.Background()
		if err := ss.Begin(ctx); err != nil {
			t.Fatalf("Begin on a read-only connection: %v", err)
		}
		defer ss.Rollback(ctx)
		if err := exec(t, ss, `SELECT count(*) FROM ikigai_it.people`); err != nil {
			t.Errorf("reading inside a read-only transaction: %v", err)
		}
		if err := exec(t, ss, `UPDATE ikigai_it.writes SET n = 1`); err == nil {
			t.Error("a write ran inside a read-only transaction")
		}
	})
}

// A session closing with a transaction open rolls it back: a pooled
// connection handed back mid-transaction holds its locks.
func TestLiveClosingASessionEndsItsTransaction(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		src := open(t, srv, source.Guard{})
		ctx := context.Background()
		first, err := src.Session(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ss := first.(*session)
		other := txSession(t, srv, source.Guard{})
		before := rowCount(t, other, `SELECT count(*) FROM ikigai_it.writes`)

		if err := ss.Begin(ctx); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if err := exec(t, ss, `INSERT INTO ikigai_it.writes (name, n) VALUES ('abandoned', 1)`); err != nil {
			t.Fatalf("inserting: %v", err)
		}
		// Closing must not wait on the transaction it is responsible for
		// ending: a close that never returned would be a window that never
		// closed a tab.
		done := make(chan error, 1)
		go func() { done <- ss.Close() }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Close: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("closing waited on the transaction it should have rolled back")
		}
		if got := rowCount(t, other, `SELECT count(*) FROM ikigai_it.writes`); got != before {
			t.Errorf("after closing there are %d rows, want the %d there were", got, before)
		}
	})
}

// Every statement of a script runs inside the transaction.
func TestLiveAScriptRunsInsideTheOpenTransaction(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		ss := txSession(t, srv, source.Guard{})
		ctx := context.Background()
		before := rowCount(t, ss, `SELECT count(*) FROM ikigai_it.writes`)
		if err := ss.Begin(ctx); err != nil {
			t.Fatalf("Begin: %v", err)
		}
		ch, err := ss.QueryMulti(ctx, `INSERT INTO ikigai_it.writes (name, n) VALUES ('a', 1);
			INSERT INTO ikigai_it.writes (name, n) VALUES ('b', 2);`, source.ScriptOptions{})
		if err != nil {
			t.Fatalf("QueryMulti: %v", err)
		}
		for r := range ch {
			if r.Err != nil {
				t.Fatalf("statement %d: %v", r.Index, r.Err)
			}
		}
		if got := rowCount(t, ss, `SELECT count(*) FROM ikigai_it.writes`); got != before+2 {
			t.Errorf("inside the transaction there are %d rows, want %d", got, before+2)
		}
		if err := ss.Rollback(ctx); err != nil {
			t.Fatalf("Rollback: %v", err)
		}
		if got := rowCount(t, ss, `SELECT count(*) FROM ikigai_it.writes`); got != before {
			t.Errorf("after rolling back a script there are %d rows, want %d", got, before)
		}
	})
}

// A connection that holds transactions says so, or the window would never
// offer to open one.
func TestLiveSaysItHoldsTransactions(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		if !open(t, srv, source.Guard{}).Capabilities().Query.Transactions {
			t.Error("this connection holds transactions and does not say so")
		}
	})
}
