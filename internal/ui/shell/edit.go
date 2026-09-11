package shell

import "github.com/ikigai-db/ikigai-db/internal/ui/grid"

// Editing a table's cells and rows (FR-4.1, FR-4.2, ADR-0029, ADR-0030).
// The grid edits in place and hands each value to the tab's pending changes;
// nothing is written to the server until the changes are committed (T2.6).

// canEditCell reports whether the active tab's active cell can be edited.
func (s *Shell) canEditCell() bool {
	t := s.activeTab()
	return t != nil && t.pending != nil && t.grid != nil && t.grid.CanEditCell()
}

// canInsert reports whether the active tab's table takes new rows.
func (s *Shell) canInsert() bool {
	t := s.activeTab()
	return t != nil && t.pending != nil && t.grid != nil
}

// canChangeRows reports whether the active tab edits and has cells selected.
func (s *Shell) canChangeRows() bool {
	return s.canInsert() && !s.activeTab().grid.Selection().Empty()
}

// setNull sets every selected cell of the rows loaded, and of the new rows,
// to NULL. A column that cannot hold NULL is left as it is, and the footer
// says so; so is a row to be deleted.
func (s *Shell) setNull() {
	if !s.canChangeRows() {
		return
	}
	t := s.activeTab()
	g, cols := t.grid, t.model.Columns()
	sel := g.Selection()
	problem := ""
	for _, vc := range sel.Columns() {
		mc := g.ColumnAt(vc)
		if mc < 0 || mc >= len(cols) {
			continue
		}
		if !cols[mc].Type.Nullable {
			problem = cols[mc].Name + " cannot be NULL"
			continue
		}
		for _, r := range selectedRows(t) {
			if !sel.Contains(r, vc) {
				continue
			}
			row, _ := t.model.Row(t.ctx, int64(r))
			if row == nil {
				continue
			}
			if err := g.SetValue(r, row, mc, nil); err != nil && problem == "" {
				problem = err.Error()
			}
		}
	}
	t.said = problem
	g.Table.Refresh()
	s.showCount(t)
}

// selectedRows lists, in order, the grid's rows with a cell selected, among
// the new rows and the rows loaded: a whole column reaches past the rows
// loaded.
func selectedRows(t *tab) []int {
	sel := t.grid.Selection()
	first, last := sel.Rows()
	if n, _ := t.model.Extent(); int64(last) >= n {
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
	t := s.activeTab()
	i := t.pending.Add()
	s.showAdded(t)
	t.grid.GoTo(grid.CellID{Row: i, Col: 0})
}

// duplicateRows adds a new row for each row selected, with its values but
// not its key, and selects the first.
func (s *Shell) duplicateRows() {
	if !s.canChangeRows() {
		return
	}
	t := s.activeTab()
	first := -1
	for _, r := range selectedRows(t) {
		row, _ := t.model.Row(t.ctx, int64(r))
		if row == nil {
			continue
		}
		if i := t.pending.Duplicate(row); first < 0 {
			first = i
		}
	}
	s.showAdded(t)
	if first >= 0 {
		t.grid.GoTo(grid.CellID{Row: first, Col: 0})
	}
}

// deleteRows marks each row read that is selected to be deleted, and takes
// out the new rows selected, which nothing had written.
func (s *Shell) deleteRows() {
	if !s.canChangeRows() {
		return
	}
	t := s.activeTab()
	rows, k := selectedRows(t), t.model.Added()
	for i := len(rows) - 1; i >= 0; i-- { // from the last, so a new row's place holds
		r := rows[i]
		if r < k {
			t.pending.RemoveAdded(r)
			continue
		}
		if row, _ := t.model.Row(t.ctx, int64(r)); row != nil {
			t.pending.Delete(row)
		}
	}
	s.showAdded(t)
}

// showAdded puts the pending new rows in the grid and counts the changes.
func (s *Shell) showAdded(t *tab) {
	t.model.SetAdded(t.pending.Added())
	t.grid.Table.Refresh()
	s.showCount(t)
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
