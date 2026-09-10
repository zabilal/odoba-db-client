package grid

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
)

// headerCell is a column header: a cell that sorts the grid when tapped.
// Fyne's tap event carries no modifier keys, so MouseDown notes Shift for it.
type headerCell struct {
	cellWidget
	g     *TableGrid
	col   int
	shift bool
}

func newHeaderCell(g *TableGrid) *headerCell {
	h := &headerCell{g: g}
	h.fg, h.bg = color.Black, color.Transparent
	h.ExtendBaseWidget(h)
	return h
}

func (h *headerCell) MouseDown(ev *desktop.MouseEvent) {
	h.shift = ev.Modifier&fyne.KeyModifierShift != 0
}

func (h *headerCell) MouseUp(*desktop.MouseEvent) {}

func (h *headerCell) Tapped(*fyne.PointEvent) {
	h.g.ToggleSort(h.col, h.shift)
	h.shift = false
}

func (h *headerCell) Cursor() desktop.Cursor {
	if h.g.Sortable {
		return desktop.PointerCursor
	}
	return desktop.DefaultCursor
}

// TappedSecondary asks for the column's list of values, where the grid
// offers one (OnPickValues).
func (h *headerCell) TappedSecondary(*fyne.PointEvent) {
	if h.g.OnPickValues != nil && h.col >= 0 {
		h.g.OnPickValues(h.col)
	}
}
