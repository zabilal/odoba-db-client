package shell

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
)

// statusWidth holds the longest count the bar shows, "5,000+ matches".
const statusWidth = 120

// findLimit caps how many matches are counted and outlined. Past a few
// thousand, "5,000+ matches" says all there is to say, and outlining more
// would cost frames.
const findLimit = 5000

// findBar is a query tab's find and replace bar (FR-5.11, T1.60). It sits
// above the editor, and the current match is the editor's selection.
type findBar struct {
	s       *Shell
	q       *queryTab
	box     *fyne.Container
	find    *findEntry
	with    *widget.Entry
	replace *fyne.Container
	match   *widget.Check
	word    *widget.Check
	regex   *widget.Check
	status  *widget.Label
	// parent holds the bar and the editor. Only it can re-divide their room
	// when the bar appears or goes: laid out while hidden, the bar kept a
	// zero width, and showing it gave its fields negative ones — unusable,
	// and a crash in the software renderer.
	parent *fyne.Container
}

// findEntry hands Return and Escape to the bar, as a find field should.
type findEntry struct {
	widget.Entry
	bar *findBar
}

func (e *findEntry) TypedKey(k *fyne.KeyEvent) {
	switch k.Name {
	case fyne.KeyEscape:
		e.bar.hide()
	case fyne.KeyReturn, fyne.KeyEnter:
		e.bar.next(false)
	default:
		e.Entry.TypedKey(k)
	}
}

func newFindBar(s *Shell, q *queryTab) *findBar {
	f := &findBar{s: s, q: q, with: widget.NewEntry(), status: widget.NewLabel("")}
	f.find = &findEntry{bar: f}
	f.find.ExtendBaseWidget(f.find)
	f.find.SetPlaceHolder("Find")
	f.find.OnChanged = func(string) { f.refresh() }
	f.with.SetPlaceHolder("Replace with")
	f.match = widget.NewCheck("Match Case", func(bool) { f.refresh() })
	f.word = widget.NewCheck("Whole Words", func(bool) { f.refresh() })
	f.regex = widget.NewCheck("Regular Expression", func(bool) { f.refresh() })
	f.status.Importance = widget.LowImportance
	f.status.Alignment = fyne.TextAlignTrailing
	// A fixed width for the count: a label that grows does not make its
	// container re-lay out, so "4 matches" was drawn clipped to "4" — and a
	// width that followed the text would shift the buttons as it changed.
	count := container.New(layout.NewGridWrapLayout(fyne.NewSize(statusWidth, f.status.MinSize().Height)), f.status)

	prev := widget.NewButton("Previous", func() { f.next(true) })
	next := widget.NewButton("Next", func() { f.next(false) })
	done := widget.NewButton("Done", f.hide)
	findRow := container.NewBorder(nil, nil, nil, container.NewHBox(count, prev, next, done), f.find)
	f.replace = container.NewBorder(nil, nil, nil, container.NewHBox(
		widget.NewButton("Replace", f.replaceOne), widget.NewButton("Replace All", func() { f.replaceAll() })), f.with)
	f.box = container.NewVBox(findRow, f.replace, container.NewHBox(f.match, f.word, f.regex), widget.NewSeparator())
	f.box.Hide()
	return f
}

func (s *Shell) hasQuery() bool {
	_, q := s.activeQuery()
	return q != nil
}

func (s *Shell) withFind(fn func(*findBar)) {
	if _, q := s.activeQuery(); q != nil {
		fn(q.find)
	}
}

func (f *findBar) visible() bool { return f.box.Visible() }

func (f *findBar) search() editor.Search {
	return editor.Search{Text: f.find.Text, Regex: f.regex.Checked, Case: f.match.Checked, Word: f.word.Checked}
}

// show opens the bar, with the replace row when asked, seeded with the
// selection if it is a single line, as macOS's "Use Selection for Find".
func (f *findBar) show(replace bool) {
	f.box.Show()
	if replace {
		f.replace.Show()
	} else {
		f.replace.Hide()
	}
	doc := f.q.editor.Document()
	if from, to, sel := doc.Selection(); sel && from.Line == to.Line {
		f.find.SetText(doc.SelectedText())
	}
	f.relayout()
	if c := fyne.CurrentApp().Driver().CanvasForObject(f.find); c != nil {
		c.Focus(f.find)
	}
	f.refresh()
}

func (f *findBar) relayout() {
	if f.parent != nil {
		f.parent.Refresh()
		return
	}
	f.box.Refresh()
}

func (f *findBar) hide() {
	f.box.Hide()
	f.relayout()
	f.q.editor.SetMatches(nil)
	f.q.editor.Focus()
}

// refresh finds the matches again and says where the selection is among
// them.
func (f *findBar) refresh() {
	doc := f.q.editor.Document()
	f.status.Importance = widget.LowImportance
	if f.find.Text == "" {
		f.q.editor.SetMatches(nil)
		f.status.SetText("")
		return
	}
	ms, err := doc.FindAll(f.search(), findLimit)
	if err != nil {
		f.q.editor.SetMatches(nil)
		f.status.Importance = widget.DangerImportance
		f.status.SetText("Invalid pattern")
		return
	}
	f.q.editor.SetMatches(ms)
	from, to, _ := doc.Selection()
	current := 0
	for i, m := range ms {
		if m.From == from && m.To == to {
			current = i + 1
			break
		}
	}
	switch {
	case len(ms) == 0:
		f.status.SetText("No matches")
	case len(ms) >= findLimit:
		f.status.SetText(fmt.Sprintf("%s+ matches", group(findLimit)))
	case current > 0:
		f.status.SetText(fmt.Sprintf("%d of %d", current, len(ms)))
	case len(ms) == 1:
		f.status.SetText("1 match")
	default:
		f.status.SetText(fmt.Sprintf("%d matches", len(ms)))
	}
}

// next selects the next match, or the previous one when backward.
func (f *findBar) next(backward bool) {
	if f.find.Text == "" {
		f.show(false)
		return
	}
	if ok, _ := f.q.editor.Document().FindNext(f.search(), backward); ok {
		f.q.editor.Reveal()
	}
	f.refresh()
}

func (f *findBar) replaceOne() {
	f.edit(func(doc *editor.Document) { doc.ReplaceSelection(f.search(), f.with.Text) })
}

func (f *findBar) replaceAll() int {
	var n int
	f.edit(func(doc *editor.Document) { n, _ = doc.ReplaceAll(f.search(), f.with.Text) })
	if n > 0 {
		f.status.Importance = widget.LowImportance
		f.status.SetText(fmt.Sprintf("Replaced %d", n))
	}
	return n
}

// edit changes the document directly, so it reports the edit to the tab
// itself: the editor's OnChanged fires only for its own input.
func (f *findBar) edit(fn func(*editor.Document)) {
	doc := f.q.editor.Document()
	rev := doc.Revision()
	fn(doc)
	f.q.editor.Reveal()
	if doc.Revision() != rev && f.q.editor.OnChanged != nil {
		f.q.editor.OnChanged()
	} else {
		f.refresh()
	}
}
