package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// formOn opens a tab's form view on a row of its grid g.
func formOn(t *testing.T, fx *fixture, tb *tab, g *grid.TableGrid, row int) *formView {
	t.Helper()
	g.Select(grid.CellID{Row: row, Col: 1}, grid.CellID{Row: row, Col: 1})
	fx.s.run(cmdFormView)
	f := tb.forms[g]
	if f == nil || !f.shown() {
		t.Fatal("the form view did not open")
	}
	return f
}

// submit types text into a field and presses Return.
func submit(ff *formField, text string) {
	ff.entry.SetText(text)
	ff.entry.OnSubmitted(text)
}

func TestTheFormViewShowsTheActiveRowInTheGridsPlace(t *testing.T) {
	fx, tb := loadedItems(t)
	f := formOn(t, fx, tb, tb.grid, 3)
	if tb.body.Objects[0] != f.box {
		t.Error("the form takes the grid's place")
	}
	if len(f.fields) != 2 || f.fields[0].item.Text != "id" || f.fields[0].entry.Text != "3" || f.fields[1].entry.Text != "item 3" {
		t.Fatalf("a field for each column, with the row's values: %d fields", len(f.fields))
	}
	if f.where.Text != "Row 4 of 250" || f.prev.Disabled() || f.next.Disabled() {
		t.Errorf("which row, and both ways open: %q", f.where.Text)
	}
	f.next.OnTapped()
	if a, _ := tb.grid.Selection().Active(); a.Row != 4 || f.where.Text != "Row 5 of 250" || f.fields[1].entry.Text != "item 4" {
		t.Errorf("Next Row moves the grid's row, and the form: %v, %q", a, f.where.Text)
	}
	for range 5 {
		f.prev.OnTapped()
	}
	if f.where.Text != "Row 1 of 250" || !f.prev.Disabled() {
		t.Errorf("Previous Row stops at the first: %q", f.where.Text)
	}
	tb.grid.HideColumn(0)
	if len(f.fields) != 1 || f.fields[0].item.Text != "name" {
		t.Errorf("a column hidden has no field, at once: %d fields", len(f.fields))
	}
	f.next.OnTapped()
	if f.where.Text != "Row 2 of 250" {
		t.Errorf("Next Row moves in the column left: %q", f.where.Text)
	}
	fx.s.run(cmdFormView)
	if f.shown() || tb.body.Objects[0] != tb.grid.View() {
		t.Error("toggled again, the grid has its place back")
	}
}

func TestAFieldTypedIntoIsAPendingChange(t *testing.T) {
	fx, tb := loadedItems(t)
	f := formOn(t, fx, tb, tb.grid, 1)
	submit(f.fields[1], "renamed")
	row, _ := tb.model.Row(tb.ctx, 1)
	if v, _ := tb.ed.pending.Value(row, 1); v != "renamed" || !strings.HasSuffix(tb.footer.Text, " · 1 pending change") {
		t.Fatalf("a pending change: %v; footer %q", v, tb.footer.Text)
	}
	if !strings.Contains(f.fields[1].item.HintText, "changed") || !strings.HasSuffix(f.where.Text, " · changed") {
		t.Errorf("marked in words: %q, %q", f.fields[1].item.HintText, f.where.Text)
	}
	submit(f.fields[0], "x")
	if !strings.HasPrefix(f.fields[0].item.HintText, "Not written: ") || tb.ed.pending.Len() != 1 || f.fields[0].entry.Text != "x" {
		t.Errorf("a value its column cannot hold is said, and stays typed: %q", f.fields[0].item.HintText)
	}
	f.fields[0].entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if f.fields[0].entry.Text != "1" {
		t.Errorf("Escape puts it back: %q", f.fields[0].entry.Text)
	}
	f.fields[1].entry.SetText("again")
	f.next.OnTapped() // what was typed is kept before the row changes
	if v, _ := tb.ed.pending.Value(row, 1); v != "again" {
		t.Errorf("moving on keeps what was typed: %v", v)
	}
	f.fields[1].entry.SetText("left")
	f.fields[1].entry.FocusLost()
	row2, _ := tb.model.Row(tb.ctx, 2)
	if v, _ := tb.ed.pending.Value(row2, 1); v != "left" {
		t.Errorf("leaving a field keeps what was typed: %v", v)
	}
}

func TestANewRowIsFilledInInTheForm(t *testing.T) {
	fx, tb := loadedItems(t)
	fx.s.run(cmdInsertRow)
	fx.s.run(cmdFormView)
	f := tb.forms[tb.grid]
	if f == nil || f.where.Text != "New row 1 of 1" || f.fields[0].entry.Text != "" {
		t.Fatalf("the new row, given nothing: %v", f)
	}
	submit(f.fields[0], "5")
	if added := tb.ed.pending.Added(); added[0][0] != int64(5) || f.fields[0].entry.Text != "5" {
		t.Errorf("its column given: %v", added)
	}
}

func TestTheFormIsReadOnlyWhereTheRowIsNotEdited(t *testing.T) {
	fx, tb := loadedItems(t)
	row, _ := tb.model.Row(tb.ctx, 2)
	tb.ed.pending.Delete(row)
	f := formOn(t, fx, tb, tb.grid, 2)
	if f.fields[1].entry.Visible() || f.fields[1].text.Text != "item 2" || !strings.HasSuffix(f.where.Text, " · to be deleted") {
		t.Errorf("a row to be deleted is not typed into: %q", f.where.Text)
	}

	fx2 := newFixture(t)
	c := fx2.create(t, "nokey", nil)
	fx2.s.OpenObject(c.ID, itemsNode)
	tb2 := fx2.onlyTab(t)
	pump(t, fx2.q, func() bool {
		if tb2.model == nil {
			return false
		}
		_, ok := tb2.model.Row(tb2.ctx, 3)
		return ok
	})
	fx2.s.run(cmdFormView) // nothing selected
	f2 := tb2.forms[tb2.grid]
	if f2 == nil || f2.where.Text != "Row 1 of 250" || f2.fields[0].entry.Visible() || f2.fields[0].text.Text != "0" {
		t.Error("with no row selected it opens on the first; rows that are not edited are shown, not typed into")
	}
}

func TestTheViewerAndTheFormTakeTurns(t *testing.T) {
	fx, tb := loadedItems(t)
	f := formOn(t, fx, tb, tb.grid, 1)
	fx.s.run(cmdCellViewer)
	v := tb.viewers[tb.grid]
	if f.shown() || v == nil || !v.shown() {
		t.Fatal("the viewer takes the form's place, beside the grid")
	}
	fx.s.run(cmdFormView)
	if !f.shown() || v.shown() {
		t.Fatal("the form takes the grid and its viewer's place")
	}
	fx.s.run(cmdFormView)
	if !v.shown() {
		t.Error("closed, the form gives back the grid and its viewer")
	}
	fx.s.run(cmdFormView)
	f.fields[1].entry.SetText("typed")
	fx.s.run(cmdCellViewer)
	if f.shown() || !v.shown() {
		t.Error("asked for while open under the form, the viewer comes back")
	}
	row, _ := tb.model.Row(tb.ctx, 1)
	if val, _ := tb.ed.pending.Value(row, 1); val != "typed" {
		t.Errorf("what was typed in the form is kept: %v", val)
	}
}

func TestAResultHasAFormToo(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	r := ranItems(t, fx, q)
	f := formOn(t, fx, tb, q.grids[0], 2)
	submit(f.fields[1], "renamed")
	row, _ := r.ed.model.Row(r.ed.ctx, 2)
	if v, _ := r.ed.pending.Value(row, 1); v != "renamed" || !strings.Contains(r.count.Text, "1 pending change") {
		t.Errorf("the result's own changes take it: %v; %q", v, r.count.Text)
	}
	q.grids[0].HideColumn(0)
	if len(f.fields) != 1 {
		t.Errorf("a result's column hidden has no field: %d fields", len(f.fields))
	}
}
