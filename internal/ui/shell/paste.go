package shell

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
	"github.com/ikigai-db/ikigai-db/internal/value"
)

// Pasting a block of cells (FR-4.10, ADR-0037), and one value written into
// every selected cell (FR-4.11, ADR-0038). The clipboard's text is read as
// rows of cells (grid.ParseBlock) and written from the active cell,
// rightwards over the columns as shown, each cell as its column's type, as
// pending changes: nothing reaches the server before Commit.

// errPastLastColumn and errPastLastRow say why a cell of a block was not
// pasted: the grid ends before it.
var (
	errPastLastColumn = errors.New("past the last column")
	errPastLastRow    = errors.New("past the last row")
)

// doing is what writes cells, for what it says: its name, what it says while
// it reads rows, and what it did.
type doing struct{ name, ing, ed string }

var (
	pasting = doing{"Paste", "Pasting…", "Pasted"}
	setting = doing{"Set Value", "Setting…", "Set"}
	nulling = doing{"Set to NULL", "Setting…", "Set"}
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
		s.fill(e, parsed(block[0][0]), pasting)
	case int64(len(block)) > copyLimit:
		e.say(fmt.Sprintf("Paste takes up to %s rows, and the clipboard has more.", group(copyLimit)))
		e.show()
	case at.Row < e.model.Added():
		e.sayDone(pasting, pasteNew(e, at, block))
	default:
		s.pasteRead(e, at, block)
	}
}

// oneCell reports whether a selection is a single cell.
func oneCell(sel grid.Selection) bool {
	first, last := sel.Rows()
	return first == last && len(sel.Columns()) == 1
}

// fill writes one value into every selected cell: what each column makes of
// it. It reads the rows the selection reaches off the UI goroutine, those
// not loaded too, so that a whole column is filled and not only the rows
// drawn; up to as many rows as Copy takes (ADR-0038).
func (s *Shell) fill(e *edits, value func(model.ColumnDef) (any, error), d doing) {
	sel := e.grid.Selection()
	first, last := sel.Rows()
	to := int64(last) + 1
	if last == grid.End || to > int64(first)+copyLimit {
		to = int64(first) + copyLimit + 1 // one past the limit, to know it was passed
	}
	e.say(d.ing)
	e.show()
	m := e.model
	go func() {
		rows, err := m.Read(e.ctx, int64(first), to)
		s.d.Run(func() {
			switch {
			case e.ctx.Err() != nil:
			case err != nil:
				e.say("Could not read the rows: " + err.Error())
				e.show()
			case int64(len(rows)) > copyLimit:
				e.say(fmt.Sprintf("%s takes up to %s rows, and the selection has more.", d.name, group(copyLimit)))
				e.show()
			default:
				e.sayDone(d, fillRows(e, sel, first, rows, value))
			}
		})
	}()
}

// fillRows writes a value into the cells of sel among rows, which were read
// from grid row first on.
func fillRows(e *edits, sel grid.Selection, first int, rows []model.Row, value func(model.ColumnDef) (any, error)) pasted {
	g, cols := e.grid, e.model.Columns()
	var p pasted
	for _, vc := range sel.Columns() {
		mc := g.ColumnAt(vc)
		for i, row := range rows {
			if r := first + i; sel.Contains(r, vc) {
				p.write(cols, mc, value, setter(e, r, row))
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
			p.write(cols, mc, parsed(text), setter(e, at.Row+i, nil))
		}
	}
	selectBlock(e.grid, at, len(block), width(block))
	return p
}

// pasteRead writes a block into the rows read from the active one down,
// reading those not loaded off the UI goroutine. What reaches past the last
// row is not pasted: rows are added only among the new ones.
func (s *Shell) pasteRead(e *edits, at grid.CellID, block [][]string) {
	e.say(pasting.ing)
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
				e.sayDone(pasting, pasteRows(e, at, block, rows))
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
			p.write(cols, e.grid.ColumnAt(at.Col+j), parsed(text), set)
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

// pasted counts the cells a paste or a fill reached and those it wrote, and
// keeps why the first it did not write was not.
type pasted struct {
	cells, written int
	why            error
}

// write writes the value model column mc makes of it, through set. mc is -1
// past the last column shown.
func (p *pasted) write(cols []model.ColumnDef, mc int, value func(model.ColumnDef) (any, error), set func(int, any) error) {
	err := errPastLastColumn
	if mc >= 0 && mc < len(cols) {
		err = writeCell(cols[mc], mc, value, set)
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

// writeCell writes the value a column makes of it into the column, through
// set, and names the column in what goes wrong.
func writeCell(col model.ColumnDef, mc int, value func(model.ColumnDef) (any, error), set func(int, any) error) error {
	v, err := value(col)
	if err == nil {
		err = set(mc, v)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", col.Name, err)
	}
	return nil
}

// parsed is text as each column reads it, as typing it would be.
func parsed(text string) func(model.ColumnDef) (any, error) {
	return func(col model.ColumnDef) (any, error) {
		if !value.Editable(col) {
			return nil, errors.New("its values are not typed")
		}
		return value.Parse(text, col, time.Local)
	}
}

// null is NULL, in a column that can hold it.
func null(col model.ColumnDef) (any, error) {
	if !col.Type.Nullable {
		return nil, errors.New("cannot be NULL")
	}
	return nil, nil
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

// sayDone says how many cells d wrote, and why the first it did not write
// was not, and shows the changes.
func (e *edits) sayDone(d doing, p pasted) {
	if p.written == p.cells {
		e.say(d.ed + " " + nounCount(p.cells, "cell"))
	} else {
		e.say(fmt.Sprintf("%s %s of %s; the first not %s: %v", d.ed, group(int64(p.written)),
			nounCount(p.cells, "cell"), strings.ToLower(d.ed), p.why))
	}
	e.showAdded()
}
