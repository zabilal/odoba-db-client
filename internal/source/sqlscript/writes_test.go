package sqlscript

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// pgLike is a dialect with PostgreSQL's quoting and placeholders.
type pgLike struct{}

func (pgLike) QuoteIdentifier(n string) string             { return `"` + strings.ReplaceAll(n, `"`, `""`) + `"` }
func (pgLike) QualifyRef(r model.ObjectRef) string         { return `"s"."` + r.Name() + `"` }
func (pgLike) Placeholder(i int) string                    { return "$" + strconv.Itoa(i) }
func (pgLike) Classify(string) source.Access               { return source.AccessWrite }
func (pgLike) SplitScript(string) []source.ScriptStatement { return nil }
func (pgLike) BuildBrowse(model.ObjectRef, source.BrowseOptions) (source.Statement, error) {
	return source.Statement{}, nil
}
func (d pgLike) UpsertClause(keys, cols []string) string { return OnConflict(d, keys, cols) }

var (
	peopleRef = model.NewRef(model.KindTable, "db", "s", "people")
	byID      = model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: peopleRef}
)

func plan(t *testing.T, guard source.Guard, changes ...source.RowChange) *source.WritePlan {
	t.Helper()
	p, err := PlanWrites(pgLike{}, guard, source.Changeset{Target: peopleRef, Identity: byID, Changes: changes, Confirmed: true}, "DEFAULT VALUES")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAChangesetIsWrittenAsBoundStatementsInItsOrder(t *testing.T) {
	p := plan(t, source.Guard{},
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(3)}, Values: map[string]any{"score": model.Decimal("4.50"), "name": "o'brien"}},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(4)}},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"name": "cat", "meta": model.JSON(`{"a":1}`)}},
		source.RowChange{Kind: source.ChangeInsert},
	)
	want := []string{
		`UPDATE "s"."people" SET "name" = $1, "score" = $2 WHERE "id" = $3 [o'brien 4.50 3]`,
		`DELETE FROM "s"."people" WHERE "id" = $1 [4]`,
		`INSERT INTO "s"."people" ("meta", "name") VALUES ($1, $2) [{"a":1} cat]`,
		`INSERT INTO "s"."people" DEFAULT VALUES []`,
	}
	for i, st := range p.Statements {
		if got := fmt.Sprintf("%s %v", st.SQL, st.Args); i >= len(want) || got != want[i] {
			t.Errorf("statement %d: %s", i, got)
		}
		if !st.Confirmed {
			t.Errorf("statement %d lost the changeset's consent", i)
		}
	}
	if len(p.Statements) != len(want) {
		t.Fatalf("%d statements", len(p.Statements))
	}
	if _, ok := p.Statements[0].Args[1].(string); !ok {
		t.Error("a decimal is bound as its text")
	}
	if _, ok := p.Statements[2].Args[0].(string); !ok {
		t.Error("JSON is bound as its text")
	}
	if got := strings.Join(p.Descriptions, "; "); got != "Update name, score where id = 3; Delete the row where id = 4; Insert a row: meta, name; Insert a row of defaults" {
		t.Errorf("descriptions: %s", got)
	}
	if !p.Atomic || p.Guarded || !reflect.DeepEqual(p.Target, peopleRef) {
		t.Errorf("atomic %v, guarded %v, target %v", p.Atomic, p.Guarded, p.Target)
	}
	if !plan(t, source.Guard{Environment: source.EnvProduction}).Guarded {
		t.Error("a plan for a production connection is guarded")
	}
}

func TestAChangeThatCannotBeWrittenWholeIsRefused(t *testing.T) {
	two := model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"a", "b"}, Target: peopleRef}
	for name, c := range map[string]struct {
		id     model.RowIdentity
		change source.RowChange
	}{
		"no key":            {model.RowIdentity{Kind: model.IdentityNone, Target: peopleRef}, source.RowChange{Kind: source.ChangeDelete, Key: []any{1}}},
		"no key at all":     {model.RowIdentity{Kind: model.IdentityNone, Target: peopleRef}, source.RowChange{Kind: source.ChangeDelete}},
		"an update, no key": {model.RowIdentity{}, source.RowChange{Kind: source.ChangeUpdate, Key: []any{1}, Values: map[string]any{"a": 1}}},
		"a NULL key":        {byID, source.RowChange{Kind: source.ChangeDelete, Key: []any{nil}}},
		"a short key":       {two, source.RowChange{Kind: source.ChangeDelete, Key: []any{1}}},
		"an empty update":   {byID, source.RowChange{Kind: source.ChangeUpdate, Key: []any{1}}},
		"an unknown kind":   {byID, source.RowChange{Kind: 9, Key: []any{1}}},
	} {
		if p, err := PlanWrites(pgLike{}, source.Guard{}, source.Changeset{Target: peopleRef, Identity: c.id, Changes: []source.RowChange{c.change}}, ""); err == nil {
			t.Errorf("%s: planned %v", name, p.Statements)
		}
	}
}

func TestAPlanIsAppliedWholeOrNotAtAll(t *testing.T) {
	p := plan(t, source.Guard{},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{1}},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{2}},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{3}})
	var committed, rolledBack int
	commit := func() error { committed++; return nil }
	rollback := func() error { rolledBack++; return nil }
	counts := func(ns ...int64) func(source.Statement) (int64, error) {
		i := 0
		return func(source.Statement) (int64, error) { i++; return ns[i-1], nil }
	}
	out := ApplyWith(p, counts(1, 1, 1), commit, rollback)
	if out.Err != nil || out.Applied != 3 || out.Affected != 3 || out.FailedAt != -1 || committed != 1 || rolledBack != 0 {
		t.Fatalf("all applied: %+v, committed %d", out, committed)
	}
	out = ApplyWith(p, counts(1, 2), commit, rollback)
	if !errors.Is(out.Err, ErrManyRows) || !strings.Contains(out.Err.Error(), "2 rows matched") || out.FailedAt != 1 || !out.RolledBack {
		t.Errorf("a statement matching two rows stops it all: %+v", out)
	}
	rolledBack = 0
	out = ApplyWith(p, counts(1, 0, 1), commit, rollback)
	if !errors.Is(out.Err, ErrNoRow) || out.FailedAt != 1 || !out.RolledBack || out.Affected != 0 || committed != 1 || rolledBack != 1 {
		t.Errorf("a statement matching no row stops it all: %+v", out)
	}
	boom := errors.New("duplicate key")
	out = ApplyWith(p, func(st source.Statement) (int64, error) { return 0, boom }, commit, rollback)
	if !errors.Is(out.Err, boom) || out.FailedAt != 0 || !out.RolledBack || rolledBack != 2 {
		t.Errorf("a server's refusal stops it all: %+v", out)
	}
	out = ApplyWith(p, counts(1, 1, 1), func() error { return boom }, rollback)
	if !errors.Is(out.Err, boom) || out.RolledBack || out.FailedAt != -1 {
		t.Errorf("a failed commit is said, and not taken for a rollback: %+v", out)
	}
	out = ApplyWith(p, counts(0), commit, func() error { return boom })
	if out.RolledBack {
		t.Error("a rollback that failed is not reported as done")
	}
}

func TestWritesAskTheGuardWithTheirConsent(t *testing.T) {
	p := plan(t, source.Guard{}, source.RowChange{Kind: source.ChangeDelete, Key: []any{1}})
	if err := AllowWrites(source.Guard{ReadOnly: true}, p); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("read-only: %v", err)
	}
	if err := AllowWrites(source.Guard{Environment: source.EnvProduction}, p); err != nil {
		t.Errorf("consent given: %v", err)
	}
	p.Statements[0].Confirmed = false
	if err := AllowWrites(source.Guard{Environment: source.EnvProduction}, p); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("production without consent: %v", err)
	}
}

func TestNewRowsNeedNoKey(t *testing.T) {
	add := source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"name": "a"}}
	p, err := PlanWrites(pgLike{}, source.Guard{}, source.Changeset{Target: peopleRef, Changes: []source.RowChange{add, add}}, "DEFAULT VALUES")
	if err != nil || len(p.Statements) != 2 || p.Statements[1].SQL != `INSERT INTO "s"."people" ("name") VALUES ($1)` {
		t.Fatalf("rows added to a table with no key: %v %+v", err, p)
	}
	del := source.RowChange{Kind: source.ChangeDelete, Key: []any{1}}
	if _, err := PlanWrites(pgLike{}, source.Guard{}, source.Changeset{Target: peopleRef, Changes: []source.RowChange{add, del}}, ""); err == nil {
		t.Error("a row deleted beside them still needs its key")
	}
}

func TestARemovedFieldIsRefused(t *testing.T) {
	// A column belongs to the table, so no row can be without it: only a
	// store whose rows hold their own fields can remove one (model.Removed).
	cs := source.Changeset{Target: peopleRef, Identity: byID,
		Changes: []source.RowChange{{Kind: source.ChangeUpdate, Key: []any{int64(1)},
			Values: map[string]any{"nickname": model.Removed{}}}}}
	_, err := PlanWrites(pgLike{}, source.Guard{}, cs, "DEFAULT VALUES")
	if err == nil {
		t.Fatal("a column was removed from one row")
	}
	if !strings.Contains(err.Error(), "document") {
		t.Errorf("error %q, want it to say where a field can be removed", err)
	}
}

// An engine that changes a row otherwise than with UPDATE … SET says so,
// and the planner writes what it says (UpdateWriter, T3.31).

// alterLike changes rows the way ClickHouse does.
type alterLike struct{ pgLike }

func (alterLike) UpdateStatement(table, sets, where string) string {
	return "ALTER TABLE " + table + " UPDATE " + sets + " WHERE " + where
}

func TestAnEngineMayWriteAChangeItsOwnWay(t *testing.T) {
	cs := source.Changeset{Target: peopleRef, Identity: byID,
		Changes: []source.RowChange{{Kind: source.ChangeUpdate, Key: []any{int64(1)},
			Values: map[string]any{"name": "uno"}}}}
	plain, err := PlanWrites(pgLike{}, source.Guard{}, cs, "DEFAULT VALUES")
	if err != nil {
		t.Fatal(err)
	}
	if got := plain.Statements[0].SQL; !strings.HasPrefix(got, "UPDATE ") {
		t.Errorf("a dialect that says nothing writes %q", got)
	}
	own, err := PlanWrites(alterLike{}, source.Guard{}, cs, "DEFAULT VALUES")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := own.Statements[0].SQL,
		`ALTER TABLE "s"."people" UPDATE "name" = $1 WHERE "id" = $2`; got != want {
		t.Errorf("a dialect that writes its own way writes\n%s\nwant\n%s", got, want)
	}
	// The same assignments and the same condition either way: only the
	// shape around them is the engine's.
	for _, sql := range []string{plain.Statements[0].SQL, own.Statements[0].SQL} {
		if !strings.Contains(sql, `"name" = $1`) || !strings.Contains(sql, `"id" = $2`) {
			t.Errorf("it writes %q", sql)
		}
	}
	// Only a change to a row is written the engine's way: deleting and
	// adding are the same statements either way.
	for _, c := range []source.RowChange{
		{Kind: source.ChangeDelete, Key: []any{int64(1)}},
		{Kind: source.ChangeInsert, Values: map[string]any{"name": "new"}},
	} {
		one := source.Changeset{Target: peopleRef, Identity: byID, Changes: []source.RowChange{c}}
		a, err := PlanWrites(pgLike{}, source.Guard{}, one, "DEFAULT VALUES")
		if err != nil {
			t.Fatal(err)
		}
		b, err := PlanWrites(alterLike{}, source.Guard{}, one, "DEFAULT VALUES")
		if err != nil {
			t.Fatal(err)
		}
		if a.Statements[0].SQL != b.Statements[0].SQL {
			t.Errorf("%v is written %q one way and %q the other", c.Kind, a.Statements[0].SQL, b.Statements[0].SQL)
		}
	}
}
