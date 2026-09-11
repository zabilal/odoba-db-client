package shell

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// errorBar is where the shell says that something failed (FR-15.7): a band
// across the top of the window rather than a dialog, so the data stays in
// view (UX principle 4). It says what happened in a sentence, offers one thing to do
// about it where there is one, and can be copied whole and dismissed. A
// later error replaces it. Errors here are sentences, never stack traces.
type errorBar struct {
	s         *Shell
	slot      *fyne.Container // the window's top slot: the bar, or nothing
	bg        *canvas.Rectangle
	message   *widget.Label
	details   *widget.Label
	detailBox fyne.CanvasObject
	more      *widget.Button
	action    *widget.Button
	box       *fyne.Container
	act       func()
	text      string
}

func (s *Shell) newErrorBar() *errorBar {
	b := &errorBar{s: s, slot: container.NewVBox(), bg: canvas.NewRectangle(s.colours().DangerSubtle),
		message: widget.NewLabel(""), details: widget.NewLabel("")}
	b.message.Truncation = fyne.TextTruncateEllipsis
	b.details.Selectable = true // no wrapping: see the WHERE bar, whose statement spilled when it wrapped
	b.detailBox = container.NewHScroll(b.details)
	b.detailBox.Hide()
	b.more = widget.NewButton("Details", b.toggleDetails)
	b.action = widget.NewButton("", func() {
		act := b.act
		b.dismiss()
		if act != nil {
			act()
		}
	})
	b.action.Importance = widget.HighImportance
	copyIt := widget.NewButtonWithIcon("Copy", fynetheme.ContentCopyIcon(), b.copyAll)
	closeIt := widget.NewButtonWithIcon("", fynetheme.CancelIcon(), b.dismiss)
	b.more.Importance, copyIt.Importance, closeIt.Importance = widget.LowImportance, widget.LowImportance, widget.LowImportance
	icon := widget.NewIcon(fynetheme.NewErrorThemedResource(fynetheme.ErrorIcon()))
	row := container.NewBorder(nil, nil, icon, container.NewHBox(b.action, b.more, copyIt, closeIt), b.message)
	b.box = container.NewStack(b.bg, container.NewPadded(container.NewVBox(row, b.detailBox)))
	return b
}

// showError says that something failed.
func (s *Shell) showError(err error) { s.showErrorWith(err, "", nil) }

// showErrorWith says that something failed and offers one thing to do about
// it, which dismisses the bar as it runs.
func (s *Shell) showErrorWith(err error, label string, act func()) {
	b := s.errors
	b.text = err.Error()
	first, rest, _ := strings.Cut(b.text, "\n")
	b.message.SetText(first)
	b.details.SetText(b.text)
	b.detailBox.Hide()
	if rest != "" || len(first) > 120 {
		b.more.Show()
	} else {
		b.more.Hide()
	}
	b.act = act
	b.action.SetText(label)
	if act != nil {
		b.action.Show()
	} else {
		b.action.Hide()
	}
	b.repaint(s.colours())
	b.slot.Objects = []fyne.CanvasObject{b.box}
	b.relayout()
}

func (b *errorBar) shown() bool { return len(b.slot.Objects) > 0 }

// repaint gives the band its tint for the palette in use. It is drawn, not
// themed, so an appearance change must repaint it: left alone, it kept the
// dark tint under light text and could not be read.
func (b *errorBar) repaint(p uitheme.Palette) {
	b.bg.FillColor = p.DangerSubtle
	b.bg.Refresh()
}

func (b *errorBar) dismiss() {
	b.slot.Objects = nil
	b.act = nil
	b.relayout()
}

func (b *errorBar) toggleDetails() {
	if b.detailBox.Visible() {
		b.detailBox.Hide()
	} else {
		b.detailBox.Show()
	}
	b.relayout()
}

func (b *errorBar) copyAll() { b.s.app.Clipboard().SetContent(b.text) }

// relayout re-divides the window between the bar and the rest. Refreshing
// the bar alone would leave it the room it had.
func (b *errorBar) relayout() {
	b.slot.Refresh()
	if c := b.s.win.Content(); c != nil {
		c.Refresh()
	}
}

// ShowError says that something failed, for callers outside the shell.
func (s *Shell) ShowError(err error) { s.showError(err) }
