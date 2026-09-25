package firebird

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a changeset becomes, decided without a server. A row of nothing but
// defaults is the case that depends on the engine's version, and it is the
// one where writing SQL an older server cannot parse would surface as a
// syntax error nobody could act on.

func changeset(changes ...source.RowChange) source.Changeset {
	target := model.NewRef(model.KindTable, "ikigai.fdb", "WRITES")
	return source.Changeset{
		Target:    target,
		Identity:  model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"ID"}, Target: target},
		Changes:   changes,
		Confirmed: true,
	}
}

func TestARowOfNothingButDefaults(t *testing.T) {
	s := &firebirdSource{version: "5.0.4"}
	plan, err := s.Plan(context.Background(), changeset(source.RowChange{Kind: source.ChangeInsert}))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Statements) != 1 {
		t.Fatalf("it planned %d statements", len(plan.Statements))
	}
	if got := plan.Statements[0].SQL; got != `INSERT INTO "WRITES" DEFAULT VALUES` {
		t.Errorf("it would run %s", got)
	}
}

// On a server without the clause, the refusal happens here and says what to
// do, rather than at the server and in its words about syntax.
func TestOnAnOlderServerARowOfDefaultsIsRefusedHere(t *testing.T) {
	s := &firebirdSource{version: "3.0.10"}
	_, err := s.Plan(context.Background(), changeset(source.RowChange{Kind: source.ChangeInsert}))
	if err == nil {
		t.Fatal("it planned one anyway")
	}
	for _, want := range []string{"3.0.10", "Firebird 4", "at least one column"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("it says %q, which does not mention %q", err, want)
		}
	}
	// And a row with a value in it is planned as usual, on the same server:
	// what is refused is the row of nothing, not the table.
	plan, err := s.Plan(context.Background(), changeset(source.RowChange{
		Kind: source.ChangeInsert, Values: map[string]any{"NAME": "one"}}))
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Statements[0].SQL; got != `INSERT INTO "WRITES" ("NAME") VALUES (?)` {
		t.Errorf("it would run %s", got)
	}
}

// Every value is bound and no identifier arrives any other way (NFR-S6).
func TestAChangesetBindsItsValues(t *testing.T) {
	s := &firebirdSource{version: "5.0.4"}
	plan, err := s.Plan(context.Background(), changeset(
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"NAME": "'; DROP TABLE WRITES --"}},
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(1)},
			Values: map[string]any{"NAME": "two"}},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(2)}},
	))
	if err != nil {
		t.Fatal(err)
	}
	wants := []string{
		`INSERT INTO "WRITES" ("NAME") VALUES (?)`,
		`UPDATE "WRITES" SET "NAME" = ? WHERE "ID" = ?`,
		`DELETE FROM "WRITES" WHERE "ID" = ?`,
	}
	for i, st := range plan.Statements {
		if i >= len(wants) {
			t.Fatalf("it planned %d statements", len(plan.Statements))
		}
		if st.SQL != wants[i] {
			t.Errorf("it would run %s, want %s", st.SQL, wants[i])
		}
		if strings.Contains(st.SQL, "DROP") {
			t.Errorf("a value reached the statement: %s", st.SQL)
		}
	}
}
