package shell

import (
	"testing"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

func TestTheCellViewerShowsTheActiveCellsWholeValue(t *testing.T) {
	fx, tb := openItems(t)
	tb.grid.Select(grid.CellID{Row: 3, Col: 1}, grid.CellID{Row: 3, Col: 1})
	c, ok := fx.s.reg.Get(cmdCellViewer)
	if !ok || !c.Enabled() {
		t.Fatal("a grid can show its cell viewer")
	}
	c.Run()
	v := tb.viewers[tb.grid]
	if v == nil || !v.shown() {
		t.Fatal("the viewer did not open")
	}
	if v.title.Text != "name" || v.text.Text != "item 3" {
		t.Errorf("viewer shows %q: %q", v.title.Text, v.text.Text)
	}
	tb.grid.Select(grid.CellID{Row: 7, Col: 0}, grid.CellID{Row: 7, Col: 0})
	if v.title.Text != "id" || v.text.Text != "7" {
		t.Errorf("the viewer should follow the selection; shows %q: %q", v.title.Text, v.text.Text)
	}
	v.copyValue()
	if got := fx.s.app.Clipboard().Content(); got != "7" {
		t.Errorf("Copy Value copied %q", got)
	}
	c.Run()
	if v.shown() || tb.body.Objects[0] != fyne.CanvasObject(tb.grid.View()) {
		t.Error("toggled again, the viewer should close and give the grid its room back")
	}
}

func TestSpaceOnTheGridTogglesTheViewer(t *testing.T) {
	_, tb := openItems(t)
	tb.grid.Select(grid.CellID{Row: 0, Col: 1}, grid.CellID{Row: 0, Col: 1})
	if tb.grid.OnSpace == nil {
		t.Fatal("Space on the grid should open the viewer")
	}
	tb.grid.OnSpace()
	if v := tb.viewers[tb.grid]; v == nil || !v.shown() || v.text.Text != "item 0" {
		t.Error("Space should open the viewer on the selected cell")
	}
	tb.grid.OnSpace()
	if tb.viewers[tb.grid].shown() {
		t.Error("Space again should close it")
	}
}
