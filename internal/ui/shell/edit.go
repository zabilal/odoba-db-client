package shell

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// Editing a grid's cells and rows (FR-4.1, FR-4.2, ADR-0029, ADR-0030).
// The grid edits in place and hands each value to its pending changes
// (edits.go); nothing is written to the server until the changes are
// committed (commit.go).

// canEditCell reports whether the active cell of the grid in front can be
// edited.
func (s *Shell) canEditCell() bool {
	e := s.activeEdits()
	return e != nil && e.pending != nil && e.grid.CanEditCell()
}

// canInsert reports whether the grid in front takes new rows.
func (s *Shell) canInsert() bool {
	e := s.activeEdits()
	return e != nil && e.pending != nil
}

// canChangeRows reports whether the grid in front edits and has cells
// selected.
func (s *Shell) canChangeRows() bool {
	return s.canInsert() && !s.activeEdits().grid.Selection().Empty()
}

// setNull sets every selected cell to NULL, in the rows not loaded too
// (ADR-0038). A column that cannot hold NULL is left as it is, and it is
// said; so is a row to be deleted.
func (s *Shell) setNull() {
	if s.canChangeRows() {
		s.fill(s.activeEdits(), null, nulling)
	}
}

// setValue asks for one value, and writes it into every selected cell as
// each column reads it (FR-4.11, ADR-0038).
func (s *Shell) setValue() {
	if !s.canChangeRows() {
		return
	}
	e := s.activeEdits()
	entry := widget.NewEntry()
	entry.SetPlaceHolder("Empty is NULL, or empty text in a column of text")
	note := widget.NewLabel("Written into each selected cell, read as its column's type. " +
		"Nothing reaches the server until the changes are committed.")
	note.Wrapping = fyne.TextWrapWord
	d := dialog.NewForm("Set Value", "Set", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Value", entry), widget.NewFormItem("", note)},
		func(ok bool) {
			if ok {
				s.fill(e, parsed(entry.Text), setting)
			}
		}, s.win)
	d.Resize(fyne.NewSize(440, 220))
	d.Show()
	s.win.Canvas().Focus(entry)
}

// selectedRows lists, in order, the grid's rows with a cell selected, among
// the new rows and the rows loaded: a whole column reaches past the rows
// loaded.
func selectedRows(e *edits) []int {
	sel := e.grid.Selection()
	first, last := sel.Rows()
	if n, _ := e.model.Extent(); int64(last) >= n {
		last = int(n) - 1
	}
	var out []int
	for r := first; r <= last; r++ {
		if sel.HasRow(r) {
			out = append(out, r)
		}
	}
	return out
}

// insertRow adds a new row, after any others, before the rows read, and
// selects its first cell to type into (FR-4.2).
func (s *Shell) insertRow() {
	if !s.canInsert() {
		return
	}
	e := s.activeEdits()
	i := e.pending.Add()
	e.showAdded()
	e.grid.GoTo(grid.CellID{Row: i, Col: 0})
}

// duplicateRows adds a new row for each row selected, with its values but
// not its key, and selects the first.
func (s *Shell) duplicateRows() {
	if !s.canChangeRows() {
		return
	}
	e := s.activeEdits()
	first := -1
	for _, r := range selectedRows(e) {
		row, _ := e.model.Row(e.ctx, int64(r))
		if row == nil {
			continue
		}
		if i := e.pending.Duplicate(row); first < 0 {
			first = i
		}
	}
	e.showAdded()
	if first >= 0 {
		e.grid.GoTo(grid.CellID{Row: first, Col: 0})
	}
}

// deleteRows marks each row read that is selected to be deleted, and takes
// out the new rows selected, which nothing had written.
func (s *Shell) deleteRows() {
	if !s.canChangeRows() {
		return
	}
	e := s.activeEdits()
	rows, k := selectedRows(e), e.model.Added()
	for i := len(rows) - 1; i >= 0; i-- { // from the last, so a new row's place holds
		r := rows[i]
		if r < k {
			e.pending.RemoveAdded(r)
			continue
		}
		if row, _ := e.model.Row(e.ctx, int64(r)); row != nil {
			e.pending.Delete(row)
		}
	}
	e.showAdded()
}

// editInViewer opens the active cell's value at length in the cell viewer,
// opening the viewer if it is not open.
func (s *Shell) editInViewer() {
	t, g := s.activeTab(), s.activeGrid()
	if t == nil || g == nil || t.holders[g] == nil {
		return
	}
	if v := t.viewers[g]; v == nil || !v.shown() {
		s.toggleViewerFor(t, g)
	}
	t.viewers[g].beginEdit()
}

// pendingText words how many rows have changes not yet committed.
func pendingText(n int) string {
	if n == 1 {
		return "1 pending change"
	}
	return group(int64(n)) + " pending changes"
}
