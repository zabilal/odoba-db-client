package shell

// Editing a table's cells (FR-4.1, ADR-0029). The grid edits in place and
// hands each value to the tab's pending changes; nothing is written to the
// server until the changes are committed (T2.6).

// canEditCell reports whether the active tab's active cell can be edited.
func (s *Shell) canEditCell() bool {
	t := s.activeTab()
	return t != nil && t.pending != nil && t.grid != nil && t.grid.CanEditCell()
}

// canSetNull reports whether the active tab edits and has cells selected.
func (s *Shell) canSetNull() bool {
	t := s.activeTab()
	return t != nil && t.pending != nil && t.grid != nil && !t.grid.Selection().Empty()
}

// setNull sets every selected cell of the rows loaded to NULL. A column that
// cannot hold NULL is left as it is, and the footer says so; so is a row to
// be deleted.
func (s *Shell) setNull() {
	t := s.activeTab()
	if t == nil || t.pending == nil || t.grid == nil {
		return
	}
	g, cols := t.grid, t.model.Columns()
	sel := g.Selection()
	first, last := sel.Rows()
	if n, _ := t.model.Extent(); int64(last) >= n {
		last = int(n) - 1 // a whole column reaches past the rows loaded
	}
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
		for r := first; r <= last; r++ {
			if !sel.Contains(r, vc) {
				continue
			}
			row, _ := t.model.Row(t.ctx, int64(r))
			if row == nil {
				continue
			}
			if err := t.pending.Set(row, mc, nil); err != nil && problem == "" {
				problem = err.Error()
			}
		}
	}
	t.said = problem
	g.Table.Refresh()
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
