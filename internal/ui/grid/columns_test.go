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
