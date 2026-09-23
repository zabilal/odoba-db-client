package clickhouse

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Planning what a grid's changes would write (FR-4.4, ADR-0142).

func planFor(t *testing.T, changes ...source.RowChange) *source.WritePlan {
	t.Helper()
	s := &clickhouseSource{}
	plan, err := s.Plan(context.Background(), source.Changeset{
		Target:   tbl,
		Identity: model.RowIdentity{Kind: model.IdentityChosen, Columns: []string{"id"}, Target: tbl},
		Changes:  changes,
	})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	return plan
}

// A change to rows is written as an ALTER, and waited for: a mutation is
// asynchronous by default, and a grid that said a row had changed before it
// had would be telling whoever changed it something untrue.
func TestAChangeToRowsIsWrittenAsAnAlterAndWaitedFor(t *testing.T) {
	plan := planFor(t, source.RowChange{Kind: source.ChangeUpdate,
		Key: []any{int64(1)}, Values: map[string]any{"name": "uno"}})
	want := "ALTER TABLE `shop`.`people` UPDATE `name` = ? WHERE `id` = ? SETTINGS mutations_sync = 1"
	if got := plan.Statements[0].SQL; got != want {
		t.Errorf("it writes\n%s\nwant\n%s", got, want)
	}
	if got := tsql.UpdateStatement("t", "a = ?", "b = ?"); got != "ALTER TABLE t UPDATE a = ? WHERE b = ? SETTINGS mutations_sync = 1" {
		t.Errorf("UpdateStatement gives %q", got)
	}
}

// Deleting and adding are written the way every engine writes them.
func TestDeletingAndAddingAreOrdinary(t *testing.T) {
	plan := planFor(t,
		source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(2)}},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"id": int64(3), "name": "three"}})
	if got, want := plan.Statements[0].SQL, "DELETE FROM `shop`.`people` WHERE `id` = ?"; got != want {
		t.Errorf("a delete writes %q, want %q", got, want)
	}
	if got, want := plan.Statements[1].SQL, "INSERT INTO `shop`.`people` (`id`, `name`) VALUES (?, ?)"; got != want {
		t.Errorf("an insert writes %q, want %q", got, want)
	}
}

// Every change that addresses a row carries the question of whether it
// addresses exactly one; adding a row addresses none, and carries nothing.
func TestAChangeThatAddressesARowAsksHowMany(t *testing.T) {
	plan := planFor(t,
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(1)}, Values: map[string]any{"name": "uno"}},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(2)}},
		source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"id": int64(3)}})
	for i, want := range []string{
		"SELECT count() FROM `shop`.`people` WHERE `id` = ?",
		"SELECT count() FROM `shop`.`people` WHERE `id` = ?",
		"",
	} {
		chk, ok := plan.Statements[i].Op.(*rowCheck)
		switch {
		case want == "" && ok:
			t.Errorf("statement %d asks %q, and addresses no row", i, chk.SQL)
		case want == "":
		case !ok:
			t.Errorf("statement %d asks nothing, and addresses a row", i)
		case chk.SQL != want:
			t.Errorf("statement %d asks\n%s\nwant\n%s", i, chk.SQL, want)
		case len(chk.Args) != 1:
			t.Errorf("statement %d asks with %#v", i, chk.Args)
		}
	}
}

// A key of several columns is asked about whole.
func TestAKeyOfSeveralColumnsIsAskedAboutWhole(t *testing.T) {
	s := &clickhouseSource{}
	plan, err := s.Plan(context.Background(), source.Changeset{
		Target:   tbl,
		Identity: model.RowIdentity{Kind: model.IdentityChosen, Columns: []string{"a", "b"}, Target: tbl},
		Changes:  []source.RowChange{{Kind: source.ChangeDelete, Key: []any{int64(1), "x"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	chk := plan.Statements[0].Op.(*rowCheck)
	want := "SELECT count() FROM `shop`.`people` WHERE `a` = ? AND `b` = ?"
	if chk.SQL != want {
		t.Errorf("it asks\n%s\nwant\n%s", chk.SQL, want)
	}
	if len(chk.Args) != 2 || chk.Args[0] != int64(1) || chk.Args[1] != "x" {
		t.Errorf("it asks with %#v", chk.Args)
	}
}

// Nothing here is undone, so a plan does not say it will be (FR-4.5).
func TestAPlanIsNotAppliedAllAtOnce(t *testing.T) {
	if plan := planFor(t, source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(1)}}); plan.Atomic {
		t.Error("the plan says it is applied all at once")
	}
	if err := noCommit(); err != nil {
		t.Errorf("committing what needs no commit: %v", err)
	}
	if noRollback() == nil {
		t.Error("undoing said it had undone something, and there is nothing here to undo it in")
	}
}

// A row of nothing but defaults is refused: ClickHouse has no form for one.
func TestARowOfNothingIsRefused(t *testing.T) {
	s := &clickhouseSource{}
	_, err := s.Plan(context.Background(), source.Changeset{
		Target:   tbl,
		Identity: model.RowIdentity{Kind: model.IdentityChosen, Columns: []string{"id"}, Target: tbl},
		Changes:  []source.RowChange{{Kind: source.ChangeInsert}},
	})
	if err == nil || !strings.Contains(err.Error(), "at least one column") {
		t.Errorf("a row of nothing was planned: %v", err)
	}
}

// Rows with no key at all are not written: there would be nothing to say
// which row a change was for (FR-4.7).
func TestRowsWithNoKeyAreNotWritten(t *testing.T) {
	s := &clickhouseSource{}
	_, err := s.Plan(context.Background(), source.Changeset{
		Target:   tbl,
		Identity: model.RowIdentity{Kind: model.IdentityNone},
		Changes:  []source.RowChange{{Kind: source.ChangeDelete, Key: []any{int64(1)}}},
	})
	if err == nil {
		t.Error("a change was planned against rows that cannot be told apart")
	}
}
