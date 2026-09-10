// Package view is the query editor's widget (T1.57–T1.59). It draws an
// editor.Document — syntax colours, selection, caret, line numbers and the
// bracket pair at the caret — and turns keys, clicks and drags into Document
// operations.
//
// Only visible lines are drawn, so a 10 000-line script costs what the lines
// on screen cost (NFR-P6). That is why this is a widget of its own rather than
// a widget.Entry, which lays out and shapes all of its text.
package view

import (
	"image/color"
	"math"
	"runtime"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// pad is the margin around the text, in points.
const pad = 6

// Editor is the query editor: line numbers beside a scrolling, focusable text
// surface.
type Editor struct {
	widget.BaseWidget

	doc     *editor.Document
	pal     uitheme.Palette
	surface *surface
	gutter  *gutter
	scroll  *container.Scroll

	// OnChanged is called after an edit changes the text; caret moves and
	// selection changes do not call it.
	OnChanged func()

	mark *errorMark
}

// errorMark underlines the word a server rejected, until the text changes.
type errorMark struct {
	from, to editor.Pos
	rev      uint64
}

// MarkError underlines the word at p in the danger colour and moves the caret
// there, for a statement the server rejected (FR-5.10). The mark lasts until
// the text next changes, since an edit may have moved what it pointed at.
func (e *Editor) MarkError(p editor.Pos) {
	from, to := e.doc.WordAt(p)
	e.mark = &errorMark{from: from, to: to, rev: e.doc.Revision()}
	e.doc.SetCaret(from, false)
	e.surface.changed()
}

// New builds an editor over a document. UI goroutine only, like the document.
func New(doc *editor.Document, pal uitheme.Palette) *Editor {
	e := &Editor{doc: doc, pal: pal}
	e.surface = newSurface(e)
	e.gutter = newGutter(e)
	e.scroll = container.NewScroll(e.surface)
	e.scroll.OnScrolled = func(fyne.Position) { e.redraw() }
	e.ExtendBaseWidget(e)
	return e
}

// Document is the text being edited.
func (e *Editor) Document() *editor.Document { return e.doc }

// SetPalette recolours the editor, for an appearance change.
func (e *Editor) SetPalette(p uitheme.Palette) {
	e.pal = p
	e.Refresh()
}

// Focus puts the keyboard focus in the editor.
func (e *Editor) Focus() { e.surface.focus() }

// Focused reports whether the editor has the keyboard focus.
func (e *Editor) Focused() bool { return e.surface.focused }

func (e *Editor) redraw() {
	e.surface.Refresh()
	e.gutter.Refresh()
}

func (e *Editor) CreateRenderer() fyne.WidgetRenderer {
	bg := canvas.NewRectangle(e.pal.ContentBackground)
	content := container.NewBorder(nil, nil, e.gutter, nil, e.scroll)
	return &editorRenderer{e: e, bg: bg, content: content}
}

type editorRenderer struct {
	e       *Editor
	bg      *canvas.Rectangle
	content *fyne.Container
}

func (r *editorRenderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.content.Resize(size)
}
func (r *editorRenderer) MinSize() fyne.Size { return r.content.MinSize() }
func (r *editorRenderer) Refresh() {
	r.bg.FillColor = r.e.pal.ContentBackground
	r.bg.Refresh()
	r.e.redraw()
}
func (r *editorRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.bg, r.content} }
func (r *editorRenderer) Destroy()                     {}

// metrics are the monospaced grid text is laid on.
type metrics struct {
	size   float32 // text size
	cw, lh float32 // character width, line height
	th     float32 // text height, for centring text in its line
}

func currentMetrics() metrics {
	size := fynetheme.TextSize()
	m := fyne.MeasureText("M", size, fyne.TextStyle{Monospace: true})
	return metrics{size: size, cw: m.Width, th: m.Height,
		lh: float32(math.Ceil(float64(m.Height + size*0.35)))}
}

// visible is the range of lines the viewport shows.
func (e *Editor) visible(m metrics) (first, last int) {
	n := e.doc.Buffer().LineCount()
	off, h := e.scroll.Offset.Y, e.scroll.Size().Height
	if h <= 0 {
		h = 60 * m.lh // not laid out yet: draw a screenful
	}
	first = max(0, int((off-pad)/m.lh))
	last = min(n-1, int((off+h-pad)/m.lh)+1)
	return first, last
}

func (e *Editor) colour(k sqllex.TokenKind) color.NRGBA {
	p := e.pal
	switch k {
	case sqllex.TokKeyword:
		return p.SyntaxKeyword
	case sqllex.TokType:
		return p.SyntaxType
	case sqllex.TokFunction:
		return p.SyntaxFunction
	case sqllex.TokString:
		return p.SyntaxString
	case sqllex.TokNumber:
		return p.SyntaxNumber
	case sqllex.TokComment:
		return p.SyntaxComment
	case sqllex.TokParameter:
		return p.SyntaxParameter
	case sqllex.TokError:
		return p.Danger
	}
	return p.Label // identifiers, operators, punctuation: see Palette
}

// surface is the scrolling text area. It takes the focus and all input.
type surface struct {
	widget.BaseWidget
	e       *Editor
	focused bool
	shift   bool // Shift held: Fyne sends Shift+arrow as a plain key
}

func newSurface(e *Editor) *surface {
	s := &surface{e: e}
	s.ExtendBaseWidget(s)
	return s
}

func (s *surface) CreateRenderer() fyne.WidgetRenderer {
	r := &surfaceRenderer{s: s, current: canvas.NewRectangle(color.Transparent),
		caret: canvas.NewRectangle(color.Transparent), errLine: canvas.NewRectangle(color.Transparent)}
	for i := range r.brackets {
		r.brackets[i] = canvas.NewRectangle(color.Transparent)
	}
	r.Refresh()
	return r
}

func (s *surface) focus() {
	if c := fyne.CurrentApp().Driver().CanvasForObject(s); c != nil {
		c.Focus(s)
	}
}

func (s *surface) FocusGained() {
	s.focused = true
	s.e.redraw()
}

func (s *surface) FocusLost() {
	s.focused, s.shift = false, false
	s.e.redraw()
}

// AcceptsTab keeps Tab in the editor, where it indents, rather than moving
// the focus on.
func (s *surface) AcceptsTab() bool { return true }

func (s *surface) Cursor() desktop.Cursor { return desktop.TextCursor }

func (s *surface) KeyDown(k *fyne.KeyEvent) {
	if k.Name == desktop.KeyShiftLeft || k.Name == desktop.KeyShiftRight {
		s.shift = true
	}
}

func (s *surface) KeyUp(k *fyne.KeyEvent) {
	if k.Name == desktop.KeyShiftLeft || k.Name == desktop.KeyShiftRight {
		s.shift = false
	}
}

func (s *surface) TypedRune(r rune) { s.edit(func() { s.e.doc.Insert(string(r)) }) }

func (s *surface) TypedKey(k *fyne.KeyEvent) {
	d := s.e.doc
	switch k.Name {
	case fyne.KeyLeft:
		s.move(editor.Left)
	case fyne.KeyRight:
		s.move(editor.Right)
	case fyne.KeyUp:
		s.move(editor.Up)
	case fyne.KeyDown:
		s.move(editor.Down)
	case fyne.KeyHome:
		s.move(editor.LineStart)
	case fyne.KeyEnd:
		s.move(editor.LineEnd)
	case fyne.KeyPageUp:
		d.MoveLines(-s.page(), s.shift)
		s.changed()
	case fyne.KeyPageDown:
		d.MoveLines(s.page(), s.shift)
		s.changed()
	case fyne.KeyBackspace:
		s.edit(d.Backspace)
	case fyne.KeyDelete:
		s.edit(d.Delete)
	case fyne.KeyReturn, fyne.KeyEnter:
		s.edit(d.Newline)
	case fyne.KeyTab:
		if s.shift {
			s.edit(d.Outdent)
		} else {
			s.edit(d.Tab)
		}
	}
}

func (s *surface) TypedShortcut(sc fyne.Shortcut) {
	d := s.e.doc
	switch sc := sc.(type) {
	case *fyne.ShortcutCopy:
		if t := d.SelectedText(); t != "" && sc.Clipboard != nil {
			sc.Clipboard.SetContent(t)
		}
	case *fyne.ShortcutCut:
		if t := d.SelectedText(); t != "" && sc.Clipboard != nil {
			sc.Clipboard.SetContent(t)
			s.edit(func() { d.Cut() })
		}
	case *fyne.ShortcutPaste:
		if sc.Clipboard != nil {
			if t := sc.Clipboard.Content(); t != "" {
				s.edit(func() { d.Insert(t) })
			}
		}
	case *fyne.ShortcutSelectAll:
		d.SelectAll()
		s.changed()
	case *fyne.ShortcutUndo:
		s.edit(func() { d.Undo() })
	case *fyne.ShortcutRedo:
		s.edit(func() { d.Redo() })
	case *desktop.CustomShortcut:
		if fn := s.chord(sc); fn != nil {
			fn()
		}
	}
}

// wordModifier is the modifier that moves by word: ⌥ on macOS, Ctrl
// elsewhere.
func wordModifier() fyne.KeyModifier {
	if runtime.GOOS == "darwin" {
		return fyne.KeyModifierAlt
	}
	return fyne.KeyModifierControl
}

// chord maps a modified key to what it does, following each platform's
// convention. It returns nil for chords the editor leaves alone.
func (s *surface) chord(sc *desktop.CustomShortcut) func() {
	d := s.e.doc
	extend := sc.Modifier&fyne.KeyModifierShift != 0
	mod := sc.Modifier &^ fyne.KeyModifierShift
	cmd, mac := fyne.KeyModifierShortcutDefault, runtime.GOOS == "darwin"
	mv := func(m editor.Motion) func() {
		return func() {
			d.Move(m, extend)
			s.changed()
		}
	}
	// eraseTo deletes from the caret back to where a motion goes, unless a
	// selection exists, which is deleted instead.
	eraseTo := func(m editor.Motion) func() {
		return func() {
			s.edit(func() {
				if _, _, sel := d.Selection(); !sel {
					d.Move(m, true)
				}
				d.Backspace()
			})
		}
	}
	switch k := sc.KeyName; {
	case mod == wordModifier() && k == fyne.KeyLeft:
		return mv(editor.WordLeft)
	case mod == wordModifier() && k == fyne.KeyRight:
		return mv(editor.WordRight)
	case mod == wordModifier() && k == fyne.KeyBackspace && !extend:
		return eraseTo(editor.WordLeft)
	case mod == cmd && mac && k == fyne.KeyLeft:
		return mv(editor.LineStart)
	case mod == cmd && mac && k == fyne.KeyRight:
		return mv(editor.LineEnd)
	case mod == cmd && (mac && k == fyne.KeyUp || !mac && k == fyne.KeyHome):
		return mv(editor.DocStart)
	case mod == cmd && (mac && k == fyne.KeyDown || !mac && k == fyne.KeyEnd):
		return mv(editor.DocEnd)
	case mod == cmd && mac && k == fyne.KeyBackspace && !extend:
		return eraseTo(editor.LineStart)
	case mod == cmd && extend && k == fyne.KeyZ:
		return func() { s.edit(func() { d.Redo() }) }
	case mod == cmd && !extend && k == "]":
		return func() { s.edit(d.IndentLines) }
	case mod == cmd && !extend && k == "[":
		return func() { s.edit(d.Outdent) }
	}
	return nil
}

// Reserved lists the chords the editor handles itself. A menu item bound to
// one would take it first, whatever has the focus — Fyne tries the menu bar
// before the focused widget — and the editor would silently lose the key.
func Reserved() []fyne.Shortcut {
	cmd, mac := fyne.KeyModifierShortcutDefault, runtime.GOOS == "darwin"
	out := []fyne.Shortcut{
		&fyne.ShortcutCopy{}, &fyne.ShortcutCut{}, &fyne.ShortcutPaste{},
		&fyne.ShortcutSelectAll{}, &fyne.ShortcutUndo{}, &fyne.ShortcutRedo{},
		&desktop.CustomShortcut{KeyName: fyne.KeyZ, Modifier: cmd | fyne.KeyModifierShift},
		&desktop.CustomShortcut{KeyName: "]", Modifier: cmd},
		&desktop.CustomShortcut{KeyName: "[", Modifier: cmd},
		&desktop.CustomShortcut{KeyName: fyne.KeyBackspace, Modifier: wordModifier()},
	}
	moves := func(m fyne.KeyModifier, keys ...fyne.KeyName) {
		for _, k := range keys {
			out = append(out, &desktop.CustomShortcut{KeyName: k, Modifier: m},
				&desktop.CustomShortcut{KeyName: k, Modifier: m | fyne.KeyModifierShift})
		}
	}
	moves(wordModifier(), fyne.KeyLeft, fyne.KeyRight)
	if mac {
		moves(cmd, fyne.KeyLeft, fyne.KeyRight, fyne.KeyUp, fyne.KeyDown)
		out = append(out, &desktop.CustomShortcut{KeyName: fyne.KeyBackspace, Modifier: cmd})
	} else {
		moves(cmd, fyne.KeyHome, fyne.KeyEnd)
	}
	return out
}

func (s *surface) MouseDown(ev *desktop.MouseEvent) {
	if ev.Button != desktop.MouseButtonPrimary {
		return
	}
	s.focus()
	s.e.doc.SetCaret(s.posAt(ev.Position), ev.Modifier&fyne.KeyModifierShift != 0)
	s.changed()
}

func (s *surface) MouseUp(*desktop.MouseEvent) {}

// Tapped only focuses: MouseDown has already placed the caret.
func (s *surface) Tapped(*fyne.PointEvent) { s.focus() }

func (s *surface) DoubleTapped(ev *fyne.PointEvent) {
	s.e.doc.SelectWord(s.posAt(ev.Position))
	s.changed()
}

func (s *surface) Dragged(ev *fyne.DragEvent) {
	s.e.doc.SetCaret(s.posAt(ev.Position), true)
	s.changed()
}

func (s *surface) DragEnd() {}

// posAt is the document position under a point on the surface. Below the
// last line is the end of the document, as in any macOS text view.
func (s *surface) posAt(p fyne.Position) editor.Pos {
	m, doc := currentMetrics(), s.e.doc
	n := doc.Buffer().LineCount()
	line := int((p.Y - pad) / m.lh)
	if p.Y < pad {
		line = 0
	}
	if line >= n {
		return editor.Pos{Line: n - 1, Col: len(doc.Buffer().Line(n - 1))}
	}
	col := max(0, int((p.X-pad)/m.cw+0.5))
	return doc.PosAtColumn(line, col)
}

func (s *surface) page() int {
	return max(1, int(s.e.scroll.Size().Height/currentMetrics().lh)-1)
}

func (s *surface) move(m editor.Motion) {
	s.e.doc.Move(m, s.shift)
	s.changed()
}

// edit runs a document operation and reports it if the text changed.
func (s *surface) edit(fn func()) {
	rev := s.e.doc.Revision()
	fn()
	s.changed()
	if s.e.doc.Revision() != rev && s.e.OnChanged != nil {
		s.e.OnChanged()
	}
}

// changed redraws after the document changed, keeping the caret in view.
func (s *surface) changed() {
	s.e.scroll.Refresh() // the content may have grown: re-measure it first
	s.ensureVisible()
	s.e.redraw()
}

// ensureVisible scrolls just enough to show the caret, with a few columns of
// context horizontally so the caret is never pinned to the edge.
func (s *surface) ensureVisible() {
	e, m := s.e, currentMetrics()
	vs := e.scroll.Size()
	if vs.Height <= 0 {
		return
	}
	c := e.doc.Caret()
	x := pad + float32(e.doc.Column(c))*m.cw
	y := pad + float32(c.Line)*m.lh
	off := e.scroll.Offset
	switch {
	case y < off.Y:
		off.Y = y - pad
	case y+m.lh > off.Y+vs.Height:
		off.Y = y + m.lh + pad - vs.Height
	}
	switch {
	case x < off.X:
		off.X = x - 4*m.cw
	case x+m.cw > off.X+vs.Width:
		off.X = x + 4*m.cw - vs.Width
	}
	off.X, off.Y = max(0, off.X), max(0, off.Y)
	if off != e.scroll.Offset {
		e.scroll.ScrollToOffset(off)
	}
}

// surfaceRenderer draws the visible lines from pools of canvas objects, so
// scrolling reuses rather than allocates.
type surfaceRenderer struct {
	s        *surface
	m        metrics
	current  *canvas.Rectangle
	caret    *canvas.Rectangle
	errLine  *canvas.Rectangle
	brackets [2]*canvas.Rectangle
	sel      []*canvas.Rectangle
	texts    []*canvas.Text
	objects  []fyne.CanvasObject

	cols int    // widest line in columns, for the horizontal extent
	rev  uint64 // document revision cols was measured at
}

func (r *surfaceRenderer) MinSize() fyne.Size {
	n := r.s.e.doc.Buffer().LineCount()
	return fyne.NewSize(float32(r.cols+1)*r.m.cw+2*pad, float32(n)*r.m.lh+2*pad)
}

func (r *surfaceRenderer) Layout(fyne.Size)             { r.draw() }
func (r *surfaceRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *surfaceRenderer) Destroy()                     {}

func (r *surfaceRenderer) Refresh() {
	r.m = currentMetrics()
	if doc := r.s.e.doc; r.rev != doc.Revision() || r.cols == 0 {
		r.rev, r.cols = doc.Revision(), widest(doc)
	}
	r.draw()
	canvas.Refresh(r.s)
}

// widest measures the widest line, bounding tabs from above: exact widths
// would cost a rune walk per line on every keystroke.
func widest(doc *editor.Document) int {
	w, buf := 0, doc.Buffer()
	for i := 0; i < buf.LineCount(); i++ {
		l := buf.Line(i)
		w = max(w, len(l)+strings.Count(l, "\t")*(doc.TabWidth-1))
	}
	return w
}

func (r *surfaceRenderer) draw() {
	e, m := r.s.e, r.m
	doc, buf, pal := e.doc, e.doc.Buffer(), e.pal
	first, last := e.visible(m)
	at := func(col, line int) fyne.Position {
		return fyne.NewPos(pad+float32(col)*m.cw, pad+float32(line)*m.lh)
	}
	width := max(r.s.Size().Width, r.MinSize().Width)
	objs := r.objects[:0]

	caret := doc.Caret()
	from, to, sel := doc.Selection()
	if !sel && caret.Line >= first && caret.Line <= last {
		r.current.FillColor = pal.AlternateRow
		r.current.Move(fyne.NewPos(0, at(0, caret.Line).Y))
		r.current.Resize(fyne.NewSize(width, m.lh))
		objs = append(objs, r.current)
	}

	if sel {
		n := 0
		for l := max(first, from.Line); l <= min(last, to.Line); l++ {
			c0, c1 := 0, doc.Column(editor.Pos{Line: l, Col: len(buf.Line(l))})+1 // +1: the newline is selected too
			if l == from.Line {
				c0 = doc.Column(from)
			}
			if l == to.Line {
				c1 = doc.Column(to)
			}
			if n == len(r.sel) {
				r.sel = append(r.sel, canvas.NewRectangle(color.Transparent))
			}
			rect := r.sel[n]
			n++
			// RangeSelection whether or not focused: the syntax colours are
			// contrast-checked on it and on nothing grey (ADR-0006).
			rect.FillColor = pal.RangeSelection
			rect.Move(at(c0, l))
			rect.Resize(fyne.NewSize(float32(c1-c0)*m.cw, m.lh))
			objs = append(objs, rect)
		}
	}

	if a, b, ok := doc.MatchBracket(); ok {
		for i, p := range []editor.Pos{a, b} {
			if p.Line < first || p.Line > last {
				continue
			}
			rect := r.brackets[i]
			rect.FillColor = color.Transparent
			rect.StrokeColor = pal.SecondaryLabel
			rect.StrokeWidth = 1
			rect.Move(at(doc.Column(p), p.Line))
			rect.Resize(fyne.NewSize(m.cw, m.lh))
			objs = append(objs, rect)
		}
	}

	n := 0
	style := fyne.TextStyle{Monospace: true}
	for l := first; l <= last; l++ {
		line, toks := buf.Line(l), doc.Highlighter().Tokens(l)
		for i := 0; i < len(toks); {
			// Merge neighbours of one colour into a single run: fewer objects
			// to shape and draw, and the monospaced grid keeps them aligned.
			col := e.colour(toks[i].Kind)
			j := i
			for j+1 < len(toks) && e.colour(toks[j+1].Kind) == col {
				j++
			}
			start, end := int(toks[i].Start), min(int(toks[j].End), len(line))
			i = j + 1
			text := line[start:end]
			if strings.TrimSpace(text) == "" {
				continue
			}
			startCol := doc.Column(editor.Pos{Line: l, Col: start})
			if n == len(r.texts) {
				r.texts = append(r.texts, canvas.NewText("", col))
			}
			t := r.texts[n]
			n++
			t.Text = expandTabs(text, startCol, doc.TabWidth)
			t.Color, t.TextSize, t.TextStyle = col, m.size, style
			p := at(startCol, l)
			t.Move(fyne.NewPos(p.X, p.Y+(m.lh-m.th)/2))
			t.Resize(fyne.NewSize(float32(len(t.Text))*m.cw, m.th))
			objs = append(objs, t)
		}
	}

	if mk := e.mark; mk != nil && mk.rev == doc.Revision() && mk.from.Line >= first && mk.from.Line <= last {
		c0, c1 := doc.Column(mk.from), doc.Column(mk.to)
		r.errLine.FillColor = pal.Danger
		p := at(c0, mk.from.Line)
		r.errLine.Move(fyne.NewPos(p.X, p.Y+m.lh-2))
		r.errLine.Resize(fyne.NewSize(float32(max(c1-c0, 1))*m.cw, 2))
		objs = append(objs, r.errLine)
	}

	if r.s.focused && caret.Line >= first && caret.Line <= last {
		r.caret.FillColor = pal.Label
		r.caret.Move(at(doc.Column(caret), caret.Line))
		r.caret.Resize(fyne.NewSize(2, m.lh))
		objs = append(objs, r.caret)
	}
	r.objects = objs
}

// expandTabs draws tabs as the spaces they stand for, from the column the
// text starts at.
func expandTabs(text string, col, width int) string {
	if !strings.Contains(text, "\t") || width <= 0 {
		return text
	}
	var b strings.Builder
	for _, r := range text {
		if r == '\t' {
			n := width - col%width
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

// gutter draws line numbers. It sits outside the scroll container so that it
// stays put when the text scrolls sideways, and follows it vertically.
type gutter struct {
	widget.BaseWidget
	e *Editor
}

func newGutter(e *Editor) *gutter {
	g := &gutter{e: e}
	g.ExtendBaseWidget(g)
	return g
}

func (g *gutter) CreateRenderer() fyne.WidgetRenderer {
	r := &gutterRenderer{g: g}
	r.Refresh()
	return r
}

type gutterRenderer struct {
	g       *gutter
	m       metrics
	labels  []*canvas.Text
	objects []fyne.CanvasObject
}

func (r *gutterRenderer) MinSize() fyne.Size {
	digits := len(strconv.Itoa(r.g.e.doc.Buffer().LineCount()))
	return fyne.NewSize(float32(max(digits, 2))*r.m.cw+2*pad, 0)
}

func (r *gutterRenderer) Layout(fyne.Size)             { r.draw() }
func (r *gutterRenderer) Objects() []fyne.CanvasObject { return r.objects }
func (r *gutterRenderer) Destroy()                     {}

func (r *gutterRenderer) Refresh() {
	r.m = currentMetrics()
	r.draw()
	canvas.Refresh(r.g)
}

func (r *gutterRenderer) draw() {
	e, m := r.g.e, r.m
	first, last := e.visible(m)
	caret, off := e.doc.Caret().Line, e.scroll.Offset.Y
	width := r.MinSize().Width
	objs := r.objects[:0]
	for l := first; l <= last; l++ {
		i := l - first
		if i == len(r.labels) {
			t := canvas.NewText("", color.Transparent)
			t.Alignment = fyne.TextAlignTrailing
			r.labels = append(r.labels, t)
		}
		t := r.labels[i]
		t.Text = strconv.Itoa(l + 1)
		t.TextSize, t.TextStyle = m.size, fyne.TextStyle{Monospace: true}
		t.Color = e.pal.SecondaryLabel
		if l == caret {
			t.Color = e.pal.Label
		}
		t.Move(fyne.NewPos(0, pad+float32(l)*m.lh-off+(m.lh-m.th)/2))
		t.Resize(fyne.NewSize(width-pad, m.th))
		objs = append(objs, t)
	}
	r.objects = objs
}
