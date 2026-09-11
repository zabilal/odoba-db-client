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
		if holds(shown.Content, g.View()) || holds(shown.Content, t.holders[g]) {
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

// copyJob is what a copy needs besides the rows: the selection, the row the
// read began at, the columns, and which of them the selection covers.
type copyJob struct {
	sel    grid.Selection
	first  int
	cols   []model.ColumnDef
	picked []int
}

// copySelection reads the rows a selection reaches and puts what render
// makes of them on the clipboard (FR-3.7). Rows that were never drawn are
// read off the UI goroutine; a selection reaching past copyLimit rows is
// refused. as is added to the report, such as " as CSV".
func (s *Shell) copySelection(ctx context.Context, g *grid.TableGrid, as string, render func(copyJob, []model.Row) (string, int, error)) {
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
			if err == nil && int64(len(rows)) > copyLimit {
				s.status.SetText(fmt.Sprintf("Copy takes up to %s rows, and this selection has more. Export it instead.", group(copyLimit)))
				return
			}
			var text string
			var n int
			if err == nil {
				text, n, err = render(copyJob{sel, first, cols, picked}, rows)
			}
			if err != nil {
				s.status.SetText("Could not copy: " + err.Error())
				return
			}
			s.app.Clipboard().SetContent(text)
			s.status.SetText("Copied " + describeCopy(n, len(picked)) + as)
		})
	}()
}

// copyCells puts exactly the selected cells on the clipboard as
// tab-separated text, the way spreadsheets paste: each row that has a
// selected cell, in order, and in each the columns any part of the selection
// covers, with the cells outside it left empty. One cell is copied as its
// text alone.
func (s *Shell) copyCells(ctx context.Context, g *grid.TableGrid) {
	s.copySelection(ctx, g, "", func(j copyJob, rows []model.Row) (string, int, error) {
		out, defs := j.rows(rows, true)
		if len(out) == 1 && len(defs) == 1 {
			return export.Text(out[0][0], defs[0]), 1, nil
		}
		return written(defs, out, export.Options{Format: export.TSV})
	})
}

// copyAs puts the rows that have a selected cell on the clipboard in a
// format, with a header. Unlike ⌘C it writes every cell of those rows in the
// columns the selection covers: CSV, JSON and Markdown describe rows, and a
// blank where the selection skipped would say something false about one.
func (s *Shell) copyAs(ctx context.Context, g *grid.TableGrid, f export.Format) {
	s.copySelection(ctx, g, " as "+f.String(), func(j copyJob, rows []model.Row) (string, int, error) {
		out, defs := j.rows(rows, false)
		return written(defs, out, export.Options{Format: f, Header: true})
	})
}

func (s *Shell) copyActiveAs(f export.Format) {
	if t, g := s.activeTab(), s.activeGrid(); t != nil && g != nil {
		s.copyAs(t.ctx, g, f)
	}
}

// copyInsert puts the rows that have a selected cell on the clipboard as
// INSERT statements into the table they came from, rows whole as Copy As
// writes them. The dialect writes the values (ARCH-2).
func (s *Shell) copyInsert() {
	t, g := s.activeTab(), s.activeGrid()
	if t == nil || g == nil || !s.canCopyInsert() {
		return
	}
	bs := t.browse
	s.copySelection(t.ctx, g, " as INSERT", func(j copyJob, rows []model.Row) (string, int, error) {
		out, defs := j.rows(rows, false)
		text, err := bs.InsertRows(defs, out)
		return strings.TrimSuffix(text, "\n"), len(out), err
	})
}

// canCopyInsert reports whether the selection can be copied as INSERT: rows
// of a table, from a source whose dialect writes them. A query's result has
// no one table to insert into.
func (s *Shell) canCopyInsert() bool {
	t := s.activeTab()
	return s.hasSelection() && t != nil && t.query == nil && t.browse != nil && t.browse.CanScriptRows()
}

func (s *Shell) copyCSV()      { s.copyActiveAs(export.CSV) }
func (s *Shell) copyJSON()     { s.copyActiveAs(export.JSON) }
func (s *Shell) copyMarkdown() { s.copyActiveAs(export.Markdown) }

// rows keeps, of rows read from the job's first row on, those that have a
// selected cell, in the picked columns. With blanks, the cells outside the
// selection are left empty.
func (j copyJob) rows(rows []model.Row, blanks bool) ([]model.Row, []model.ColumnDef) {
	var out []model.Row
	for i, r := range rows {
		row := j.first + i
		if !j.sel.HasRow(row) {
			continue
		}
		o := make(model.Row, len(j.picked))
		for k, c := range j.picked {
			if c < len(r) && (!blanks || j.sel.Contains(row, c)) {
				o[k] = r[c]
			}
		}
		out = append(out, o)
	}
	defs := make([]model.ColumnDef, len(j.picked))
	for k, c := range j.picked {
		defs[k] = j.cols[c]
	}
	return out, defs
}

// written formats rows, without the final line break a clipboard does not
// want, and says how many rows it wrote.
func written(defs []model.ColumnDef, rows []model.Row, opt export.Options) (string, int, error) {
	var b strings.Builder
	if err := export.Write(&b, defs, rows, opt); err != nil {
		return "", 0, err
	}
	return strings.TrimSuffix(b.String(), "\n"), len(rows), nil
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
