package grid

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// cellWidget is the per-cell object handed to widget.Table.
//
// It is a custom widget rather than a widget.Label because a Label carries a
// full widget lifecycle — its own renderer, padding, wrapping and truncation
// logic — and a grid instantiates one per visible cell. At 30 columns by 40
// rows that is 1 200 of them per frame, so the per-object cost is the whole
// question of spike W1.
//
// This draws exactly two canvas primitives: a background rectangle carrying
// selection and changeset state, and a text object. Nothing else.
type cellWidget struct {
	widget.BaseWidget

	text  string
	fg    color.Color
	bg    color.Color
	align fyne.TextAlign
	style fyne.TextStyle

	rect  *canvas.Rectangle
	label *canvas.Text
}

func newCellWidget() *cellWidget {
	c := &cellWidget{fg: color.Black, bg: color.Transparent}
	c.ExtendBaseWidget(c)
	return c
}

// set updates the cell in place. Called for every visible cell every time the
// table refreshes, so it must avoid work when nothing changed — Fyne's Refresh
// is not free, and skipping unchanged cells is the single largest win
// available on this path.
func (c *cellWidget) set(text string, fg, bg color.Color, align fyne.TextAlign, style fyne.TextStyle) {
	if c.text == text && c.fg == fg && c.bg == bg && c.align == align && c.style == style {
		return
	}
	c.text, c.fg, c.bg, c.align, c.style = text, fg, bg, align, style

	if c.label == nil {
		return // not yet rendered; CreateRenderer will pick these up
	}

	c.label.Text = text
	c.label.Color = fg
	c.label.Alignment = align
	c.label.TextStyle = style
	c.rect.FillColor = bg

	c.label.Refresh()
	c.rect.Refresh()
}

func (c *cellWidget) CreateRenderer() fyne.WidgetRenderer {
	c.rect = canvas.NewRectangle(c.bg)

	c.label = canvas.NewText(c.text, c.fg)
	c.label.TextSize = theme.TextBody
	c.label.Alignment = c.align
	c.label.TextStyle = c.style

	return &cellRenderer{cell: c, objects: []fyne.CanvasObject{c.rect, c.label}}
}

type cellRenderer struct {
	cell    *cellWidget
	objects []fyne.CanvasObject
}

func (r *cellRenderer) Layout(size fyne.Size) {
	r.cell.rect.Resize(size)
	r.cell.rect.Move(fyne.NewPos(0, 0))

	// Inset the text horizontally; vertical centring comes from the text
	// object's own height within the cell.
	pad := theme.SpaceSM
	textH := r.cell.label.MinSize().Height
	r.cell.label.Resize(fyne.NewSize(size.Width-2*pad, textH))
	r.cell.label.Move(fyne.NewPos(pad, (size.Height-textH)/2))
}

func (r *cellRenderer) MinSize() fyne.Size {
	return fyne.NewSize(0, theme.RowHeight)
}

func (r *cellRenderer) Refresh() {
	r.cell.rect.Refresh()
	r.cell.label.Refresh()
}

func (r *cellRenderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *cellRenderer) Destroy() {}
