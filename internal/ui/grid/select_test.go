package grid

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func selectingGrid(t *testing.T) *TableGrid {
	t.Helper()
	g := sortableGrid(t)
	w := test.NewTempWindow(t, g.View())
	w.Resize(fyne.NewSize(600, 400))
	return g
}

// click is a click with modifiers held: the mouse-down, then Fyne's tap.
func click(g *TableGrid, row, col int, mods fyne.KeyModifier) {
	g.table.MouseDown(&desktop.MouseEvent{Modifier: mods})
	g.table.Select(widget.TableCellID{Row: row, Col: col})
}

func TestClicksSelectBlocksAndCells(t *testing.T) {
	g := selectingGrid(t)
	heard := 0
	g.OnSelectCell = func() { heard++ }
	click(g, 1, 1, 0)
	click(g, 3, 2, fyne.KeyModifierShift)
	if s := g.Selection(); !s.Contains(2, 2) || !s.Contains(1, 1) || s.Contains(0, 1) {
		t.Error("⇧-click should select the block from 1,1 to 3,2")
	}
	click(g, 5, 0, fyne.KeyModifierShortcutDefault)
	if s := g.Selection(); !s.Contains(5, 0) || !s.Contains(2, 2) {
		t.Error("⌘-click should add a cell and keep the block")
	}
	click(g, 5, 0, fyne.KeyModifierShortcutDefault)
	if g.Selection().Contains(5, 0) {
		t.Error("⌘-clicking the same cell again should take it out: the second click must be heard")
	}
	click(g, 4, 2, 0)
	if s := g.Selection(); s.Contains(1, 1) || !s.Contains(4, 2) {
		t.Error("a plain click selects just that cell")
	}
	if heard != 5 || g.SelectedColumn() != 2 {
		t.Errorf("heard %d changes, want 5; active column %d", heard, g.SelectedColumn())
	}
}

func TestShiftArrowsStretchTheSelection(t *testing.T) {
	g := selectingGrid(t)
	click(g, 1, 1, 0)
	g.table.KeyDown(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	g.table.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	g.table.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	g.table.KeyUp(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	if s := g.Selection(); !s.Contains(2, 2) || !s.Contains(1, 1) {
		t.Error("⇧-arrows should stretch the block")
	}
	g.table.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	if s := g.Selection(); s.Contains(1, 1) || !s.Contains(3, 2) {
		t.Error("an arrow alone moves the selection")
	}
}

func TestTheFocusedGridAnswersCopyAndSelectAll(t *testing.T) {
	g := selectingGrid(t)
	copied := 0
	g.OnCopy = func() { copied++ }
	g.table.TypedShortcut(&fyne.ShortcutSelectAll{})
	last := len(g.model.Columns()) - 1
	if s := g.Selection(); !s.Contains(99, 0) || !s.Contains(0, last) || s.Contains(0, last+1) {
		t.Error("⌘A should select every cell")
	}
	g.table.TypedShortcut(&fyne.ShortcutCopy{})
	if copied != 1 {
		t.Errorf("⌘C asked to copy %d times, want once", copied)
	}
}

func TestSelectedCellsAreTinted(t *testing.T) {
	g := selectingGrid(t)
	click(g, 2, 1, 0)
	cell := newCellWidget()
	g.UpdateCell(widget.TableCellID{Row: 2, Col: 1}, cell)
	if cell.bg != g.palette.SelectedUnemphasized {
		t.Error("a selected cell should be tinted")
	}
	g.UpdateCell(widget.TableCellID{Row: 2, Col: 0}, cell)
	if cell.bg == g.palette.SelectedUnemphasized {
		t.Error("the cell beside it is not selected")
	}
}

// The first screenshot of a ⇧-click showed one cell: the view held Fyne's
// Table, so the driver's clicks went round the grid's.
func TestClicksReachTheGridsOwnTable(t *testing.T) {
	g := selectingGrid(t)
	var held []fyne.CanvasObject
	if box, ok := g.View().(*fyne.Container); ok {
		held = box.Objects
	}
	for _, o := range held {
		if o == fyne.CanvasObject(g.table) {
			return
		}
	}
	t.Error("the grid's view does not hold the grid's own table, so clicks bypass it")
}
