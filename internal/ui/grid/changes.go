package grid

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Changes are a table's edits not yet written, as the grid shows them
// (FR-4.3, ADR-0028). app.Pending is one.
type Changes interface {
	// State is how a row read stands.
	State(row model.Row) model.RowState
	// Value is a cell's new value, if it has one. col is the model's.
	Value(row model.Row, col int) (any, bool)
}

// gutterWidth is the column before the first, where each changed row is
// marked.
const gutterWidth float32 = 20

// SetChanges shows a table's changes not yet written, or none when c is
// nil. A changed cell shows its new value, bold on the modified tint, and
// says what it was when the pointer rests on it. A deleted row is struck
// through on the deleted tint, and a new row is on the added tint.
//
// Colour is never the only sign. Each changed row is also marked in a
// gutter before the first column, a dot when changed, a minus when deleted
// and a plus when new, and the mark says what it means in words. The gutter
// is there whenever the grid has changes to show, so the first edit moves
// nothing.
func (g *TableGrid) SetChanges(c Changes) {
	g.changes = c
	g.Table.ShowHeaderColumn = c != nil
	g.Table.SetColumnWidth(-1, gutterWidth)
	g.fitFiller()
	g.Table.Refresh()
}

// rowAt is a row and how it stands. A row still loading or past the end,
// which the model gives as nil, or any row of a grid with no changes to
// show, is unchanged.
func (g *TableGrid) rowAt(r int) (model.Row, bool, model.RowState) {
	row, loaded := g.model.Row(g.ctx, int64(r))
	if g.changes == nil || row == nil {
		return row, loaded, model.RowUnchanged
	}
	return row, loaded, g.changes.State(row)
}

// look is how a row that stands so is drawn: its text, its tint, and its
// mark in the gutter. An unchanged row has no look of its own.
func (g *TableGrid) look(s model.RowState) (fg, bg color.Color, mark string) {
	switch s {
	case model.RowModified:
		return g.palette.ModifiedFg, g.palette.ModifiedBg, "•"
	case model.RowDeleted:
		return g.palette.DeletedFg, g.palette.DeletedBg, "−"
	case model.RowAdded:
		return g.palette.AddedFg, g.palette.AddedBg, "+"
	}
	return g.palette.SecondaryLabel, g.palette.SidebarBackground, ""
}

// said is a row's state in words, for its mark in the gutter.
func (g *TableGrid) said(row model.Row, s model.RowState) string {
	switch s {
	case model.RowModified:
		n := 0
		for mc := range g.model.Columns() {
			if _, ok := g.changes.Value(row, mc); ok {
				n++
			}
		}
		if n == 1 {
			return "1 cell changed"
		}
		return fmt.Sprintf("%d cells changed", n)
	case model.RowDeleted:
		return deletedHint
	case model.RowAdded:
		return addedHint
	}
	return ""
}

const (
	deletedHint = "To be deleted"
	addedHint   = "New row"
)

// updateGutter marks a row in the gutter. Fyne makes the gutter's cells
// from the same template as the column headers, and moves them between the
// two, so it sets everything a column header would.
func (g *TableGrid) updateGutter(r int, h *columnHeader) {
	row, _, state := g.rowAt(r)
	fg, bg, mark := g.look(state)
	if h.bg.FillColor != bg {
		h.bg.FillColor = bg
		h.bg.Refresh()
	}
	h.title.col, h.handle.col = -1, -1
	h.title.hint = g.said(row, state)
	h.title.set(mark, fg, bg, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	h.handle.Hide()
	if h.field != nil {
		h.field.Hide()
	}
}
