package grid

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

func TestDraggingATitlesEdgeSetsItsColumnsWidth(t *testing.T) {
	g := selectingGrid(t)
	h := g.createHeader().(*columnHeader)
	g.updateHeader(widget.TableCellID{Row: -1, Col: 1}, h)
	before := g.ColumnWidth(1)
	h.handle.Dragged(&fyne.DragEvent{Dragged: fyne.Delta{DX: 30}})
	h.handle.DragEnd()
	if got := g.ColumnWidth(1); got != before+30 {
		t.Fatalf("width %v after dragging 30 from %v", got, before)
	}
	g.MoveColumn(1, -1)
	if got := g.ColumnWidth(1); got != before+30 {
		t.Errorf("a moved column should keep its width: %v, want %v", got, before+30)
	}
	h.handle.Dragged(&fyne.DragEvent{Dragged: fyne.Delta{DX: -10000}})
	if got := g.ColumnWidth(1); got != minColumnWidth {
		t.Errorf("width %v; a column goes no narrower than %v", got, minColumnWidth)
	}
	if h.handle.Cursor() != desktop.HResizeCursor || g.table.Cursor() != desktop.DefaultCursor {
		t.Error("the resize cursor is the handle's, not the gap between titles")
	}
}

func TestEveryHeaderHasAHandleOnItsRightEdge(t *testing.T) {
	g := selectingGrid(t)
	for _, filterable := range []bool{false, true} {
		g.SetFilterable(filterable)
		h := g.createHeader().(*columnHeader)
		h.Resize(fyne.NewSize(120, 60))
		test := h.CreateRenderer()
		test.Layout(fyne.NewSize(120, 60))
		if p, s := h.handle.Position(), h.handle.Size(); p.X != 120-handleWidth || s.Height != 60 {
			t.Errorf("filterable %v: handle at %v, size %v", filterable, p, s)
		}
	}
}
