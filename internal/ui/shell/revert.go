package shell

import (
	"fmt"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// Reverting pending changes (FR-4.6, ADR-0033): a cell, a row, or all of
// them; and asking before changes not committed are lost to a tab closing
// or the app quitting.

// canRevert reports whether the grid in front has changes, cells selected,
// and is not committing.
func (s *Shell) canRevert() bool {
	return s.canChangeRows() && s.canReview()
}

// revertCells undoes the change to each selected cell. A row read's cell
// shows what was read again; a new row's column goes back to not given.
func (s *Shell) revertCells() {
	if !s.canRevert() {
		return
	}
	e := s.activeEdits()
	g, sel, k := e.grid, e.grid.Selection(), e.model.Added()
	for _, vc := range sel.Columns() {
		mc := g.ColumnAt(vc)
		if mc < 0 {
			continue
		}
		for _, r := range selectedRows(e) {
			if !sel.Contains(r, vc) {
				continue
			}
			if r < k {
				e.pending.UnsetAdded(r, mc)
				continue
			}
			if row, _ := e.model.Row(e.ctx, int64(r)); row != nil {
				e.pending.RevertCell(row, mc)
			}
		}
	}
	e.showAdded()
}

// revertRows undoes every change to each selected row. A row read is as it
// was read, a deletion undone too; a new row goes, from the last so that
// each keeps its place until it goes.
func (s *Shell) revertRows() {
	if !s.canRevert() {
		return
	}
	e := s.activeEdits()
	rows, k := selectedRows(e), e.model.Added()
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if r < k {
			e.pending.RemoveAdded(r)
			continue
		}
		if row, _ := e.model.Row(e.ctx, int64(r)); row != nil {
			e.pending.RevertRow(row)
		}
	}
	e.showAdded()
}

// discardAll asks, then undoes every change in the grid, the new rows too.
func (s *Shell) discardAll() {
	if !s.canReview() {
		return
	}
	e := s.activeEdits()
	n := e.pending.Len()
	d := dialog.NewConfirm("Discard All Changes?", discardText(n), func(yes bool) {
		if yes {
			e.pending.RevertAll()
			e.say("Discarded " + changesText(n))
			e.showAdded()
		}
	}, s.win)
	d.SetConfirmText("Discard")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}

// discardText says how many changes not committed would be lost.
func discardText(n int) string {
	return fmt.Sprintf("%s not committed will be lost.", changesText(n))
}

// pendingIn is how many changes not committed a tab holds: a table's, or
// its query's results'.
func pendingIn(t *tab) int {
	n := t.ed.changes()
	if q := t.query; q != nil {
		n += resultChanges(q)
	}
	return n
}

// pendingAll is how many changes not committed the open tabs hold.
func (s *Shell) pendingAll() int {
	n := 0
	for _, t := range s.open {
		n += pendingIn(t)
	}
	return n
}
