package app

import (
	"context"
	"errors"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Explicit transactions, through a query session (FR-5.14).

// A connection that holds none says so, and answers every call the same way
// rather than pretending.
func TestASessionThatHoldsNoTransactions(t *testing.T) {
	qs := &QuerySession{}
	if qs.CanTransact() {
		t.Error("a session with no connection at all holds transactions")
	}
	if got := qs.Transaction(); got != source.TxNone {
		t.Errorf("it is in a transaction: %v", got)
	}
	ctx := context.Background()
	for name, fn := range map[string]func(context.Context) error{
		"Begin": qs.Begin, "Commit": qs.Commit, "Rollback": qs.Rollback,
	} {
		if err := fn(ctx); !errors.Is(err, ErrNoTransactions) {
			t.Errorf("%s said %v, want it refused", name, err)
		}
	}
	if got := ErrNoTransactions.Error(); got == "" {
		t.Error("the refusal says nothing")
	}
}
