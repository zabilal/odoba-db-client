package shell

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// rereads counts the statements run again once a result's changes are
// written; failReread makes them fail.
var (
	rereads    atomic.Int64
	failReread atomic.Bool
)

// itemsResult is a query's result of the items table, known by its key.
func itemsResult() model.RowStream { return &sliceStream{end: int64(fakeRows), keyed: true} }

// joinedStream is rows read from the items table but not known by its key,
// as a join's would be.
type joinedStream struct{ *sliceStream }

func (joinedStream) Columns() []model.ColumnDef {
	cols := (*sliceStream)(nil).Columns()
	for i := range cols {
		cols[i].Origin = itemsNode.Ref
	}
	return cols
}

// ranItems runs a query of the items table, and waits for its first rows.
func ranItems(t *testing.T, fx *fixture, q *queryTab) *result {
	t.Helper()
	q.editor.Document().SetText("items;")
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return !q.executing && len(q.res) == 1 })
	r := q.res[0]
	if r.ed == nil {
		t.Fatal("a result of one table, known by its key, is edited")
	}
	pump(t, fx.q, func() bool { _, ok := r.ed.model.Row(r.ed.ctx, 3); return ok })
	return r
}

// renameOne changes the name of a result's row 1.
func renameOne(t *testing.T, r *result) {
	t.Helper()
	row, _ := r.ed.model.Row(r.ed.ctx, 1)
	if err := r.ed.grid.OnEdit(row, 1, "renamed"); err != nil {
		t.Fatal(err)
	}
}

func TestAResultOfOneTableIsEditedAndCommittedToIt(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	r := ranItems(t, fx, q)
	e := r.ed
	if e.pending == nil || !q.grids[0].Table.ShowHeaderColumn || e.review.Visible() {
		t.Fatal("its changes are marked in a gutter, and there are none yet to review")
	}
	renameOne(t, r)
	fx.s.run(cmdInsertRow) // the Edit menu acts on the result in front
	if e.model.Added() != 1 || !strings.HasSuffix(r.count.Text, " · 2 pending changes") || !e.review.Visible() {
		t.Fatalf("the changes are counted under the result: %q", r.count.Text)
	}
	before, reads, old := len(writtenPlans()), rereads.Load(), r.rs
	if text := reviewed(t, fx); !strings.Contains(text, "2 statements will run in one transaction") {
		t.Errorf("the review says %q", text)
	}
	tapOnTop(t, fx, "Commit")
	pump(t, fx.q, func() bool { return rereads.Load() == reads+1 && q.sets[0] != old })
	plans := writtenPlans()
	if len(plans) != before+1 || !plans[before].Target.Equal(itemsNode.Ref) {
		t.Fatalf("the changes are written to the result's table: %d plans", len(plans)-before)
	}
	if e.pending.Len() != 0 || e.model.Added() != 0 || e.review.Visible() || r.rs != q.sets[0] {
		t.Error("written, the changes are gone, and the statement's rows are read again into the same grid")
	}
	pump(t, fx.q, func() bool { row, _ := e.model.Row(e.ctx, 0); return row != nil && row[0] == int64(1) })
	pump(t, fx.q, func() bool { return strings.HasPrefix(r.count.Text, "999 rows") })
	pump(t, fx.q, func() bool { n, ok := e.model.Total(); return ok && n == 999 }) // the grid's extent, past its first page
	if !strings.Contains(r.count.Text, "Committed 2 changes") {
		t.Errorf("count %q", r.count.Text)
	}
}

func TestAResultsChangesAreAskedAboutBeforeTheyAreLost(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	r := ranItems(t, fx, q)
	renameOne(t, r)
	fx.s.run(cmdQueryRun)
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "The results have 1 change not committed, which running again discards.") {
		t.Fatalf("running again asks first: %q", text)
	}
	tapOnTop(t, fx, "Cancel")
	if len(q.res) != 1 || q.res[0] != r || pendingIn(tb) != 1 {
		t.Fatal("Cancel keeps the result and its change")
	}
	fx.s.requestClose(tb.item)
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "has 1 change not committed, which closing discards.") {
		t.Errorf("closing the tab asks too: %q", text)
	}
	tapOnTop(t, fx, "Cancel")
	r.ed.committing = true
	if fx.s.canRun() {
		t.Error("nothing runs while a result's changes are being written")
	}
	r.ed.committing = false
	fx.s.run(cmdQueryRun)
	tapOnTop(t, fx, "Run")
	pump(t, fx.q, func() bool { return !q.executing && len(q.res) == 1 && q.res[0] != r })
	if pendingIn(tb) != 0 {
		t.Error("the run's new result has no changes")
	}
}

func TestOnlyAResultKnownByItsTablesKeyIsEdited(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	q.editor.Document().SetText("joined; rows 3;")
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return !q.executing && len(q.res) == 2 })
	for i, r := range q.res {
		if r.ed != nil || q.grids[i].OnEdit != nil || q.grids[i].Table.ShowHeaderColumn {
			t.Errorf("result %d is edited", i+1)
		}
	}
	pump(t, fx.q, func() bool {
		return strings.Contains(q.res[0].count.Text, resultKeyText) && strings.HasPrefix(q.res[1].count.Text, "3 rows")
	})
	if strings.Contains(q.res[1].count.Text, "Read-only") {
		t.Errorf("a result read from no table says nothing of a key: %q", q.res[1].count.Text)
	}
	if fx.s.canInsert() || fx.s.canEditCell() || fx.s.canReview() {
		t.Error("the Edit menu has nothing to do on a result that is not edited")
	}
}

func TestAReadOnlyConnectionsResultIsNotEdited(t *testing.T) {
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "ro", Driver: "postgres", Host: "db1", ReadOnly: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenQuery(c.ID)
	q := fx.onlyTab(t).query
	pump(t, fx.q, func() bool { return q.session != nil })
	q.editor.Document().SetText("items; joined;")
	fx.s.run(cmdQueryRunAll)
	pump(t, fx.q, func() bool { return !q.executing && len(q.res) == 2 })
	pump(t, fx.q, func() bool { return strings.HasPrefix(q.res[1].count.Text, "5 rows") })
	if q.res[0].ed != nil || strings.Contains(q.res[1].count.Text, "Read-only:") {
		t.Errorf("a read-only connection edits nothing, and needs no key to say why: %q", q.res[1].count.Text)
	}
}

func TestAResultNotReadAgainSaysSoAndKeepsItsRows(t *testing.T) {
	failReread.Store(true)
	t.Cleanup(func() { failReread.Store(false) })
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	r := ranItems(t, fx, q)
	renameOne(t, r)
	old := r.rs
	reviewed(t, fx)
	tapOnTop(t, fx, "Commit")
	pump(t, fx.q, func() bool {
		return strings.Contains(r.count.Text, "Committed 1 change, but the rows could not be read again: fakesql: the table has gone")
	})
	if r.rs != old || q.sets[0] != old || r.ed.pending.Len() != 0 {
		t.Error("the rows read before stay, and the changes written are gone")
	}
}
