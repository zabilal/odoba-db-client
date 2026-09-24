package cockroach

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A transaction the server asks to be run again is the engine working as
// designed, not a fault, and it is told apart from everything else.
func TestATransactionToldToStartAgainIsRecognised(t *testing.T) {
	if !retryable(&pgconn.PgError{Code: "40001", Message: "restart transaction"}) {
		t.Error("a serialisable restart was not recognised")
	}
	// Wrapped, which is how it arrives from the shared planner.
	wrapped := fmt.Errorf("applying: %w", &pgconn.PgError{Code: "40001"})
	if !retryable(wrapped) {
		t.Error("a wrapped restart was not recognised")
	}
	// Nothing else is retried. A unique-key violation run twice writes the
	// same failure twice and wastes the window's time.
	for _, err := range []error{
		nil,
		errors.New("who knows"),
		&pgconn.PgError{Code: "23505"}, // a key already there
		&pgconn.PgError{Code: "25006"}, // a read-only transaction
	} {
		if retryable(err) {
			t.Errorf("%v was taken for a restart", err)
		}
	}
}

// A changeset on a read-only connection is refused before the connection
// is touched at all (NFR-S4).
//
// Planning is rendering, and rendering is what lets somebody read the SQL
// a change would run; it is applying that is refused. The refusal comes
// first, which is what this proves: the source here has no pool, so
// anything that reached the server would panic rather than refuse.
func TestAReadOnlyConnectionAppliesNothing(t *testing.T) {
	s := &crdbSource{cfg: source.ConnectionConfig{Guard: source.Guard{ReadOnly: true}}}
	tbl := model.NewRef(model.KindTable, "shop", "public", "orders")
	cs := source.Changeset{
		Target:   tbl,
		Identity: model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: tbl},
		Changes: []source.RowChange{
			{Kind: source.ChangeInsert, Values: map[string]any{"id": int64(1)}},
		}}
	plan, err := s.Plan(t.Context(), cs)
	if err != nil {
		t.Fatalf("a change could not even be rendered: %v", err)
	}
	if _, err := s.Apply(t.Context(), plan); err == nil {
		t.Fatal("a read-only connection wrote a row")
	}
}

// Rows with no key to tell them apart are not written at all (FR-4.7).
func TestRowsWithNoKeyAreNotWritten(t *testing.T) {
	s := &crdbSource{}
	tbl := model.NewRef(model.KindTable, "shop", "public", "orders")
	_, err := s.Plan(t.Context(), source.Changeset{Target: tbl,
		Identity: model.RowIdentity{Kind: model.IdentityNone},
		Changes: []source.RowChange{
			{Kind: source.ChangeUpdate, Key: []any{int64(1)}, Values: map[string]any{"a": 1}},
		}})
	if err == nil {
		t.Fatal("a row with no address was updated")
	}
}

// A plan names every identifier through QuoteIdentifier and binds every
// value (NFR-S6), and its statements are three parts long because that is
// how this engine names a table.
func TestAPlanBindsEveryValue(t *testing.T) {
	s := &crdbSource{}
	tbl := model.NewRef(model.KindTable, "shop", "public", "orders")
	id := model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: tbl}
	plan, err := s.Plan(t.Context(), source.Changeset{Target: tbl, Identity: id,
		Changes: []source.RowChange{
			{Kind: source.ChangeInsert, Values: map[string]any{"note": "'; DROP TABLE orders --"}},
			{Kind: source.ChangeUpdate, Key: []any{int64(7)}, Values: map[string]any{"note": "x"}},
			{Kind: source.ChangeDelete, Key: []any{int64(8)}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 3 {
		t.Fatalf("the plan holds %d statements", len(plan.Statements))
	}
	if !plan.Atomic {
		t.Error("a changeset here is written in one transaction and the plan says otherwise")
	}
	for _, st := range plan.Statements {
		if got := st.SQL; !contains([]string{got}, got) {
			t.Fatal("unreachable")
		}
		if st.SQL == "" {
			t.Error("a statement was rendered empty")
		}
		if idx := indexOf(st.SQL, "DROP TABLE"); idx >= 0 {
			t.Errorf("a value reached the statement: %s", st.SQL)
		}
		if idx := indexOf(st.SQL, `"shop"."public"."orders"`); idx < 0 {
			t.Errorf("the table is named %s, which is not three parts long", st.SQL)
		}
	}
	// A row with no values at all takes the whole row's defaults, which
	// this engine spells the same way PostgreSQL does.
	plan, err = s.Plan(t.Context(), source.Changeset{Target: tbl, Identity: id,
		Changes: []source.RowChange{{Kind: source.ChangeInsert}}})
	if err != nil {
		t.Fatal(err)
	}
	if idx := indexOf(plan.Statements[0].SQL, "DEFAULT VALUES"); idx < 0 {
		t.Errorf("a row of nothing but defaults reads %s", plan.Statements[0].SQL)
	}
}

// A row taken over the row already there is written as ON CONFLICT
// (ADR-0052).
func TestAnUpsertNamesTheKeyItTakesOver(t *testing.T) {
	got := d.UpsertClause([]string{"id"}, []string{"id", "name"})
	if idx := indexOf(got, `ON CONFLICT ("id") DO UPDATE`); idx < 0 {
		t.Errorf("an upsert reads %s", got)
	}
	if idx := indexOf(got, `"name" = EXCLUDED."name"`); idx < 0 {
		t.Errorf("an upsert does not write the other columns: %s", got)
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
