package cassandra

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Planning what a grid's changes would write (FR-4.4, ADR-0143).

var writesRef = model.NewRef(model.KindTable, "ikigai_it", "writes")

func byKey() model.RowIdentity {
	return model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: writesRef}
}

func planFor(t *testing.T, changes ...source.RowChange) *source.WritePlan {
	t.Helper()
	s := &cassandraSource{}
	plan, err := s.Plan(context.Background(), source.Changeset{
		Target: writesRef, Identity: byKey(), Changes: changes})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	return plan
}

// CQL has no writes, only upserts, so every statement carries a condition:
// a change is for a row that is there, and a new row is for a key that is
// not.
func TestEveryWriteCarriesItsCondition(t *testing.T) {
	plan := planFor(t,
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{1}, Values: map[string]any{"name": "uno"}},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{2}},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"id": 3, "name": "three"}})
	want := []string{
		`UPDATE "ikigai_it"."writes" SET "name" = ? WHERE "id" = ? IF EXISTS`,
		`DELETE FROM "ikigai_it"."writes" WHERE "id" = ? IF EXISTS`,
		`INSERT INTO "ikigai_it"."writes" ("id", "name") VALUES (?, ?) IF NOT EXISTS`,
	}
	for i, w := range want {
		if got := plan.Statements[i].SQL; got != w {
			t.Errorf("statement %d writes\n%s\nwant\n%s", i, got, w)
		}
	}
	// And each says which of the two ways its condition could fail.
	for i, inserting := range []bool{false, false, true} {
		cond, ok := plan.Statements[i].Op.(conditional)
		if !ok || cond.inserting != inserting {
			t.Errorf("statement %d says %#v, want inserting=%v", i, plan.Statements[i].Op, inserting)
		}
	}
}

// Nothing here is undone, so a plan does not say it will be (FR-4.5).
func TestAPlanIsNotAppliedAllAtOnce(t *testing.T) {
	if plan := planFor(t, source.RowChange{Kind: source.ChangeDelete, Key: []any{1}}); plan.Atomic {
		t.Error("the plan says it is applied all at once")
	}
	if err := noCommit(); err != nil {
		t.Errorf("committing what needs no commit: %v", err)
	}
	if noRollback() == nil {
		t.Error("undoing said it had undone something, and there is nothing here to undo it in")
	}
}

// A row here is its key, so a row of nothing is refused: there would be no
// key to write it under.
func TestARowOfNothingIsRefused(t *testing.T) {
	s := &cassandraSource{}
	_, err := s.Plan(context.Background(), source.Changeset{Target: writesRef, Identity: byKey(),
		Changes: []source.RowChange{{Kind: source.ChangeInsert}}})
	if err == nil || !strings.Contains(err.Error(), "columns of its key") {
		t.Errorf("a row of nothing was planned: %v", err)
	}
}

// Rows that cannot be told apart are not written: there would be nothing to
// say which row a change was for (FR-4.7).
func TestRowsWithNoKeyAreNotWritten(t *testing.T) {
	s := &cassandraSource{}
	_, err := s.Plan(context.Background(), source.Changeset{Target: writesRef,
		Identity: model.RowIdentity{Kind: model.IdentityNone},
		Changes:  []source.RowChange{{Kind: source.ChangeDelete, Key: []any{1}}}})
	if err == nil {
		t.Error("a change was planned against rows that cannot be told apart")
	}
}
