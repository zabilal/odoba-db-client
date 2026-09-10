// Package palette is the command palette (⌘K; FR-15.1, UX principle 3): every
// action, found by typing a few letters of its name, with its shortcut shown so
// the user learns it.
package palette

import (
	"image/color"
	"runtime"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
)

const (
	maxResults = 12
	width      = 560
)

// Palette is the command palette for one window.
type Palette struct {
	reg  *commands.Registry
	win  fyne.Window
	goos string

	popup   *widget.PopUp
	entry   *queryEntry
	list    *widget.List
	matches []commands.Match
	sel     int
}

// New builds a palette for a window. Show opens it.
func New(reg *commands.Registry, win fyne.Window) *Palette {
	p := &Palette{reg: reg, win: win, goos: runtime.GOOS}
	p.entry = newQueryEntry(p)
	p.entry.SetPlaceHolder("Type a command")
	p.entry.OnChanged = p.setQuery

	p.list = widget.NewList(
		func() int { return len(p.matches) },
		func() fyne.CanvasObject { return newRow() },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < len(p.matches) {
				o.(*row).show(p.matches[i], p.goos, i == p.sel)
			}
		},
	)
	p.list.OnSelected = func(i widget.ListItemID) {
		p.sel = i
		p.runSelected()
	}
	return p
}

// Show opens the palette with an empty query and the focus in it.
func (p *Palette) Show() {
	p.entry.SetText("")
	p.setQuery("")
	if p.popup == nil {
		body := container.NewBorder(p.entry, nil, nil, nil, p.list)
		p.popup = widget.NewModalPopUp(body, p.win.Canvas())
	}
	p.popup.Resize(fyne.NewSize(width, 44+float32(maxResults)*30))
	cs := p.win.Canvas().Size()
	p.popup.Move(fyne.NewPos((cs.Width-width)/2, cs.Height*0.12))
	p.popup.Show()
	p.win.Canvas().Focus(p.entry)
}

// Hide closes the palette.
func (p *Palette) Hide() {
	if p.popup != nil {
		p.popup.Hide()
	}
}

// Visible reports whether the palette is open.
func (p *Palette) Visible() bool { return p.popup != nil && p.popup.Visible() }

func (p *Palette) setQuery(q string) {
	p.matches = p.reg.Search(q, maxResults)
	p.sel = 0
	p.list.UnselectAll()
	p.list.Refresh()
}

func (p *Palette) move(delta int) {
	if len(p.matches) == 0 {
		return
	}
	p.sel = (p.sel + delta + len(p.matches)) % len(p.matches)
	p.list.ScrollTo(p.sel)
	p.list.Refresh()
}

// runSelected hides the palette BEFORE running the command. Many commands open
// a dialog of their own, and running first would put that dialog behind the
// palette's modal layer.
func (p *Palette) runSelected() {
	if p.sel < 0 || p.sel >= len(p.matches) {
		return
	}
	m := p.matches[p.sel]
	if !m.Enabled {
		return // listed so the user learns it exists; not runnable now
	}
	p.Hide()
	p.reg.Run(m.Command.ID)
}

// queryEntry is a single-line entry that hands navigation keys to the palette
// instead of moving its own cursor.
type queryEntry struct {
	widget.Entry
	p *Palette
}

func newQueryEntry(p *Palette) *queryEntry {
	e := &queryEntry{p: p}
	e.ExtendBaseWidget(e)
	return e
}

func (e *queryEntry) TypedKey(k *fyne.KeyEvent) {
	switch k.Name {
	case fyne.KeyDown:
		e.p.move(1)
	case fyne.KeyUp:
		e.p.move(-1)
	case fyne.KeyReturn, fyne.KeyEnter:
		e.p.runSelected()
	case fyne.KeyEscape:
		e.p.Hide()
	default:
		e.Entry.TypedKey(k)
	}
}

// row draws one result: the label with matched letters highlighted, and the
// shortcut right-aligned.
type row struct {
	widget.BaseWidget
	bg       *canvas.Rectangle
	label    *fyne.Container
	shortcut *canvas.Text
}

func newRow() *row {
	r := &row{
		bg:       canvas.NewRectangle(color.Transparent),
		label:    container.NewHBox(),
		shortcut: canvas.NewText("", nil),
	}
	r.bg.CornerRadius = fynetheme.SelectionRadiusSize()
	r.ExtendBaseWidget(r)
	return r
}

func (r *row) show(m commands.Match, goos string, selected bool) {
	th := fyne.CurrentApp().Settings().Theme()
	v := fyne.CurrentApp().Settings().ThemeVariant()
	fg := th.Color(fynetheme.ColorNameForeground, v)
	hi := th.Color(fynetheme.ColorNameHyperlink, v)
	if !m.Enabled {
		fg = th.Color(fynetheme.ColorNameDisabled, v)
		hi = fg
	}

	r.bg.FillColor = color.Transparent
	if selected {
		r.bg.FillColor = th.Color(fynetheme.ColorNameHover, v)
	}

	// Split the label into runs of matched and unmatched runes, so the letters
	// the user typed stand out: that is how they learn why a result matched.
	matched := map[int]bool{}
	for _, i := range m.Positions {
		matched[i] = true
	}
	var runs []fyne.CanvasObject
	label := []rune(m.Label)
	for i := 0; i < len(label); {
		j := i
		for j < len(label) && matched[j] == matched[i] {
			j++
		}
		t := canvas.NewText(string(label[i:j]), fg)
		if matched[i] {
			t.Color = hi
			t.TextStyle = fyne.TextStyle{Bold: true}
		}
		runs = append(runs, t)
		i = j
	}
	r.label.Objects = runs
	r.label.Refresh()

	r.shortcut.Text = m.Command.Shortcut.Label(goos)
	r.shortcut.Color = th.Color(fynetheme.ColorNamePlaceHolder, v)
	r.shortcut.Alignment = fyne.TextAlignTrailing
	r.Refresh()
}

func (r *row) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewStack(r.bg,
		container.NewBorder(nil, nil, nil, r.shortcut, r.label)))
}
