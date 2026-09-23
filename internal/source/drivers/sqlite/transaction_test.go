package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Explicit transactions (FR-5.14). A real file, because what a transaction
// does is SQLite's business and no fake can stand in for it.

func txSession(t *testing.T, path string, guard source.Guard) *session {
	t.Helper()
	src := open(t, path, guard)
	t.Cleanup(func() { src.Close() })
	ss, err := src.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	return ss.(*session)
}

func exec(t *testing.T, ss *session, sql string) error {
	t.Helper()
	res, err := ss.Query(context.Background(), source.Statement{SQL: sql})
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
	// Bounded, because a transaction left open somewhere else locks the
	// file: a test that hung would say nothing about why.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

// What a transaction did is there while it is open, and gone when it is
// rolled back.
func TestARollbackUndoesWhatTheTransactionDid(t *testing.T) {
	ss := txSession(t, fixture(t), source.Guard{})
	ctx := context.Background()
	before := rowCount(t, ss, `SELECT count(*) FROM writes`)

	if err := ss.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if got := ss.Transaction(); got != source.TxOpen {
		t.Errorf("after Begin the transaction is %v", got)
	}
	if err := exec(t, ss, `INSERT INTO writes (name, n) VALUES ('rolled', 1)`); err != nil {
		t.Fatalf("inserting: %v", err)
	}
	if got := rowCount(t, ss, `SELECT count(*) FROM writes`); got != before+1 {
		t.Errorf("inside the transaction there are %d rows, want %d", got, before+1)
	}
	if err := ss.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := ss.Transaction(); got != source.TxNone {
		t.Errorf("after Rollback the transaction is %v", got)
	}
	if got := rowCount(t, ss, `SELECT count(*) FROM writes`); got != before {
		t.Errorf("after rolling back there are %d rows, want the %d there were", got, before)
	}
}

// What a transaction did stays when it is committed.
func TestACommitKeepsWhatTheTransactionDid(t *testing.T) {
	ss := txSession(t, fixture(t), source.Guard{})
	ctx := context.Background()
	before := rowCount(t, ss, `SELECT count(*) FROM writes`)

	if err := ss.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := exec(t, ss, `INSERT INTO writes (name, n) VALUES ('kept', 1)`); err != nil {
		t.Fatalf("inserting: %v", err)
	}
	if err := ss.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := rowCount(t, ss, `SELECT count(*) FROM writes`); got != before+1 {
		t.Errorf("after committing there are %d rows, want %d", got, before+1)
	}
}

// A statement that fails inside a transaction does not end it here: SQLite
// carries on, and saying the transaction had failed would be inventing a
// state this engine does not have.
func TestAFailedStatementDoesNotEndTheTransaction(t *testing.T) {
	ss := txSession(t, fixture(t), source.Guard{})
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
}

// Ending a transaction that never began is a mistake worth saying, and
// beginning one twice is another.
func TestWhatCannotBeBegunOrEnded(t *testing.T) {
	ss := txSession(t, fixture(t), source.Guard{})
	ctx := context.Background()
	if err := ss.Commit(ctx); err == nil || !strings.Contains(err.Error(), "no transaction") {
		t.Errorf("committing nothing said %v", err)
	}
	if err := ss.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "no transaction") {
		t.Errorf("rolling back nothing said %v", err)
	}
	if err := ss.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer ss.Rollback(ctx)
	// SQLite refuses a transaction inside a transaction too, so what is
	// claimed here is that this refuses it first and says which it is.
	err := ss.Begin(ctx)
	if err == nil {
		t.Fatal("a second transaction was opened on top of the first")
	}
	if !strings.Contains(err.Error(), "already open") {
		t.Errorf("it said %v, which is the engine's answer rather than this one", err)
	}
}

// A statement runs inside the transaction while one is open, and on the
// connection when none is.
func TestAStatementRunsWhereTheTransactionIs(t *testing.T) {
	ss := txSession(t, fixture(t), source.Guard{})
	ctx := context.Background()
	if got := ss.where(); got != runner(ss.conn) {
		t.Errorf("with no transaction open a statement runs on %T", got)
	}
	if err := ss.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer ss.Rollback(ctx)
	if got := ss.where(); got != runner(ss.tx) {
		t.Errorf("with a transaction open a statement runs on %T", got)
	}
}

// A read-only connection's transaction is read-only too.
func TestAReadOnlyTransactionRefusesAWrite(t *testing.T) {
	ss := txSession(t, fixture(t), source.Guard{ReadOnly: true})
	ctx := context.Background()
	if err := ss.Begin(ctx); err != nil {
		t.Fatalf("Begin on a read-only connection: %v", err)
	}
	defer ss.Rollback(ctx)
	if err := exec(t, ss, `SELECT count(*) FROM people`); err != nil {
		t.Errorf("reading inside a read-only transaction: %v", err)
	}
	if err := exec(t, ss, `INSERT INTO writes (name) VALUES ('no')`); err == nil {
		t.Error("a write ran inside a read-only transaction")
	}
}

// A session closing with a transaction open rolls it back: a file left
// locked is a file nothing else can write to.
func TestClosingASessionEndsItsTransaction(t *testing.T) {
	path := fixture(t)
	src := open(t, path, source.Guard{})
	defer src.Close()
	first, err := src.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ss := first.(*session)
	before := rowCount(t, ss, `SELECT count(*) FROM writes`)

	if err := ss.Begin(context.Background()); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := exec(t, ss, `INSERT INTO writes (name, n) VALUES ('abandoned', 1)`); err != nil {
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
	// Asked of the session first, and stopping if it is wrong: a transaction
	// still open holds the file's write lock, and the reading below would
	// wait on it rather than answer.
	if got := ss.Transaction(); got != source.TxNone {
		t.Fatalf("after closing the transaction is %v", got)
	}
	other := txSession(t, path, source.Guard{})
	if got := rowCount(t, other, `SELECT count(*) FROM writes`); got != before {
		t.Errorf("after closing there are %d rows, want the %d there were", got, before)
	}
}

// A closed session begins nothing on a connection it has given back, and
// says so itself rather than leaving the driver to notice.
func TestAClosedSessionBeginsNothing(t *testing.T) {
	ss := txSession(t, fixture(t), source.Guard{})
	ss.Close()
	if err := ss.Begin(context.Background()); !errors.Is(err, errClosed) {
		t.Errorf("a closed session said %v", err)
	}
}

// A connection that holds transactions says so, or the window would never
// offer to open one.
func TestSQLiteSaysItHoldsTransactions(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	defer s.Close()
	if !s.Capabilities().Query.Transactions {
		t.Error("SQLite holds transactions and does not say so")
	}
}

// Every statement of a script runs inside the transaction, not each in one
// of its own.
func TestAScriptRunsInsideTheOpenTransaction(t *testing.T) {
	ss := txSession(t, fixture(t), source.Guard{})
	ctx := context.Background()
	before := rowCount(t, ss, `SELECT count(*) FROM writes`)
	if err := ss.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	ch, err := ss.QueryMulti(ctx, `INSERT INTO writes (name, n) VALUES ('a', 1);
		INSERT INTO writes (name, n) VALUES ('b', 2);`, source.ScriptOptions{})
	if err != nil {
		t.Fatalf("QueryMulti: %v", err)
	}
	for r := range ch {
		if r.Err != nil {
			t.Fatalf("statement %d: %v", r.Index, r.Err)
		}
	}
	if got := rowCount(t, ss, `SELECT count(*) FROM writes`); got != before+2 {
		t.Errorf("inside the transaction there are %d rows, want %d", got, before+2)
	}
	if err := ss.Rollback(ctx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := rowCount(t, ss, `SELECT count(*) FROM writes`); got != before {
		t.Errorf("after rolling back a script there are %d rows, want %d", got, before)
	}
}

// What a state is called, for a window to show.
func TestWhatAStateIsCalled(t *testing.T) {
	for state, want := range map[source.TxState]string{
		source.TxNone: "none", source.TxOpen: "open", source.TxFailed: "failed",
	} {
		if got := state.String(); got != want {
			t.Errorf("%d is called %q, want %q", state, got, want)
		}
	}
	if got := source.TxState(99).String(); got != "none" {
		t.Errorf("a state nobody set is called %q", got)
	}
}
