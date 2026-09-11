package shell

import (
	"fmt"
	"strings"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// loadedItems is the items table open, its first rows loaded.
func loadedItems(t *testing.T) (*fixture, *tab) {
	t.Helper()
	fx, tb := openItems(t)
	pump(t, fx.q, func() bool { _, ok := tb.model.Row(tb.ctx, 3); return ok })
	return fx, tb
}

func TestAnEditIsHeldAndCounted(t *testing.T) {
	_, tb := loadedItems(t)
	row, _ := tb.model.Row(tb.ctx, 1)
	if err := tb.grid.OnEdit(row, 1, "renamed"); err != nil {
		t.Fatal(err)
	}
	if v, ok := tb.ed.pending.Value(row, 1); !ok || v != "renamed" {
		t.Errorf("the edit is held as a pending change: %v %v", v, ok)
	}
	if !strings.HasSuffix(tb.footer.Text, " · 1 pending change") {
		t.Errorf("the footer counts it: %q", tb.footer.Text)
	}
}

func TestEditCellOpensAnEditorOnTheActiveCell(t *testing.T) {
	fx, tb := loadedItems(t)
	if fx.s.canEditCell() || fx.s.canChangeRows() {
		t.Error("with no cell selected there is nothing to edit")
	}
	tb.grid.Select(grid.CellID{Row: 1, Col: 1}, grid.CellID{Row: 1, Col: 1})
	if !fx.s.canEditCell() {
		t.Fatal("a keyed table's cell can be edited")
	}
	fx.s.run(cmdEditCell)
	f := fyne.CurrentApp().Driver().CanvasForObject(tb.grid.Table).Focused()
	if fmt.Sprintf("%T", f) != "*grid.editEntry" {
		t.Errorf("Edit Cell should give the keyboard to the cell's editor, not %T", f)
	}
}

func TestSetToNULLChangesTheSelectedCellsThatCanBeNULL(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 1, Col: 0}, grid.CellID{Row: 1, Col: 1})
	tb.grid.ToggleCell(grid.CellID{Row: 3, Col: 1})
	fx.s.run(cmdSetNull)
	pump(t, fx.q, func() bool { return !strings.Contains(tb.footer.Text, "Setting…") })
	for _, r := range []int{1, 3} {
		row, _ := tb.model.Row(tb.ctx, int64(r))
		if v, ok := tb.ed.pending.Value(row, 1); !ok || v != nil {
			t.Errorf("row %d's name: %v %v", r, v, ok)
		}
		if _, ok := tb.ed.pending.Value(row, 0); ok {
			t.Errorf("row %d's id cannot be NULL", r)
		}
	}
	if row, _ := tb.model.Row(tb.ctx, 2); tb.ed.pending.State(row) != 0 {
		t.Error("a row between the cells selected is not changed")
	}
	if !strings.Contains(tb.footer.Text, "2 pending changes") || !strings.Contains(tb.footer.Text, "the first not set: id: cannot be NULL") {
		t.Errorf("the footer counts the changes and says what was not changed: %q", tb.footer.Text)
	}
}

func TestAKeyedTableKeepsItsChangesThroughASort(t *testing.T) {
	fx, tb := openItems(t)
	if tb.ed.pending == nil || !tb.grid.Table.ShowHeaderColumn {
		t.Fatal("a table whose rows have a key should hold its edits and mark them in a gutter")
	}
	pending := tb.ed.pending
	tb.grid.ToggleSort(1, false)
	pump(t, fx.q, func() bool { return len(tb.applied) == 1 })
	if tb.ed.pending != pending || !tb.grid.Table.ShowHeaderColumn {
		t.Error("a sort reads the rows again; their edits should stay")
	}
}

func TestATableWithNoKeyHasNoChangesToShow(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "nokey", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	if tb.ed.pending != nil || tb.grid.Table.ShowHeaderColumn || fx.s.canInsert() {
		t.Error("rows that cannot be told apart are never edited, so nothing is marked or added")
	}
}
