package grid

import (
	"time"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// gridLayout lays the grid's layers over one another, the table on its
// background with the hover tip above, and gives the filler column the width
// the shown columns leave.
type gridLayout struct{ g *TableGrid }

func (l *gridLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, o := range objects {
		o.Move(fyne.NewPos(0, 0))
		o.Resize(size)
	}
	l.g.viewWidth = size.Width
	l.g.fitFiller()
}

func (l *gridLayout) MinSize([]fyne.CanvasObject) fyne.Size { return l.g.table.MinSize() }

// fitFiller gives the filler column the width the shown columns leave, so
// the stripes reach the grid's edge. When the columns overflow the view it
// keeps a sliver, since Fyne keeps no column of no width.
func (g *TableGrid) fitFiller() {
	used := float32(0)
	if g.Table.ShowHeaderColumn {
		used = gutterWidth + theme.SeparatorWidth
	}
	for _, mc := range g.order {
		used += g.widths[mc] + theme.SeparatorWidth
	}
	if w := max(1, g.viewWidth-used); w != g.fillerWidth {
		g.fillerWidth = w
		g.Table.SetColumnWidth(len(g.order), w)
	}
}

// tipDelay is how long the pointer rests on a cell before its hint shows.
var tipDelay = 600 * time.Millisecond

// hint shows a cell's hint near the pointer once it has rested, or hides it
// when text is empty. The tip is drawn over the grid rather than opened as a
// popup: a popup takes the pointer, and the cell would never hear it leave.
func (g *TableGrid) hint(text string, at fyne.Position) {
	g.tipSeq++
	if text == "" {
		g.tip.Hide()
		return
	}
	seq := g.tipSeq
	show := func() {
		if seq == g.tipSeq {
			g.showTip(text, at)
		}
	}
	if tipDelay == 0 {
		show()
		return
	}
	time.AfterFunc(tipDelay, func() { g.run(show) })
}

func (g *TableGrid) showTip(text string, at fyne.Position) {
	g.tipText.Text, g.tipText.Color = text, g.palette.Label
	g.tipBg.FillColor, g.tipBg.StrokeColor = g.palette.ElevatedBackground, g.palette.Separator
	ts := g.tipText.MinSize()
	pad := theme.SpaceSM
	size := fyne.NewSize(ts.Width+2*pad, ts.Height+2*pad)
	pos := at.Subtract(fyne.CurrentApp().Driver().AbsolutePositionForObject(g.view)).Add(fyne.NewPos(0, theme.RowHeight))
	view := g.view.Size()
	pos.X, pos.Y = max(0, min(pos.X, view.Width-size.Width)), max(0, min(pos.Y, view.Height-size.Height))
	g.tipBg.Move(pos)
	g.tipBg.Resize(size)
	g.tipText.Move(pos.Add(fyne.NewPos(pad, pad)))
	g.tipText.Resize(ts)
	g.tip.Show()
	g.tipBg.Refresh()
	g.tipText.Refresh()
}
