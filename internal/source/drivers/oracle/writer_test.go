package oracle

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/guardcheck"
)

// Planning what a grid's changes would write (FR-4.4).

func planFor(t *testing.T, id model.RowIdentity, changes ...source.RowChange) *source.WritePlan {
	t.Helper()
	s := &oracleSource{}
	plan, err := s.Plan(context.Background(), source.Changeset{Target: tbl, Identity: id, Changes: changes})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	return plan
}

func byKey() model.RowIdentity {
	return model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"ID"}, Target: tbl}
}

// A change is written by the key the rows are addressed by.
func TestAChangeIsWrittenByTheKey(t *testing.T) {
	plan := planFor(t, byKey(),
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(1)}, Values: map[string]any{"NAME": "uno"}},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(2)}},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"ID": int64(3), "NAME": "three"}})
	want := []string{
		`UPDATE "SHOP"."PEOPLE" SET "NAME" = :1 WHERE "ID" = :2`,
		`DELETE FROM "SHOP"."PEOPLE" WHERE "ID" = :1`,
		`INSERT INTO "SHOP"."PEOPLE" ("ID", "NAME") VALUES (:1, :2)`,
	}
	for i, w := range want {
		if got := plan.Statements[i].SQL; got != w {
			t.Errorf("statement %d writes\n%s\nwant\n%s", i, got, w)
		}
	}
	// Oracle has a transaction to write a changeset in, so the plan is
	// applied whole or not at all.
	if !plan.Atomic {
		t.Error("the plan says it is not applied all at once")
	}
}

// A row with no key of its own is written by its address (ADR-0144).
func TestARowWithNoKeyIsWrittenByItsAddress(t *testing.T) {
	id := model.RowIdentity{Kind: model.IdentityRowID, Columns: []string{"ROWID"}, Target: tbl}
	plan := planFor(t, id, source.RowChange{Kind: source.ChangeDelete, Key: []any{"AAAR5FAAYAAAAANAAA"}})
	if got, want := plan.Statements[0].SQL, `DELETE FROM "SHOP"."PEOPLE" WHERE "ROWID" = :1`; got != want {
		t.Errorf("it writes\n%s\nwant\n%s", got, want)
	}
}

// A row of nothing but defaults is refused: Oracle has no form for one.
// A row of nothing but defaults is written by naming one column and
// asking for its default, every column not named taking its own anyway.
// Oracle has no DEFAULT VALUES clause, so a column has to be named, and
// the one a row is certain to have is its key.
func TestARowOfNothingTakesEveryDefault(t *testing.T) {
	s := &oracleSource{}
	plan, err := s.Plan(context.Background(), source.Changeset{Target: tbl, Identity: byKey(),
		Changes: []source.RowChange{{Kind: source.ChangeInsert}}})
	if err != nil {
		t.Fatalf("a row of nothing but defaults: %v", err)
	}
	if got := plan.Statements[0].SQL; !strings.Contains(got, `("ID") VALUES (DEFAULT)`) {
		t.Errorf("it reads %s", got)
	}
}

// A table addressed by where its rows are has no such column: its address
// is not something in the row, so there is nothing to name and a row of
// nothing at all is refused.
func TestARowOfNothingIsRefusedWithoutAKey(t *testing.T) {
	s := &oracleSource{}
	_, err := s.Plan(context.Background(), source.Changeset{Target: tbl,
		Identity: model.RowIdentity{Kind: model.IdentityRowID, Columns: []string{rowIDName}, Target: tbl},
		Changes:  []source.RowChange{{Kind: source.ChangeInsert}}})
	if err == nil || !strings.Contains(err.Error(), "at least one column") {
		t.Errorf("a row of nothing was planned: %v", err)
	}
}

// Rows that cannot be told apart are not written (FR-4.7).
func TestRowsWithNoKeyAreNotWritten(t *testing.T) {
	s := &oracleSource{}
	_, err := s.Plan(context.Background(), source.Changeset{Target: tbl,
		Identity: model.RowIdentity{Kind: model.IdentityNone},
		Changes:  []source.RowChange{{Kind: source.ChangeDelete, Key: []any{int64(1)}}}})
	if err == nil {
		t.Error("a change was planned against rows that cannot be told apart")
	}
}

// Read-only is enforced in the data layer, not in the window (NFR-S4).
// Every mutating operation this driver has is held to its guard here, and
// the table is held to the interfaces themselves, so a new write path
// cannot arrive without one.
func TestEveryWritePathIsGuarded(t *testing.T) {
	ctx := context.Background()
	ro := &oracleSource{cfg: source.ConnectionConfig{Guard: source.Guard{ReadOnly: true}}}
	prod := &oracleSource{cfg: source.ConnectionConfig{Guard: source.Guard{Environment: source.EnvProduction}}}

	guardcheck.Check(t, ro, prod, map[string]func(*oracleSource, bool) error{
		"Apply": func(s *oracleSource, c bool) error {
			_, err := s.Apply(ctx, &source.WritePlan{
				Target:     model.ObjectRef{Kind: model.KindTable, Path: []string{"SHOP", "PEOPLE"}},
				Statements: []source.Statement{{SQL: `DELETE FROM "SHOP"."PEOPLE"`, Confirmed: c}},
			})
			return err
		},
	})
}
