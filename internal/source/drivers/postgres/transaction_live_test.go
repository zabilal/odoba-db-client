//go:build conformance

package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Explicit transactions (FR-5.14).
//
// What a transaction does is the server's business, so none of this can be
// settled against a fake. What is claimed is that a transaction opened here
// is the one every later statement runs in, that ending it does what its
// name says, and that a window asking whether one is open is told the truth
// — including the truth that it has failed.

func session(t *testing.T, readOnly bool) *pgSession {
	t.Helper()
	ss, err := openSource(t, readOnly).openSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	return ss
}

func run(t *testing.T, ss *pgSession, sql string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := ss.Query(ctx, source.Statement{SQL: sql})
	if err != nil {
		return err
	}
	if res.Rows != nil {
		drainRows(t, res.Rows)
	}
	return nil
}

func drainRows(t *testing.T, rs interface{ Close() error }) {
	t.Helper()
	rs.Close()
}

func count(t *testing.T, ss *pgSession, sql string) int64 {
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
	switch v := row[0].(type) {
	case int64:
		return v
	case int32:
		return int64(v)
	}
	t.Fatalf("%s returned %T", sql, row[0])
	return 0
}

// What a transaction did is there while it is open, and gone when it is
// rolled back.
func TestLiveARollbackUndoesWhatTheTransactionDid(t *testing.T) {
	ss := session(t, false)
	ctx := context.Background()
	before := count(t, ss, `SELECT count(*) FROM ikigai_it.writes`)

	if err := ss.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if got := ss.Transaction(); got != source.TxOpen {
		t.Errorf("after Begin the transaction is %v", got)
	}
	if err := run(t, ss, `INSERT INTO ikigai_it.writes (name, n) VALUES ('rolled', 1)`); err != nil {
		t.Fatalf("inserting: %v", err)
	}
	if got := count(t, ss, `SELECT count(*) FROM ikigai_it.writes`); got != before+1 {
		t.Errorf("inside the transaction there are %d rows, want %d", got, before+1)
	}
	if err := ss.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := ss.Transaction(); got != source.TxNone {
		t.Errorf("after Rollback the transaction is %v", got)
	}
	if got := count(t, ss, `SELECT count(*) FROM ikigai_it.writes`); got != before {
		t.Errorf("after rolling back there are %d rows, want the %d there were", got, before)
	}
}

// What a transaction did stays when it is committed, and another connection
// can see it.
func TestLiveACommitKeepsWhatTheTransactionDid(t *testing.T) {
	ss := session(t, false)
	other := session(t, false)
	ctx := context.Background()
	before := count(t, other, `SELECT count(*) FROM ikigai_it.writes`)

	if err := ss.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := run(t, ss, `INSERT INTO ikigai_it.writes (name, n) VALUES ('kept', 1)`); err != nil {
		t.Fatalf("inserting: %v", err)
	}
	// Nobody else can see it yet: that is what a transaction is.
	if got := count(t, other, `SELECT count(*) FROM ikigai_it.writes`); got != before {
		t.Errorf("another connection sees %d rows before the commit, want %d", got, before)
	}
	if err := ss.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := count(t, other, `SELECT count(*) FROM ikigai_it.writes`); got != before+1 {
		t.Errorf("another connection sees %d rows after the commit, want %d", got, before+1)
	}
	t.Cleanup(func() { run(t, ss, `DELETE FROM ikigai_it.writes WHERE name = 'kept'`) })
}

// A statement that fails inside a transaction poisons it: from then on every
// statement answers that it is aborted, and only a rollback ends it. A
// window that said "open" would be offering a commit that cannot happen.
func TestLiveAFailedStatementPoisonsTheTransaction(t *testing.T) {
	ss := session(t, false)
	ctx := context.Background()
	if err := ss.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := run(t, ss, `SELECT nothing_of_the_sort()`); err == nil {
		t.Fatal("a statement that cannot run did")
	}
	if got := ss.Transaction(); got != source.TxFailed {
		t.Errorf("after a failed statement the transaction is %v, want failed", got)
	}
	// And the next statement says so, which is what makes the state worth
	// reporting rather than guessing at.
	if err := run(t, ss, `SELECT 1`); err == nil {
		t.Error("a statement ran inside an aborted transaction")
	} else if !strings.Contains(strings.ToLower(err.Error()), "aborted") {
		t.Errorf("it said %v", err)
	}
	if err := ss.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := ss.Transaction(); got != source.TxNone {
		t.Errorf("after rolling back the transaction is %v", got)
	}
	if err := run(t, ss, `SELECT 1`); err != nil {
		t.Errorf("after rolling back: %v", err)
	}
}

// Ending a transaction that never began is a mistake worth saying.
//
// Beginning one twice is settled without a server (transaction_test.go).
func TestLiveWhatCannotBeEnded(t *testing.T) {
	ss := session(t, false)
	ctx := context.Background()
	if err := ss.Commit(ctx); err == nil {
		t.Error("a transaction that never began was committed")
	}
	if err := ss.Rollback(ctx); err == nil {
		t.Error("a transaction that never began was rolled back")
	}
}

// A read-only connection's transaction is read-only too, and that is worth
// asking for even though the session default already refuses writes: the
// session default can be changed underneath it, and an explicit READ ONLY
// transaction cannot.
func TestLiveAReadOnlyTransactionRefusesAWrite(t *testing.T) {
	ss := session(t, true)
	ctx := context.Background()
	// The session default turned off first, through a function body the
	// lexer cannot see into. After this only the transaction's own access
	// mode stands between a read-only connection and a write, which is why
	// it is asked for rather than inherited.
	if err := run(t, ss, `SELECT ikigai_it.flip_read_only()`); err != nil {
		t.Fatalf("flipping the session default: %v", err)
	}
	if err := ss.Begin(ctx); err != nil {
		t.Fatalf("Begin on a read-only connection: %v", err)
	}
	defer ss.Rollback(ctx)
	if err := run(t, ss, `SELECT count(*) FROM ikigai_it.orders`); err != nil {
		t.Errorf("reading inside a read-only transaction: %v", err)
	}
	// Through a function body too, which the lexer cannot see into.
	err := run(t, ss, `SELECT ikigai_it.write_probe()`)
	if err == nil {
		t.Fatal("a write ran inside a read-only transaction")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "read-only transaction") {
		t.Errorf("it said %v, which is not the transaction's own refusal", err)
	}
}

// A transaction the server ended underneath us is not open, whatever this
// still holds: somebody typing COMMIT in the editor ends one, and a window
// that went on saying "open" would be offering to end nothing.
func TestLiveATransactionEndedByAStatementIsNotOpen(t *testing.T) {
	ss := session(t, false)
	ctx := context.Background()
	if err := ss.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer ss.Rollback(ctx)
	if err := run(t, ss, `COMMIT`); err != nil {
		t.Fatalf("committing by hand: %v", err)
	}
	if got := ss.Transaction(); got != source.TxNone {
		t.Errorf("after a COMMIT of their own the transaction is %v", got)
	}
}

// A connection that holds transactions says so, or the window would never
// offer to open one.
func TestLiveSaysItHoldsTransactions(t *testing.T) {
	if !openSource(t, false).Capabilities().Query.Transactions {
		t.Error("this connection holds transactions and does not say so")
	}
}

// A session closing with a transaction open rolls it back: a pooled
// connection handed back mid-transaction holds its locks until something
// else notices.
func TestLiveClosingASessionEndsItsTransaction(t *testing.T) {
	src := openSource(t, false)
	ctx := context.Background()
	ss, err := src.openSession(ctx)
	if err != nil {
		t.Fatal(err)
	}
	other := session(t, false)
	before := count(t, other, `SELECT count(*) FROM ikigai_it.writes`)

	if err := ss.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := run(t, ss, `INSERT INTO ikigai_it.writes (name, n) VALUES ('abandoned', 1)`); err != nil {
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
	if got := ss.Transaction(); got != source.TxNone {
		t.Errorf("after closing the session the transaction is %v", got)
	}
	if got := count(t, other, `SELECT count(*) FROM ikigai_it.writes`); got != before {
		t.Errorf("after closing there are %d rows, want the %d there were", got, before)
	}
}

// Every statement of a script runs inside the transaction, not each in one
// of its own.
func TestLiveAScriptRunsInsideTheOpenTransaction(t *testing.T) {
	ss := session(t, false)
	ctx := context.Background()
	before := count(t, ss, `SELECT count(*) FROM ikigai_it.writes`)
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
	if got := count(t, ss, `SELECT count(*) FROM ikigai_it.writes`); got != before+2 {
		t.Errorf("inside the transaction there are %d rows, want %d", got, before+2)
	}
	if err := ss.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := count(t, ss, `SELECT count(*) FROM ikigai_it.writes`); got != before {
		t.Errorf("after rolling back a script there are %d rows, want %d", got, before)
	}
}

// A session that is closed says so rather than beginning something on a
// connection it has given back.
func TestLiveAClosedSessionBeginsNothing(t *testing.T) {
	src := openSource(t, false)
	ss, err := src.openSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ss.Close()
	if err := ss.Begin(context.Background()); !errors.Is(err, errClosed) {
		t.Errorf("Begin on a closed session: %v", err)
	}
}
