package shell

import (
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// whereBar is a browse's WHERE clause, typed by the person, above the grid,
// with the statement the grid runs shown under it (FR-3.6, UX principle 6).
// It stays hidden until asked for (UX principle 2).
type whereBar struct {
	s     *Shell
	t     *tab
	entry *whereEntry
	sql   *widget.Label
	note  *widget.Label
	notes fyne.CanvasObject // the note's row, hidden when there is nothing to say
	box   *fyne.Container
}

// whereEntry is the WHERE field: Return applies, Escape hides the bar.
type whereEntry struct {
	widget.Entry
	bar *whereBar
}

func (e *whereEntry) TypedKey(k *fyne.KeyEvent) {
	switch k.Name {
	case fyne.KeyEscape:
		e.bar.hide()
	case fyne.KeyReturn, fyne.KeyEnter:
		e.bar.apply()
	default:
		e.Entry.TypedKey(k)
	}
}

// canWhere reports whether the active tab can take a WHERE clause.
func (s *Shell) canWhere() bool {
	t := s.activeTab()
	return t != nil && t.browse != nil && t.top != nil && t.browse.CanWhere()
}

// toggleWhere shows the active tab's WHERE bar, or hides it.
func (s *Shell) toggleWhere() {
	if !s.canWhere() {
		return
	}
	t := s.activeTab()
	if t.where == nil {
		t.where = s.newWhereBar(t)
	}
	if t.where.shown() {
		t.where.hide()
		return
	}
	t.where.show()
}

func (s *Shell) newWhereBar(t *tab) *whereBar {
	b := &whereBar{s: s, t: t, sql: widget.NewLabel(""), note: widget.NewLabel("")}
	b.entry = &whereEntry{bar: b}
	b.entry.ExtendBaseWidget(b.entry)
	b.entry.TextStyle = fyne.TextStyle{Monospace: true}
	b.entry.SetPlaceHolder("a condition, such as total > 100 AND status = 'paid'")
	b.entry.SetText(t.want.Where)
	// Neither label wraps. A wrapping label cannot know its height before it
	// is laid out, so the bar would claim too little room and spill over the
	// grid. Each line is its own, and a long one scrolls sideways.
	b.sql.TextStyle = fyne.TextStyle{Monospace: true}
	b.sql.Selectable = true
	b.note.Importance = widget.DangerImportance
	b.notes = container.NewHScroll(b.note)
	b.notes.Hide()
	keyword := widget.NewLabelWithStyle("WHERE", fyne.TextAlignLeading, fyne.TextStyle{Monospace: true, Bold: true})
	copyIt := widget.NewButtonWithIcon("Copy", fynetheme.ContentCopyIcon(), b.copySQL)
	copyIt.Importance = widget.LowImportance
	b.box = container.NewVBox(
		container.NewBorder(nil, nil, keyword,
			container.NewHBox(widget.NewButton("Clear", b.clear), widget.NewButton("Apply", b.apply)), b.entry),
		b.notes,
		container.NewBorder(nil, nil, nil, copyIt, container.NewHScroll(b.sql)),
		widget.NewSeparator())
	b.refresh()
	return b
}

func (b *whereBar) shown() bool { return len(b.t.top.Objects) > 0 }

func (b *whereBar) show() {
	b.t.top.Objects = []fyne.CanvasObject{b.box}
	b.refresh()
	b.s.win.Canvas().Focus(b.entry)
}

func (b *whereBar) hide() {
	b.t.top.Objects = nil
	b.relayout()
}

// relayout re-divides the tab between the bar and the grid. Refreshing the
// bar alone would leave it the room it had before, and it would spill over
// the grid; only the tab's frame can give it more.
func (b *whereBar) relayout() {
	b.t.top.Refresh()
	b.t.item.Content.Refresh()
}

// apply browses again with the typed condition. The server judges the SQL:
// one it refuses leaves the rows as they were, and the bar says why.
func (b *whereBar) apply() {
	opt := b.t.want
	opt.Where = strings.TrimSpace(b.entry.Text)
	b.s.rebrowse(b.t, opt, b.t.grid.Sorts(), "Filtering…", "Could not filter: ")
}

func (b *whereBar) clear() {
	b.entry.SetText("")
	b.apply()
}

func (b *whereBar) copySQL() { b.s.win.Clipboard().SetContent(b.sql.Text) }

// refresh shows the statement the grid now runs, and why the last change
// failed, if it did.
func (b *whereBar) refresh() {
	b.sql.SetText("")
	if st, ok := b.t.browse.Statement(); ok {
		b.sql.SetText(showStatement(st))
	}
	if b.t.problem == "" {
		b.notes.Hide()
	} else {
		b.note.SetText(b.t.problem)
		b.notes.Show()
	}
	if b.shown() {
		b.relayout()
	}
}

// browsed brings a tab's WHERE bar up to date after a re-browse.
func (s *Shell) browsed(t *tab) {
	if t.where != nil {
		t.where.refresh()
	}
}

// showStatement writes a statement for reading: its SQL, then the values
// bound to it, in order. The values are shown beside the SQL, not spliced
// into it, because that is how they travel (NFR-S6); nothing runs this text.
func showStatement(st source.Statement) string {
	if len(st.Args) == 0 {
		return st.SQL
	}
	vals := make([]string, len(st.Args))
	for i, a := range st.Args {
		vals[i] = shownValue(a)
	}
	return st.SQL + "\n-- values, in order: " + strings.Join(vals, ", ")
}

func shownValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case string:
		return "'" + strings.ReplaceAll(x, "'", "''") + "'"
	case time.Time:
		return "'" + x.Format(time.RFC3339Nano) + "'"
	case []byte:
		return fmt.Sprintf("<%d bytes>", len(x))
	}
	return fmt.Sprint(v)
}
