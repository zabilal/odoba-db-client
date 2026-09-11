package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// Saved queries (FR-5.9, T1.67): ⌘S keeps a script in the local database
// under a name and an optional folder, and ⇧⌘O finds it again.

func (s *Shell) canSave() bool {
	_, q := s.activeQuery()
	return q != nil && s.d.Saved != nil
}

// saveQuery saves the active query tab: in place if it is already saved,
// else after asking for a name.
func (s *Shell) saveQuery() {
	t, q := s.activeQuery()
	if q == nil || s.d.Saved == nil {
		return
	}
	if q.saved.ID == "" {
		s.promptSave(t, false)
		return
	}
	sq := q.saved
	sq.Body, sq.ConnectionID = q.editor.Document().Text(), t.connID
	s.storeQuery(t, sq)
}

// savePrompt is the Save Query sheet.
type savePrompt struct {
	dlg          *dialog.FormDialog
	name, folder *widget.Entry
}

// promptSave asks for a name and folder, then saves. asCopy saves a new
// query rather than renaming the one the tab edits.
func (s *Shell) promptSave(t *tab, asCopy bool) *savePrompt {
	q := t.query
	p := &savePrompt{name: widget.NewEntry(), folder: widget.NewEntry()}
	p.name.SetText(q.saved.Name)
	if asCopy && q.saved.Name != "" {
		p.name.SetText(q.saved.Name + " copy")
	}
	p.name.SetPlaceHolder("Monthly revenue")
	p.name.Validator = func(v string) error {
		if strings.TrimSpace(v) == "" {
			return formError("Enter a name.")
		}
		return nil
	}
	p.folder.SetText(q.saved.Folder)
	p.folder.SetPlaceHolder("None")
	title := "Save Query"
	if asCopy {
		title = "Save Query As"
	}
	p.dlg = dialog.NewForm(title, "Save", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Name", p.name), widget.NewFormItem("Folder", p.folder)},
		func(ok bool) {
			if !ok {
				return
			}
			sq := q.saved
			if asCopy {
				sq.ID = ""
			}
			sq.Name, sq.Folder = strings.TrimSpace(p.name.Text), strings.TrimSpace(p.folder.Text)
			sq.ConnectionID, sq.Body = t.connID, q.editor.Document().Text()
			s.storeQuery(t, sq)
		}, s.win)
	p.dlg.Resize(fyne.NewSize(420, p.dlg.MinSize().Height))
	p.dlg.Show()
	s.win.Canvas().Focus(p.name)
	return p
}

// storeQuery writes a saved query off the UI goroutine, then binds the tab
// to it.
func (s *Shell) storeQuery(t *tab, sq localdb.SavedQuery) {
	q := t.query
	rev := q.editor.Document().Revision()
	go func() {
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		defer cancel()
		got, err := s.d.Saved.SaveQuery(ctx, sq)
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			switch {
			case errors.Is(err, localdb.ErrNameTaken):
				where := "with no folder"
				if sq.Folder != "" {
					where = "in “" + sq.Folder + "”"
				}
				s.showError(formError(fmt.Sprintf(
					"A saved query named “%s” already exists %s. Choose another name.", sq.Name, where)))
				return
			case err != nil:
				s.showError(err)
				return
			}
			q.saved, q.title = got, got.Name
			q.dirty = q.editor.Document().Revision() != rev // edits made while saving are not in it
			s.retitle(t)
			s.keep(t) // forgotten, unless edits made while saving need keeping
			t.footer.SetText(fmt.Sprintf("Saved “%s”", got.Name))
		})
	}()
}

// retitle names a query tab, marking unsaved edits the way a macOS document
// window does.
func (s *Shell) retitle(t *tab) {
	q := t.query
	text := q.title
	if q.dirty {
		text += " •"
	}
	if t.item.Text != text {
		t.item.Text = text
		s.tabs.Refresh()
	}
}

// requestClose closes a tab the user asked to close, first asking if it has
// edits that are saved nowhere. Closes the app makes itself, as when a
// connection is deleted, do not ask.
func (s *Shell) requestClose(it *container.TabItem) {
	var t *tab
	for _, o := range s.open {
		if o.item == it {
			t = o
		}
	}
	if t == nil || t.query == nil || !t.query.dirty || strings.TrimSpace(t.query.editor.Document().Text()) == "" {
		s.closeTab(it)
		return
	}
	d := dialog.NewConfirm("Close Without Saving?",
		fmt.Sprintf("“%s” has changes that are not saved.", t.query.title),
		func(yes bool) {
			if yes {
				s.closeTab(it)
			}
		}, s.win)
	d.SetConfirmText("Close")
	d.SetDismissText("Cancel")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}

func (s *Shell) tabForSaved(id string) *tab {
	for _, t := range s.open {
		if t.query != nil && t.query.saved.ID == id {
			return t
		}
	}
	return nil
}

// savedPanel lists saved queries, filtered as you type.
type savedPanel struct {
	s      *Shell
	filter *widget.Entry
	list   *widget.List
	status *widget.Label
	all    []localdb.SavedQuery
	shown  []localdb.SavedQuery
	loaded bool
	dlg    *dialog.CustomDialog
	now    func() time.Time
}

func (s *Shell) showSaved() *savedPanel {
	if s.d.Saved == nil {
		return nil
	}
	p := &savedPanel{s: s, filter: widget.NewEntry(), status: widget.NewLabel(""), now: time.Now}
	p.filter.SetPlaceHolder("Filter by name, folder or text")
	p.status.Importance = widget.LowImportance
	p.list = widget.NewList(
		func() int { return len(p.shown) },
		func() fyne.CanvasObject {
			name := widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
			name.Truncation = fyne.TextTruncateEllipsis
			meta := widget.NewLabel("")
			meta.Importance = widget.LowImportance
			meta.Truncation = fyne.TextTruncateEllipsis
			del := widget.NewButtonWithIcon("", fynetheme.DeleteIcon(), nil)
			del.Importance = widget.LowImportance
			return container.NewBorder(nil, nil, nil, del, container.NewVBox(name, meta))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i >= len(p.shown) {
				return
			}
			sq, row := p.shown[i], o.(*fyne.Container)
			box, del := row.Objects[0].(*fyne.Container), row.Objects[1].(*widget.Button)
			title := sq.Name
			if sq.Folder != "" {
				title = sq.Folder + " / " + sq.Name
			}
			box.Objects[0].(*widget.Label).SetText(title)
			box.Objects[1].(*widget.Label).SetText(p.describe(sq))
			del.OnTapped = func() { p.confirmDelete(sq) }
		})
	p.list.OnSelected = func(i widget.ListItemID) {
		if i < len(p.shown) {
			p.open(p.shown[i])
		}
	}
	p.filter.OnChanged = func(string) { p.apply() }
	p.filter.OnSubmitted = func(string) {
		if len(p.shown) > 0 {
			p.open(p.shown[0])
		}
	}
	p.dlg = dialog.NewCustom("Saved Queries", "Close", container.NewBorder(p.filter, p.status, nil, nil, p.list), s.win)
	p.dlg.Resize(fyne.NewSize(640, 440))
	p.dlg.Show()
	s.win.Canvas().Focus(p.filter)
	p.load()
	return p
}

func (p *savedPanel) load() {
	go func() {
		ctx, cancel := context.WithTimeout(p.s.ctx, 5*time.Second)
		defer cancel()
		all, err := p.s.d.Saved.SavedQueries(ctx)
		p.s.d.Run(func() {
			if err != nil {
				p.status.SetText("Could not read saved queries: " + err.Error())
				return
			}
			p.all, p.loaded = all, true
			p.apply()
		})
	}()
}

// apply filters by every word typed, across folder, name and text.
func (p *savedPanel) apply() {
	words := strings.Fields(strings.ToLower(p.filter.Text))
	p.shown = p.shown[:0]
	for _, sq := range p.all {
		hay := strings.ToLower(sq.Folder + " " + sq.Name + " " + sq.Body)
		match := true
		for _, w := range words {
			match = match && strings.Contains(hay, w)
		}
		if match {
			p.shown = append(p.shown, sq)
		}
	}
	p.list.UnselectAll()
	p.list.Refresh()
	c, _ := p.s.reg.Get(cmdQuerySave)
	switch {
	case !p.loaded:
	case len(p.all) == 0:
		p.status.SetText(fmt.Sprintf("No saved queries yet. Save one from a query tab with %s.", c.Shortcut.Label(p.s.d.GOOS)))
	case len(p.shown) == 0:
		p.status.SetText("No saved queries match.")
	case len(p.shown) == 1:
		p.status.SetText("1 saved query")
	default:
		p.status.SetText(fmt.Sprintf("%d saved queries", len(p.shown)))
	}
}

func (p *savedPanel) describe(sq localdb.SavedQuery) string {
	conn := "a deleted connection"
	if c, ok := p.s.d.Conns.Get(sq.ConnectionID); ok {
		conn = c.Name
	}
	return strings.Join([]string{conn, "edited " + ago(sq.UpdatedAt, p.now()), firstLine(sq.Body)}, " · ")
}

// open reopens a saved query on its connection, or brings its tab forward
// if it is already open.
func (p *savedPanel) open(sq localdb.SavedQuery) {
	if t := p.s.tabForSaved(sq.ID); t != nil {
		p.dlg.Hide()
		p.s.tabs.Select(t.item)
		return
	}
	if _, ok := p.s.d.Conns.Get(sq.ConnectionID); !ok {
		p.status.SetText("The connection this query was saved with no longer exists.")
		p.list.UnselectAll()
		return
	}
	p.dlg.Hide()
	t := p.s.OpenQuery(sq.ConnectionID)
	if t == nil {
		return
	}
	q := t.query
	q.editor.Document().SetText(sq.Body)
	q.saved, q.title, q.dirty = sq, sq.Name, false
	p.s.retitle(t)
	q.editor.Refresh()
}

func (p *savedPanel) confirmDelete(sq localdb.SavedQuery) {
	d := dialog.NewConfirm(fmt.Sprintf("Delete “%s”?", sq.Name),
		"The saved query is removed. A tab that has it open keeps its text.",
		func(yes bool) {
			if !yes {
				return
			}
			go func() {
				ctx, cancel := context.WithTimeout(p.s.ctx, 5*time.Second)
				defer cancel()
				err := p.s.d.Saved.DeleteQuery(ctx, sq.ID)
				p.s.d.Run(func() {
					if err != nil {
						// The panel covers the window, so its own line says it.
						p.status.SetText("Could not delete “" + sq.Name + "”: " + err.Error())
						return
					}
					if t := p.s.tabForSaved(sq.ID); t != nil {
						t.query.saved, t.query.dirty = localdb.SavedQuery{}, true
						p.s.retitle(t)
						p.s.keep(t) // its text is now saved nowhere else
					}
					p.load()
				})
			}()
		}, p.s.win)
	d.SetConfirmText("Delete")
	d.SetConfirmImportance(widget.DangerImportance)
	d.Show()
}
