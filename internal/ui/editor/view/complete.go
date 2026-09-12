package view

import (
	"fmt"
	"image/color"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Completer answers what can be typed at a cursor, as a rune offset into the
// text. The editor knows no SQL: internal/source/sqlcomplete does the work
// (ADR-0057, ADR-0058) and the shell wires one to each query tab.
type Completer func(text string, cursor int) source.CompletionResult

const (
	// completionRows is how many candidates are shown at once. The rest are
	// reached by moving through them; a popup taller than this covers the
	// text it is meant to help write.
	completionRows = 10

	completionMinWidth = 180
	completionMaxWidth = 460
	completionPad      = 4
	completionGap      = 12 // between a label and what follows it
)

// SetCompleter gives the editor what it offers. Nil turns completion off.
func (e *Editor) SetCompleter(fn Completer) {
	e.completer = fn
	if fn == nil {
		e.comp.dismiss()
	}
}

// Complete offers what can be typed where the caret is, whatever was last
// typed: the Query ▸ Complete command (⌃Space).
func (e *Editor) Complete() { e.comp.query() }

// CompletionOpen reports whether the completion popup is showing.
func (e *Editor) CompletionOpen() bool { return e.comp.Visible() }

// completions is the popup: the candidates, which of them is chosen, and the
// text an accepted one replaces.
//
// It is drawn inside the editor rather than as a canvas overlay. An overlay
// takes the canvas's focus manager with it, which would stop the editor
// underneath receiving the very keystrokes completion is there to help with.
type completions struct {
	widget.BaseWidget

	e     *Editor
	items []source.Completion
	sel   int
	top   int // the first row drawn, so the selection stays in view

	// start and end are byte offsets into the document: what an accepted
	// candidate replaces.
	start, end int
}

func newCompletions(e *Editor) *completions {
	c := &completions{e: e}
	c.ExtendBaseWidget(c)
	c.Hide()
	return c
}

// query asks the completer about the caret's position and shows what comes
// back. Nothing to offer closes the popup: an empty one says nothing and
// hides the text.
func (c *completions) query() {
	if c.e.completer == nil {
		c.dismiss()
		return
	}
	doc := c.e.doc
	text := doc.Text()
	res := c.e.completer(text, runeOffset(text, doc.Offset(doc.Caret())))
	if len(res.Candidates) == 0 {
		c.dismiss()
		return
	}
	c.items = res.Candidates
	c.start, c.end = byteOffset(text, res.Start), byteOffset(text, res.End)
	c.sel, c.top = 0, 0
	c.Show()
	c.e.Refresh()
}

// dismiss closes the popup, which Escape, a caret move and a click all do.
func (c *completions) dismiss() {
	if !c.Visible() {
		return
	}
	c.items = nil
	c.Hide()
	c.e.Refresh()
}

// move changes the chosen candidate, wrapping at either end, and scrolls the
// drawn window to keep it in sight.
func (c *completions) move(n int) {
	if len(c.items) == 0 {
		return
	}
	c.sel = (c.sel + n%len(c.items) + len(c.items)) % len(c.items)
	switch {
	case c.sel < c.top:
		c.top = c.sel
	case c.sel >= c.top+completionRows:
		c.top = c.sel - completionRows + 1
	}
	c.Refresh()
}

// accept writes the chosen candidate over the word being typed, as one
// undoable edit.
func (c *completions) accept() {
	if c.sel < 0 || c.sel >= len(c.items) {
		c.dismiss()
		return
	}
	item := c.items[c.sel]
	c.dismiss()
	doc := c.e.doc
	c.e.surface.edit(func() {
		doc.SetCaret(doc.PosAt(c.start), false)
		doc.SetCaret(doc.PosAt(c.end), true)
		doc.Insert(item.Insert)
	})
}

// key handles the keys the popup takes while it is open, and reports whether
// it took the key. What it does not take goes to the editor, so typing goes
// on refining the word.
func (c *completions) key(k *fyne.KeyEvent) bool {
	if !c.Visible() {
		return false
	}
	switch k.Name {
	case fyne.KeyUp:
		c.move(-1)
	case fyne.KeyDown:
		c.move(1)
	case fyne.KeyPageUp:
		c.move(-completionRows)
	case fyne.KeyPageDown:
		c.move(completionRows)
	case fyne.KeyReturn, fyne.KeyEnter, fyne.KeyTab:
		c.accept()
	case fyne.KeyEscape:
		c.dismiss()
	default:
		return false
	}
	return true
}

// typed is called after a rune has been inserted. A letter goes on with the
// word, and opens the popup where one was not open; anything else — a space,
// a bracket, an operator — ends the word and closes it. A dot opens it
// because what follows one is a name in a place the schema knows.
func (c *completions) typed(r rune) {
	switch {
	case r == '.' || r == '_' || isLetter(r):
		c.query()
	case c.Visible() && isDigit(r):
		c.query() // within a word already being completed
	default:
		c.dismiss()
	}
}

// retype is called after a key that changed the word being typed without
// typing into it — Backspace, Delete. It refines what is offered where the
// popup is open, and never opens it: deleting is not asking.
func (c *completions) retype() {
	if c.Visible() {
		c.query()
	}
}

func isLetter(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 0x7f
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// AccessibilityLabel speaks the chosen candidate and where it is in the list,
// which is what a person who cannot see the popup needs to hear.
func (c *completions) AccessibilityLabel() string {
	if len(c.items) == 0 {
		return ""
	}
	it := c.items[c.sel]
	parts := []string{it.Label, kindWord(it.Kind)}
	if it.Detail != "" {
		parts = append(parts, it.Detail)
	}
	parts = append(parts, fmt.Sprintf("%d of %d", c.sel+1, len(c.items)))
	return strings.Join(parts, ", ")
}

func (c *completions) AccessibilityRole() fyne.AccessibleRole { return fyne.AccessibleRoleContainer }

// kindWord names a candidate's kind, beside its label. Colour alone would not
// say it: a person who cannot tell two colours apart would lose the
// distinction entirely (WCAG 1.4.1).
func kindWord(k source.CompletionKind) string {
	switch k {
	case source.CompletionTable:
		return "table"
	case source.CompletionView:
		return "view"
	case source.CompletionColumn:
		return "column"
	case source.CompletionFunction:
		return "function"
	case source.CompletionSchema:
		return "schema"
	case source.CompletionDatabase:
		return "database"
	case source.CompletionAlias:
		return "alias"
	case source.CompletionSnippet:
		return "snippet"
	}
	return "keyword"
}

// kindColour draws a candidate in the colour its word has in the text, so a
// keyword in the popup looks like a keyword in the editor.
func (c *completions) kindColour(k source.CompletionKind) color.NRGBA {
	p := c.e.pal
	switch k {
	case source.CompletionFunction:
		return p.SyntaxFunction
	case source.CompletionTable, source.CompletionView:
		return p.AccentText
	case source.CompletionSchema, source.CompletionDatabase, source.CompletionAlias:
		return p.SecondaryLabel
	case source.CompletionKeyword:
		return p.SyntaxKeyword
	}
	return p.Label
}

// rows is how many candidates are drawn.
func (c *completions) rows() int { return min(len(c.items), completionRows) }

func (c *completions) MinSize() fyne.Size {
	m := currentMetrics()
	w := float32(completionMinWidth)
	for _, it := range c.items {
		row := textWidth(it.Label, m) + completionGap + textWidth(it.Detail, m) +
			completionGap + textWidth(kindWord(it.Kind), m) + 2*completionPad
		w = max(w, row)
	}
	return fyne.NewSize(min(w, completionMaxWidth), float32(c.rows())*m.lh+2*completionPad)
}

func textWidth(s string, m metrics) float32 {
	if s == "" {
		return 0
	}
	return fyne.MeasureText(s, m.size, fyne.TextStyle{}).Width
}

// place puts the popup under the word being completed, or above it where
// there is no room below, and never off the right edge.
func (c *completions) place(size fyne.Size) {
	if !c.Visible() {
		return
	}
	m := currentMetrics()
	e := c.e
	word := e.doc.PosAt(c.start)
	x := pad + float32(e.doc.Column(word))*m.cw - e.scroll.Offset.X + e.gutter.Size().Width
	y := pad + float32(word.Line+1)*m.lh - e.scroll.Offset.Y

	want := c.MinSize()
	if x+want.Width > size.Width {
		x = size.Width - want.Width
	}
	if y+want.Height > size.Height {
		if above := y - m.lh - want.Height; above >= 0 {
			y = above // the caret's line would be covered otherwise
		} else {
			y = max(0, size.Height-want.Height)
		}
	}
	c.Move(fyne.NewPos(max(0, x), max(0, y)))
	c.Resize(want)
}

func (c *completions) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(color.Transparent)
	bg.CornerRadius = fynetheme.InputRadiusSize()
	bg.StrokeWidth = 1
	return &completionsRenderer{c: c, bg: bg}
}

type completionsRenderer struct {
	c       *completions
	bg      *canvas.Rectangle
	rows    []*canvas.Rectangle
	labels  []*canvas.Text
	details []*canvas.Text
	kinds   []*canvas.Text
	objects []fyne.CanvasObject
}

func (r *completionsRenderer) Destroy() {}

func (r *completionsRenderer) MinSize() fyne.Size { return r.c.MinSize() }

func (r *completionsRenderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *completionsRenderer) Layout(fyne.Size) { r.Refresh() }

func (r *completionsRenderer) Refresh() {
	c := r.c
	p := c.e.pal
	m := currentMetrics()
	n := c.rows()

	r.bg.FillColor = p.ElevatedBackground
	r.bg.StrokeColor = p.ControlBorder
	r.bg.Resize(c.Size())
	r.bg.Move(fyne.NewPos(0, 0))

	r.grow(n)
	r.objects = r.objects[:0]
	r.objects = append(r.objects, r.bg)
	for i := 0; i < n; i++ {
		it := c.items[c.top+i]
		y := completionPad + float32(i)*m.lh
		selected := c.top+i == c.sel

		row := r.rows[i]
		row.FillColor = color.Transparent
		if selected {
			row.FillColor = p.SelectedEmphasized
		}
		row.Resize(fyne.NewSize(c.Size().Width-2*completionPad, m.lh))
		row.Move(fyne.NewPos(completionPad, y))

		label, detail, kind := r.labels[i], r.details[i], r.kinds[i]
		label.Text, label.Color = it.Label, c.kindColour(it.Kind)
		detail.Text, detail.Color = it.Detail, p.SecondaryLabel
		kind.Text, kind.Color = kindWord(it.Kind), p.TertiaryLabel
		if selected {
			label.Color, detail.Color, kind.Color = p.OnSelectedEmphasized, p.OnSelectedEmphasized, p.OnSelectedEmphasized
		}
		for _, t := range []*canvas.Text{label, detail, kind} {
			t.TextSize = m.size
			t.Refresh()
		}
		ty := y + (m.lh-m.th)/2

		label.Move(fyne.NewPos(2*completionPad, ty))
		kind.Move(fyne.NewPos(c.Size().Width-2*completionPad-kind.MinSize().Width, ty))
		detail.Move(fyne.NewPos(c.Size().Width-3*completionPad-kind.MinSize().Width-completionGap-detail.MinSize().Width, ty))

		r.objects = append(r.objects, row, label, detail, kind)
	}
	canvas.Refresh(c)
}

// grow keeps enough drawing objects for n rows. They are reused: the popup is
// rebuilt on every keystroke.
func (r *completionsRenderer) grow(n int) {
	for len(r.rows) < n {
		rect := canvas.NewRectangle(color.Transparent)
		rect.CornerRadius = fynetheme.SelectionRadiusSize()
		r.rows = append(r.rows, rect)
		r.labels = append(r.labels, canvas.NewText("", color.Black))
		d := canvas.NewText("", color.Black)
		k := canvas.NewText("", color.Black)
		r.details = append(r.details, d)
		r.kinds = append(r.kinds, k)
	}
}

// runeOffset and byteOffset convert between what the document counts in
// (bytes) and what a completion request and result are in (runes).
func runeOffset(text string, bytes int) int {
	if bytes > len(text) {
		bytes = len(text)
	}
	return utf8.RuneCountInString(text[:bytes])
}

func byteOffset(text string, runes int) int {
	if runes <= 0 {
		return 0
	}
	n := 0
	for i := range text {
		if n == runes {
			return i
		}
		n++
	}
	return len(text)
}
