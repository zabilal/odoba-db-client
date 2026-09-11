package shell

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// editOne changes row 1's name.
func editOne(t *testing.T, tb *tab) {
	t.Helper()
	row, _ := tb.model.Row(tb.ctx, 1)
	if err := tb.grid.OnEdit(row, 1, "renamed"); err != nil {
		t.Fatal(err)
	}
}

// reviewed opens Review Changes, and gives what it says.
func reviewed(t *testing.T, fx *fixture) string {
	t.Helper()
	fx.s.run(cmdReviewChanges)
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("Review Changes shows nothing")
	}
	return labelText(top)
}

func tapOnTop(t *testing.T, fx *fixture, label string) {
	t.Helper()
	b := findButton(fx.s.win.Canvas().Overlays().Top(), label)
	if b == nil {
		t.Fatalf("no %q button", label)
	}
	test.Tap(b)
}

func TestReviewShowsEveryStatementBeforeAnythingRuns(t *testing.T) {
	fx, tb := loadedItems(t)
	before := len(writtenPlans())
	if tb.ed.review.Visible() || fx.s.canReview() {
		t.Error("with no changes there is nothing to review")
	}
	editOne(t, tb)
	if !tb.ed.review.Visible() || !fx.s.canReview() {
		t.Fatal("a change pending offers Review Changes beside the footer")
	}
	text := reviewed(t, fx)
	for _, want := range []string{"1 statement will run in one transaction", "1. change 1", "map[name:renamed]", "Values: 1 = 'x'"} {
		if !strings.Contains(text, want) {
			t.Errorf("the review says %q, not %q", text, want)
		}
	}
	if len(writtenPlans()) != before {
		t.Error("nothing runs while the changes are reviewed")
	}
	tapOnTop(t, fx, "Cancel")
	if tb.ed.pending.Len() != 1 || len(writtenPlans()) != before {
		t.Error("Cancel writes nothing, and keeps the changes")
	}
}

func TestCommitWritesTheChangesAndReadsTheRowsAgain(t *testing.T) {
	fx, tb := loadedItems(t)
	before := len(writtenPlans())
	editOne(t, tb)
	fx.s.run(cmdInsertRow)
	reviewed(t, fx)
	browses.Lock()
	browsed := len(browses.opts)
	browses.Unlock()
	tapOnTop(t, fx, "Commit")
	if fx.s.canReview() || !tb.ed.review.Disabled() || !tb.ed.committing {
		t.Error("while the changes are written they are not reviewed again")
	}
	pump(t, fx.q, func() bool {
		return tb.ed.pending.Len() == 0 && strings.Contains(tb.footer.Text, "Committed 2 changes")
	})
	pump(t, fx.q, func() bool { browses.Lock(); defer browses.Unlock(); return len(browses.opts) > browsed })
	plans := writtenPlans()
	if len(plans) != before+1 || len(plans[before].Statements) != 2 {
		t.Fatalf("one plan of two statements written: %d plans", len(plans)-before)
	}
	if tb.model.Added() != 0 || tb.ed.review.Visible() || tb.ed.committing {
		t.Errorf("written, the changes are gone: %d new rows, review shown %v", tb.model.Added(), tb.ed.review.Visible())
	}
}

func TestAFailedCommitSaysWhichChangeAndSelectsItsRow(t *testing.T) {
	failWrite.Store(2)
	t.Cleanup(func() { failWrite.Store(0) })
	fx, tb := loadedItems(t)
	editOne(t, tb)
	row3, _ := tb.model.Row(tb.ctx, 3)
	tb.ed.pending.Delete(row3)
	reviewed(t, fx)
	tapOnTop(t, fx, "Commit")
	pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, "Not committed") })
	if !strings.Contains(tb.footer.Text, "change 2 failed: fakesql: duplicate key. Nothing was written.") {
		t.Errorf("footer %q", tb.footer.Text)
	}
	if tb.ed.pending.Len() != 2 || tb.ed.committing {
		t.Error("the changes stay, to be put right")
	}
	if a, ok := tb.grid.Selection().Active(); !ok || a != (grid.CellID{Row: 3, Col: 0}) {
		t.Errorf("the row the failing change is to is selected: %v %v", a, ok)
	}

	failWrite.Store(3) // the new row's insert
	fx.s.run(cmdInsertRow)
	tb.grid.GoTo(grid.CellID{Row: 5, Col: 1})
	reviewed(t, fx)
	tapOnTop(t, fx, "Commit")
	pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, "change 3 failed") })
	if a, _ := tb.grid.Selection().Active(); a != (grid.CellID{Row: 0, Col: 0}) {
		t.Errorf("a new row whose insert fails is selected: %v", a)
	}
}

func TestProductionAsksBeforeCommitting(t *testing.T) {
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "prod", Driver: "postgres", Host: "db1", Environment: "production"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool {
		if tb.model == nil {
			return false
		}
		_, ok := tb.model.Row(tb.ctx, 3)
		return ok
	})
	before := len(writtenPlans())
	editOne(t, tb)
	if text := reviewed(t, fx); !strings.Contains(text, "marked Production, so Commit asks again") {
		t.Errorf("the review says a production plan asks again: %q", text)
	}
	tapOnTop(t, fx, "Commit")
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "marked Production") || len(writtenPlans()) != before {
		t.Fatalf("Commit asks before writing to production: %q", text)
	}
	tapOnTop(t, fx, "Cancel")
	if len(writtenPlans()) != before || !strings.Contains(tb.footer.Text, "Not committed") || tb.ed.pending.Len() != 1 {
		t.Fatalf("no to production writes nothing: %q", tb.footer.Text)
	}
	reviewed(t, fx)
	tapOnTop(t, fx, "Commit")
	tapOnTop(t, fx, "Commit")
	pump(t, fx.q, func() bool { return len(writtenPlans()) == before+1 })
	if plan := writtenPlans()[before]; !plan.Statements[0].Confirmed {
		t.Error("the plan written carries the consent")
	}
}

func TestAReviewSaysWhatItCannotPromise(t *testing.T) {
	test.NewTempApp(t)
	at := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	body := reviewBody(&source.WritePlan{Statements: []source.Statement{
		{SQL: "DELETE", Args: []any{nil, "o'brien", int64(3), []byte{1, 2}, at}}}})
	text := labelText(body)
	for _, want := range []string{"1 statement will run one by one: if one fails, those before it stay written.",
		"1. Statement", "Values: 1 = NULL, 2 = 'o''brien', 3 = 3, 4 = 2 bytes, 5 = 2024-05-06T07:08:09Z"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q does not say %q", text, want)
		}
	}
}

func TestAReadOnlyConnectionEditsNothing(t *testing.T) {
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "ro", Driver: "postgres", Host: "db1", ReadOnly: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	if tb.ed.pending != nil || tb.grid.OnEdit != nil || fx.s.canInsert() {
		t.Error("a read-only connection edits nothing")
	}
}
