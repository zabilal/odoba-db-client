package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Where a statement runs (FR-5.14). What a transaction does to data is the
// server's business and is settled against one (transaction_live_test.go);
// this is the part that decides what a statement is sent through.

// nowhere is a transaction that answers nothing. Only its identity matters
// here: what is claimed is which of two things a statement goes through.
type nowhere struct{ pgx.Tx }

func TestAStatementRunsWhereTheTransactionIs(t *testing.T) {
	ss := &pgSession{}
	if got := ss.where(); got != querier(ss.conn) {
		t.Errorf("with no transaction open a statement runs on %T", got)
	}
	ss.tx = nowhere{}
	if got := ss.where(); got != querier(ss.tx) {
		t.Errorf("with a transaction open a statement runs on %T", got)
	}
}

// A session that holds no transaction is in none, whatever else is true of
// it.
func TestASessionWithNoTransactionIsInNone(t *testing.T) {
	ss := &pgSession{}
	if got := ss.Transaction(); got != source.TxNone {
		t.Errorf("a session that never began one is %v", got)
	}
}

// A second transaction is refused here, in this program's own words: the
// server refuses one too, and which of the two answered is the difference
// between a message somebody can act on and one they cannot.
func TestASecondTransactionIsRefusedHere(t *testing.T) {
	ss := &pgSession{tx: nowhere{}}
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
	ss := &pgSession{}
	ctx := context.Background()
	if err := ss.Commit(ctx); err == nil {
		t.Error("a transaction that never began was committed")
	}
	if err := ss.Rollback(ctx); err == nil {
		t.Error("a transaction that never began was rolled back")
	}
}
