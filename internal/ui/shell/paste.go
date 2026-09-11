package shell

import (
	"errors"
	"fmt"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// Pasting a block of cells (FR-4.10, ADR-0037). The clipboard's text is read
// as rows of cells (grid.ParseBlock) and written from the active cell,
// rightwards over the columns as shown, each cell as its column's type, as
// pending changes: nothing reaches the server before Commit.

// errPastLastColumn and errPastLastRow say why a cell of a block was not
// pasted: the grid ends before it.
var (
	errPastLastColumn = errors.New("past the last column")
	errPastLastRow    = errors.New("past the last row")
)

// canPaste reports whether the grid in front takes a paste: its rows are
// edited, and it has a cell to paste at.
func (s *Shell) canPaste() bool { return s.canChangeRows() }

func (s *Shell) pasteActive() { s.paste(s.activeEdits()) }

// paste writes the clipboard's text into e's grid. One value over several
// cells selected is written into each. A block goes from the active cell:
// among the new rows if it begins on one, else among the rows read.
func (s *Shell) paste(e *edits) {
	if e == nil || e.pending == nil {
		return
	}
	sel := e.grid.Selection()
	at, ok := sel.Active()
	if !ok {
		return
	}
	block := grid.ParseBlock(s.app.Clipboard().Content())
	switch {
	case len(block) == 0:
		e.say("Nothing to paste: the clipboard holds no text")
		e.show()
	case len(block) == 1 && len(block[0]) == 1 && !oneCell(sel):
		e.sayPasted(fill(e, block[0][0]))
	case int64(len(block)) > copyLimit:
		e.say(fmt.Sprintf("Paste takes up to %s rows, and the clipboard has more.", group(copyLimit)))
		e.show()
	case at.Row < e.model.Added():
		e.sayPasted(pasteNew(e, at, block))
	default:
		s.pasteRead(e, at, block)
	}
}

// oneCell reports whether a selection is a single cell.
func oneCell(sel grid.Selection) bool {
	first, last := sel.Rows()
	return first == last && len(sel.Columns()) == 1
}

// fill writes one value into every selected cell of the new rows and the
// rows loaded, as Set to NULL writes NULL.
func fill(e *edits, text string) pasted {
	g, cols := e.grid, e.model.Columns()
	sel := g.Selection()
	var p pasted
	for _, vc := range sel.Columns() {
		for _, r := range selectedRows(e) {
			if !sel.Contains(r, vc) {
				continue
			}
			if row, _ := e.model.Row(e.ctx, int64(r)); row != nil {
				p.write(cols, g.ColumnAt(vc), text, setter(e, r, row))
			}
		}
	}
	return p
}

// pasteNew writes a block into the new rows from the active one down,
// adding new rows as it needs, so that it never reaches the rows read. An
// empty cell leaves a new row's column as it is: not given, for the
// server's default, unless it was.
func pasteNew(e *edits, at grid.CellID, block [][]string) pasted {
	for range at.Row + len(block) - e.model.Added() {
		e.pending.Add()
	}
	e.model.SetAdded(e.pending.Added())
	cols := e.model.Columns()
	var p pasted
	for i, line := range block {
		for j, text := range line {
			mc := e.grid.ColumnAt(at.Col + j)
			if text == "" && mc >= 0 {
				p.count(1, nil)
				continue
			}
			p.write(cols, mc, text, setter(e, at.Row+i, nil))
		}
	}
	selectBlock(e.grid, at, len(block), width(block))
	return p
}

// pasteRead writes a block into the rows read from the active one down,
// reading those not loaded off the UI goroutine. What reaches past the last
// row is not pasted: rows are added only among the new ones.
func (s *Shell) pasteRead(e *edits, at grid.CellID, block [][]string) {
	e.say("Pasting…")
	e.show()
	m := e.model
	go func() {
		rows, err := m.Read(e.ctx, int64(at.Row), int64(at.Row+len(block)))
		s.d.Run(func() {
			switch {
			case e.ctx.Err() != nil:
			case err != nil:
				e.say("Could not paste: " + err.Error())
				e.show()
			default:
				e.sayPasted(pasteRows(e, at, block, rows))
			}
		})
	}()
}

// pasteRows writes a block into rows read, the first at the active cell.
func pasteRows(e *edits, at grid.CellID, block [][]string, rows []model.Row) pasted {
	cols := e.model.Columns()
	var p pasted
	for i, line := range block {
		if i >= len(rows) {
			p.count(len(line), errPastLastRow)
			continue
		}
		row := rows[i]
		set := func(col int, v any) error { return e.pending.Set(row, col, v) }
		for j, text := range line {
			p.write(cols, e.grid.ColumnAt(at.Col+j), text, set)
		}
	}
	selectBlock(e.grid, at, min(len(block), len(rows)), width(block))
	return p
}

// setter writes into grid row r of e, a new row or the row read there, as a
// pending change.
func setter(e *edits, r int, row model.Row) func(col int, v any) error {
	if r < e.model.Added() {
		return func(col int, v any) error { return e.pending.SetAdded(r, col, v) }
	}
	return func(col int, v any) error { return e.pending.Set(row, col, v) }
}

// pasted counts the cells a paste reached and those it wrote, and keeps why
// the first it did not write was not.
type pasted struct {
	cells, written int
	why            error
}

// write writes text into model column mc, as the column's type, through
// set. mc is -1 past the last column shown.
func (p *pasted) write(cols []model.ColumnDef, mc int, text string, set func(int, any) error) {
	err := errPastLastColumn
	if mc >= 0 && mc < len(cols) {
		err = pasteCell(cols[mc], mc, text, set)
	}
	p.count(1, err)
}

// count counts n cells, written unless err says why not.
func (p *pasted) count(n int, err error) {
	p.cells += n
	switch {
	case err == nil:
		p.written += n
	case p.why == nil:
		p.why = err
	}
}

// pasteCell reads text as a column's type, as typing it would be, and
// writes it.
func pasteCell(col model.ColumnDef, mc int, text string, set func(int, any) error) error {
	if !grid.Editable(col) {
		return fmt.Errorf("%s: its values are not typed", col.Name)
	}
	v, err := grid.Parse(text, col, time.Local)
	if err == nil {
		err = set(mc, v)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", col.Name, err)
	}
	return nil
}

// width is the most cells any row of a block has.
func width(block [][]string) int {
	n := 0
	for _, line := range block {
		n = max(n, len(line))
	}
	return n
}

// selectBlock selects the cells a block of rows by cols was pasted into,
// from at, as far as the grid's columns reach.
func selectBlock(g *grid.TableGrid, at grid.CellID, rows, cols int) {
	if rows == 0 {
		return
	}
	last := min(at.Col+cols, len(g.Shown())) - 1
	g.Select(at, grid.CellID{Row: at.Row + rows - 1, Col: last})
}

// sayPasted says how many cells a paste wrote, and why the first it did not
// write was not, and shows the changes.
func (e *edits) sayPasted(p pasted) {
	if p.written == p.cells {
		e.say("Pasted " + nounCount(p.cells, "cell"))
	} else {
		e.say(fmt.Sprintf("Pasted %s of %s; the first not pasted: %v", group(int64(p.written)), nounCount(p.cells, "cell"), p.why))
	}
	e.showAdded()
}
