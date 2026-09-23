package mysql

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Where a statement runs (FR-5.14). What a transaction does to data is the
// servers' business and is settled against them (transaction_live_test.go);
// this is the part that decides what a statement is sent through.

func TestAStatementRunsWhereTheTransactionIs(t *testing.T) {
	ss := &session{}
	if got := ss.where(); got != runner(ss.conn) {
		t.Errorf("with no transaction open a statement runs on %T", got)
	}
	ss.tx = &sql.Tx{}
	if got := ss.where(); got != runner(ss.tx) {
		t.Errorf("with a transaction open a statement runs on %T", got)
	}
}

// A session that holds no transaction is in none.
func TestASessionWithNoTransactionIsInNone(t *testing.T) {
	if got := (&session{}).Transaction(); got != source.TxNone {
		t.Errorf("a session that never began one is %v", got)
	}
}

// A second transaction is refused here, in this program's own words: the
// servers deal with one differently from one another and neither says
// anything somebody could act on.
func TestASecondTransactionIsRefusedHere(t *testing.T) {
	ss := &session{tx: &sql.Tx{}}
	err := ss.Begin(context.Background())
	if err == nil {
		t.Fatal("a second transaction was opened on top of the first")
	}
	if !strings.Contains(err.Error(), "already open") {
		t.Errorf("it said %v", err)
	}
}

// Ending a transaction that never began is a mistake worth saying, and
// needs no server to say it.
func TestEndingATransactionThatNeverBegan(t *testing.T) {
	ss := &session{}
	ctx := context.Background()
	if err := ss.Commit(ctx); err == nil {
		t.Error("a transaction that never began was committed")
	}
	if err := ss.Rollback(ctx); err == nil {
		t.Error("a transaction that never began was rolled back")
	}
}
