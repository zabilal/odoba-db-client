package grid

import (
	"reflect"
	"testing"

	"fyne.io/fyne/v2/widget"
)

func TestColumnsHideMoveAndFreeze(t *testing.T) {
	g := selectingGrid(t)
	cols := g.model.Columns()
	n := len(cols)
	title := func(display int) string {
		h := g.createHeader().(*columnHeader).title
		g.updateHeader(widget.TableCellID{Row: -1, Col: display}, h)
		return h.text
	}
	g.MoveColumn(2, -2)
	if got := g.Shown()[:3]; !reflect.DeepEqual(got, []int{2, 0, 1}) {
		t.Fatalf("moved 2 to the front: shown %v", got)
	}
	if got := title(0); got != cols[2].Name {
		t.Errorf("the first header says %q, want %q", got, cols[2].Name)
	}
	click(g, 1, 0, 0)
	if g.SelectedColumn() != 2 {
		t.Errorf("the active cell is in model column %d, want 2", g.SelectedColumn())
	}
	g.MoveColumn(2, 1)
	if got := g.Shown()[:3]; !reflect.DeepEqual(got, []int{0, 2, 1}) || g.SelectedColumn() != 2 {
		t.Errorf("moved right: shown %v, active column %d; the active cell goes with its column", got, g.SelectedColumn())
	}
	g.HideColumn(0)
	if c := len(g.Shown()); c != n-1 || !g.Hidden() || g.Selection().Contains(1, 0) {
		t.Errorf("hid a column: %d shown of %d, hidden %v; the selection should clear", c, n, g.Hidden())
	}
	g.ShowAllColumns()
	if got := g.Shown()[:3]; !reflect.DeepEqual(got, []int{0, 2, 1}) || g.Hidden() {
		t.Errorf("shown again: %v; a hidden column comes back after the one before it in the model", got)
	}
	g.FreezeThrough(2)
	if g.Frozen() != 2 || g.Table.StickyColumnCount != 2 {
		t.Errorf("frozen through the second shown column: %d, sticky %d", g.Frozen(), g.Table.StickyColumnCount)
	}
	g.Unfreeze()
	if g.Frozen() != 0 || g.Table.StickyColumnCount != 0 {
		t.Error("unfrozen, every column scrolls")
	}
	for _, c := range g.Shown() {
		g.HideColumn(c)
	}
	if len(g.Shown()) != 1 {
		t.Errorf("%d columns shown after hiding them all; the last one stays", len(g.Shown()))
	}
}

func TestSetLayoutPutsALayoutBack(t *testing.T) {
	g := selectingGrid(t)
	n := len(g.model.Columns())
	g.SetLayout([]int{2, 0, 2, -1, n}, 5)
	if got := g.Shown(); !reflect.DeepEqual(got, []int{2, 0}) || g.Frozen() != 2 {
		t.Errorf("shown %v, frozen %d: repeats and columns out of range are skipped, and no more are frozen than shown",
			got, g.Frozen())
	}
	before := g.Shown()
	g.SetLayout([]int{-1, n}, 0)
	if got := g.Shown(); !reflect.DeepEqual(got, before) {
		t.Errorf("a layout naming no real column changed the grid: %v", got)
	}
	if g.Resized(0) {
		t.Error("a column no one resized should not claim a width a person gave it")
	}
	g.ResizeColumn(0, g.ColumnWidth(0)+30)
	if !g.Resized(0) {
		t.Error("a resized column should say so")
	}
}

func TestLayoutChangesAreReported(t *testing.T) {
	g := selectingGrid(t)
	n := 0
	g.OnLayout = func() { n++ }
	g.ResizeColumn(0, 150)
	g.MoveColumn(1, -1)
	g.HideColumn(2)
	g.FreezeThrough(0)
	if n < 4 {
		t.Errorf("four layout changes reported %d times", n)
	}
}
