package shell

import (
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Editing rows by their key (FR-4.7, ADR-0034). A table's rows are edited
// by the key its source reports. A table with none says why its rows are
// not edited, and a key can be chosen for it from its columns.

// noKeyText is what a table with no key says in its footer.
const noKeyText = "These rows have no key to tell them apart, so they are not edited: Choose a Key… names one."

// startEditing lets a table tab edit its rows, told apart by id. The edits
// are kept by each row's key, so they stay through a sort or a filter. A
// read-only connection edits nothing (FR-1.8). Rows that cannot be told
// apart are not edited; a table's footer says so.
func (s *Shell) startEditing(t *tab, id model.RowIdentity) {
	if _, readOnly := s.envOf(t); readOnly {
		return
	}
	p, err := app.NewPending(t.browse.Columns(), id)
	if err != nil {
		if t.ref.Kind == model.KindTable {
			t.said = noKeyText
		}
		return
	}
	g, m := t.grid, t.model
	t.pending = p
	g.SetChanges(p)
	g.OnEdit = func(row model.Row, col int, v any) error {
		if err := p.Set(row, col, v); err != nil {
			return err
		}
		s.showCount(t)
		return nil
	}
	g.OnEditAdded = func(i, col int, v any) error {
		if err := p.SetAdded(i, col, v); err != nil {
			return err
		}
		m.SetAdded(p.Added())
		s.showCount(t)
		return nil
	}
}

// canChooseKey reports whether the active tab is a table whose rows have no
// key to be edited by, on a connection that edits.
func (s *Shell) canChooseKey() bool {
	t := s.activeTab()
	if t == nil || t.browse == nil || t.pending != nil || t.ref.Kind != model.KindTable {
		return false
	}
	_, readOnly := s.envOf(t)
	return !readOnly
}

// chooseKey asks which columns tell the rows apart, and edits them by those.
func (s *Shell) chooseKey() {
	if !s.canChooseKey() {
		return
	}
	t := s.activeTab()
	var names []string
	for _, c := range t.browse.Columns() {
		names = append(names, c.Name)
	}
	pick := widget.NewCheckGroup(names, nil)
	note := widget.NewLabel("Each change is written to the row with these columns' values. " +
		"A change that would write more than one row is refused, and nothing is written.")
	note.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustomConfirm("Choose a Key", "Edit by These", "Cancel",
		container.NewBorder(note, nil, nil, nil, container.NewVScroll(pick)), func(ok bool) {
			if ok {
				s.useKey(t, pick.Selected)
			}
		}, s.win)
	d.Resize(fyne.NewSize(420, 360))
	d.Show()
}

// useKey edits a tab's rows by the columns chosen, in the table's order.
func (s *Shell) useKey(t *tab, chosen []string) {
	var key []string
	for _, c := range t.browse.Columns() {
		if slices.Contains(chosen, c.Name) {
			key = append(key, c.Name)
		}
	}
	if len(key) == 0 {
		t.said = "No key chosen: the rows are not edited."
		s.showCount(t)
		return
	}
	s.startEditing(t, model.RowIdentity{Kind: model.IdentityChosen, Columns: key, Target: t.ref})
	if t.pending != nil {
		t.said = "Edited by " + strings.Join(key, ", ") + ", the key chosen"
	}
	s.showCount(t)
}
