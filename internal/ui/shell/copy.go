package shell

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// copyLimit is the most rows Copy puts on the clipboard. More belongs in a
// file: Export streams, and a clipboard holds everything at once.
var copyLimit int64 = 100_000

// activeGrid is the grid in front: a table's rows, or the query result shown.
func (s *Shell) activeGrid() *grid.TableGrid {
	t := s.activeTab()
	switch {
	case t == nil:
		return nil
	case t.query == nil:
		return t.grid
	}
	shown := t.query.results.Selected()
	if shown == nil {
		return nil
	}
	for _, g := range t.query.grids {
		if holds(shown.Content, g.View()) {
			return g
		}
	}
	return nil
}

// holds reports whether o is c, or one of c's children.
func holds(c, o fyne.CanvasObject) bool {
	if c == o {
		return true
	}
	box, ok := c.(*fyne.Container)
	return ok && slices.Contains(box.Objects, o)
}

func (s *Shell) hasSelection() bool {
	g := s.activeGrid()
	return g != nil && !g.Selection().Empty()
}

func (s *Shell) onGrid(do func(*grid.TableGrid)) {
	if g := s.activeGrid(); g != nil {
		do(g)
	}
}

func (s *Shell) copyActive() {
	if t, g := s.activeTab(), s.activeGrid(); t != nil && g != nil {
		s.copyCells(t.ctx, g)
	}
}

// copyCells puts the selected cells on the clipboard as tab-separated text,
// the way spreadsheets paste (FR-3.7): each row that has a selected cell, in
// order, and in each the columns any part of the selection covers, with the
// cells outside it left empty. One cell is copied as its text alone. Rows
// that were never drawn are read for the copy, off the UI goroutine.
func (s *Shell) copyCells(ctx context.Context, g *grid.TableGrid) {
	sel := g.Selection()
	if sel.Empty() {
		return
	}
	m := g.Model()
	cols := m.Columns()
	var picked []int
	for _, c := range sel.Columns() {
		if c < len(cols) {
			picked = append(picked, c)
		}
	}
	first, last := sel.Rows()
	to := int64(last) + 1
	if last == grid.End || to > int64(first)+copyLimit {
		to = int64(first) + copyLimit + 1 // one past the limit, to know it was passed
	}
	s.status.SetText("Copying…")
	go func() {
		rows, err := m.Read(ctx, int64(first), to)
		s.d.Run(func() {
			if ctx.Err() != nil {
				return
			}
			switch {
			case err != nil:
				s.status.SetText("Could not copy: " + err.Error())
			case int64(len(rows)) > copyLimit:
				s.status.SetText(fmt.Sprintf("Copy takes up to %s rows, and this selection has more. Export it instead.", group(copyLimit)))
			default:
				text, n := cellText(sel, first, rows, cols, picked)
				s.app.Clipboard().SetContent(text)
				s.status.SetText("Copied " + describeCopy(n, len(picked)))
			}
		})
	}()
}

// cellText writes the selected cells of rows, the first of which is row
// first, and says how many rows it wrote.
func cellText(sel grid.Selection, first int, rows []model.Row, cols []model.ColumnDef, picked []int) (string, int) {
	var out []model.Row
	for i, r := range rows {
		row := first + i
		if !sel.HasRow(row) {
			continue
		}
		o := make(model.Row, len(picked))
		for j, c := range picked {
			if sel.Contains(row, c) && c < len(r) {
				o[j] = r[c]
			}
		}
		out = append(out, o)
	}
	if len(out) == 1 && len(picked) == 1 {
		return export.Text(out[0][0], cols[picked[0]]), 1
	}
	defs := make([]model.ColumnDef, len(picked))
	for j, c := range picked {
		defs[j] = cols[c]
	}
	var b strings.Builder
	_ = export.Write(&b, defs, out, export.Options{Format: export.TSV})
	return strings.TrimSuffix(b.String(), "\n"), len(out)
}

func describeCopy(rows, cols int) string {
	if rows == 1 && cols == 1 {
		return "1 cell"
	}
	return nounCount(rows, "row") + " × " + nounCount(cols, "column")
}

func nounCount(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return group(int64(n)) + " " + noun + "s"
}
