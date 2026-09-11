package grid

import (
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

const (
	// handleWidth is how wide the grab area at a title's right edge is.
	handleWidth = float32(6)
	// minColumnWidth is as narrow as a column goes.
	minColumnWidth = float32(40)
)

// resizeHandle is the right edge of a column's title. Dragging it sets the
// column's width, which the grid keeps for that column wherever it moves
// (FR-3.2). It draws nothing: the table draws the dividers.
type resizeHandle struct {
	widget.BaseWidget
	g   *TableGrid
	col int // the model's
}

func newResizeHandle(g *TableGrid) *resizeHandle {
	h := &resizeHandle{g: g, col: -1}
	h.ExtendBaseWidget(h)
	return h
}

func (h *resizeHandle) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(canvas.NewRectangle(color.Transparent))
}

func (h *resizeHandle) Cursor() desktop.Cursor { return desktop.HResizeCursor }

func (h *resizeHandle) Dragged(e *fyne.DragEvent) {
	if h.col >= 0 {
		h.g.ResizeColumn(h.col, h.g.ColumnWidth(h.col)+e.Dragged.DX)
	}
}

func (h *resizeHandle) DragEnd() {}

// ColumnWidth is a model column's width.
func (g *TableGrid) ColumnWidth(col int) float32 {
	if col < 0 || col >= len(g.widths) {
		return 0
	}
	return g.widths[col]
}

// ResizeColumn sets a model column's width, no narrower than minColumnWidth.
// The grid keeps it, so the column has it wherever it is moved.
func (g *TableGrid) ResizeColumn(col int, width float32) {
	if col < 0 || col >= len(g.widths) {
		return
	}
	g.widths[col] = max(width, minColumnWidth)
	if dc := slices.Index(g.order, col); dc >= 0 {
		g.Table.SetColumnWidth(dc, g.widths[col])
	}
	g.fitFiller()
	if g.OnLayout != nil {
		g.OnLayout()
	}
}

// Resized reports whether a model column has a width a person gave it,
// rather than the one its type starts it with.
func (g *TableGrid) Resized(col int) bool {
	cols := g.model.Columns()
	return col >= 0 && col < len(g.widths) && col < len(cols) && g.widths[col] != defaultWidth(cols[col])
}
