package shell

import (
	"fmt"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// Reverting pending changes (FR-4.6, ADR-0033): a cell, a row, or all of
// them; and asking before changes not committed are lost to a tab closing
// or the app quitting.

// canRevert reports whether the active tab has changes, cells selected,
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
	t := s.activeTab()
	g, sel, k := t.grid, t.grid.Selection(), t.model.Added()
	for _, vc := range sel.Columns() {
		mc := g.ColumnAt(vc)
		if mc < 0 {
			continue
		}
		for _, r := range selectedRows(t) {
			if !sel.Contains(r, vc) {
				continue
			}
			if r < k {
				t.pending.UnsetAdded(r, mc)
				continue
			}
			if row, _ := t.model.Row(t.ctx, int64(r)); row != nil {
				t.pending.RevertCell(row, mc)
			}
		}
	}
	s.showAdded(t)
}

// revertRows undoes every change to each selected row. A row read is as it
// was read, a deletion undone too; a new row goes, from the last so that
// each keeps its place until it goes.
func (s *Shell) revertRows() {
	if !s.canRevert() {
		return
	}
	t := s.activeTab()
	rows, k := selectedRows(t), t.model.Added()
	for i := len(rows) - 1; i >= 0; i-- {
		r := rows[i]
		if r < k {
			t.pending.RemoveAdded(r)
			continue
		}
		if row, _ := t.model.Row(t.ctx, int64(r)); row != nil {
			t.pending.RevertRow(row)
		}
	}
	s.showAdded(t)
}

// discardAll asks, then undoes every change in the tab, the new rows too.
func (s *Shell) discardAll() {
	if !s.canReview() {
		return
	}
	t := s.activeTab()
	n := t.pending.Len()
	d := dialog.NewConfirm("Discard All Changes?", discardText(n), func(yes bool) {
		if yes {
			t.pending.RevertAll()
			t.said = "Discarded " + changesText(n)
			s.showAdded(t)
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

// pendingIn is how many changes not committed a tab holds.
func pendingIn(t *tab) int {
	if t.pending == nil {
		return 0
	}
	return t.pending.Len()
}

// pendingAll is how many changes not committed the open tabs hold.
func (s *Shell) pendingAll() int {
	n := 0
	for _, t := range s.open {
		n += pendingIn(t)
	}
	return n
}
