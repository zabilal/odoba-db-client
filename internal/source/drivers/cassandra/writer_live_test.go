//go:build conformance

package cassandra

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlscript"
)

// Rows written back from the grid, against a real cluster (FR-4.4,
// ADR-0143).

// writable makes the table these tests write, empty.
func writable(t *testing.T, src source.Source) *cassandraSource {
	t.Helper()
	s := src.(*cassandraSource)
	seeded(t, src)
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS ` + fixture + `.writes (id int PRIMARY KEY, name text, n int)`,
		`TRUNCATE ` + fixture + `.writes`,
	} {
		if err := s.session.Query(ddl).WithContext(context.Background()).Exec(); err != nil {
			t.Fatalf("making the table to write: %v", err)
		}
	}
	return s
}

func applyTo(t *testing.T, s *cassandraSource, changes ...source.RowChange) *source.WriteOutcome {
	t.Helper()
	ctx := context.Background()
	plan, err := s.Plan(ctx, source.Changeset{Target: writesRef, Identity: byKey(),
		Changes: changes, Confirmed: true})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	out, err := s.Apply(ctx, plan)
	if err != nil {
		t.Fatalf("applying: %v", err)
	}
	return out
}

func rowsNow(t *testing.T, s *cassandraSource) string {
	t.Helper()
	rs, err := s.Browse(context.Background(), writesRef, source.BrowseOptions{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []string
	for {
		row, err := rs.Next(context.Background())
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(row))
	}
	// A partition's rows come in the ring's order, not the key's.
	slicesSort(out)
	return strings.Join(out, " ")
}

func slicesSort(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// added is a new row. Every column is given a value: what an unset one
// reads back as is gocql's business, not this test's.
func added(id int, name string, n int) source.RowChange {
	return source.RowChange{Kind: source.ChangeInsert,
		Values: map[string]any{"id": id, "name": name, "n": n}}
}

// A row is added, changed and deleted by its key.
func TestLiveRowsAreWrittenByTheirKey(t *testing.T) {
	s := writable(t, live(t, liveConfig(fixture)))
	out := applyTo(t, s, added(1, "one", 1), added(2, "two", 2))
	if out.Err != nil || out.Applied != 2 || out.Affected != 2 {
		t.Fatalf("adding rows: %+v", out)
	}
	if got := rowsNow(t, s); got != "[1 1 one] [2 2 two]" {
		t.Fatalf("after adding: %s", got)
	}
	out = applyTo(t, s,
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{1}, Values: map[string]any{"name": "uno"}},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{2}})
	if out.Err != nil || out.Applied != 2 {
		t.Fatalf("changing and deleting: %+v", out)
	}
	if got := rowsNow(t, s); got != "[1 1 uno]" {
		t.Errorf("after them: %s", got)
	}
}

// CQL has no writes, only upserts. A change to a row that is not there
// would make one, and this refuses instead (ADR-0143).
func TestLiveAChangeToARowThatIsNotThereMakesNone(t *testing.T) {
	s := writable(t, live(t, liveConfig(fixture)))
	applyTo(t, s, added(1, "one", 7))

	out := applyTo(t, s, source.RowChange{Kind: source.ChangeUpdate,
		Key: []any{99}, Values: map[string]any{"name": "ghost"}})
	if out.Err == nil || !errors.Is(out.Err, sqlscript.ErrNoRow) {
		t.Errorf("a change to a row that is not there: %+v", out)
	}
	if got := rowsNow(t, s); got != "[1 7 one]" {
		t.Errorf("a row was made by a change: %s", got)
	}
	// Deleting one that is not there is refused for the same reason: it
	// would say it had deleted something.
	out = applyTo(t, s, source.RowChange{Kind: source.ChangeDelete, Key: []any{99}})
	if out.Err == nil || !errors.Is(out.Err, sqlscript.ErrNoRow) {
		t.Errorf("deleting a row that is not there: %+v", out)
	}
}

// A new row whose key is taken would overwrite the row that has it, which
// is not what adding a row means.
func TestLiveANewRowDoesNotOverwriteTheRowItCollidesWith(t *testing.T) {
	s := writable(t, live(t, liveConfig(fixture)))
	applyTo(t, s, added(1, "first", 1))
	out := applyTo(t, s, added(1, "second", 2))
	if out.Err == nil || !strings.Contains(out.Err.Error(), "already there") {
		t.Errorf("adding a row over another: %+v", out)
	}
	if got := rowsNow(t, s); got != "[1 1 first]" {
		t.Errorf("the row that was there is as it was: %s", got)
	}
}

// A plan that fails half way leaves the half that ran: CQL has no
// transaction to undo it in, and the outcome says so (FR-4.5).
func TestLiveAFailedPlanIsNotUndone(t *testing.T) {
	s := writable(t, live(t, liveConfig(fixture)))
	applyTo(t, s, added(1, "one", 5))
	out := applyTo(t, s,
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{1}, Values: map[string]any{"name": "changed"}},
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{99}, Values: map[string]any{"name": "ghost"}})
	if out.Err == nil || out.FailedAt != 1 || out.Applied != 1 {
		t.Fatalf("a plan whose second change fails: %+v", out)
	}
	if out.RolledBack {
		t.Error("the outcome says it was undone, and CQL has nothing to undo it in")
	}
	if got := rowsNow(t, s); got != "[1 5 changed]" {
		t.Errorf("the change that ran is still there: %s", got)
	}
}

// A read-only connection writes nothing, and a production one asks first
// (FR-1.8, FR-4.9).
func TestLiveWritingIsGuarded(t *testing.T) {
	s := writable(t, live(t, liveConfig(fixture)))
	applyTo(t, s, added(1, "one", 9))
	ctx := context.Background()

	roCfg := liveConfig(fixture)
	roCfg.Guard = source.Guard{ReadOnly: true}
	ro := live(t, roCfg).(*cassandraSource)
	plan, err := ro.Plan(ctx, source.Changeset{Target: writesRef, Identity: byKey(),
		Changes: []source.RowChange{added(2, "two", 2)}, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ro.Apply(ctx, plan); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a read-only connection wrote: %v", err)
	}

	prodCfg := liveConfig(fixture)
	prodCfg.Guard = source.Guard{Environment: source.EnvProduction}
	prod := live(t, prodCfg).(*cassandraSource)
	plan, err = prod.Plan(ctx, source.Changeset{Target: writesRef, Identity: byKey(),
		Changes: []source.RowChange{{Kind: source.ChangeDelete, Key: []any{1}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Guarded {
		t.Error("a production plan is guarded")
	}
	if _, err := prod.Apply(ctx, plan); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("production without consent: %v", err)
	}
	if got := rowsNow(t, s); got != "[1 9 one]" {
		t.Errorf("the row nobody was allowed to write is as it was: %s", got)
	}
}

// The window is told rows can be written, and that a changeset is not
// written all at once.
func TestLiveTheWindowIsToldWhatCanBeWritten(t *testing.T) {
	s := writable(t, live(t, liveConfig(fixture)))
	caps := s.Capabilities()
	if !caps.Data.Insert || !caps.Data.Update || !caps.Data.Delete {
		t.Errorf("the window is not told rows can be written: %+v", caps.Data)
	}
	if caps.Data.TransactionalWrite {
		t.Error("a changeset was said to be written all at once, and CQL has no transaction")
	}
	// And a row read from the grid says the key it is addressed by.
	rs, err := s.Browse(context.Background(), writesRef, source.BrowseOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	id := model.IdentityOf(rs)
	if id.Kind != model.IdentityPrimaryKey || len(id.Columns) == 0 {
		t.Errorf("the rows are known by %+v", id)
	}
}
