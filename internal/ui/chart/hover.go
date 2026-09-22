package chart

import (
	"image/color"

	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
)

// Pointing at a chart (FR-11.4).
//
// The tooltip and the mark are the widget's alone: they are not in the scene,
// because a scene is what a picture is, and an exported picture has no cursor
// in it.
//
// What is read is read against the whole series (hit.go). A tooltip that
// reported a point the reduction invented would show somebody a value that is
// not in their result, which for a tool whose purpose is reading data
// accurately is the one thing that must never happen.

// Sizes of the tooltip. Small enough not to cover what is being pointed at,
// large enough to read.
const (
	tipTextSize = 11.0
	tipPadding  = 6.0
	tipLine     = 15.0
	tipGap      = 14.0 // how far the tooltip sits from the cursor
	markRadius  = 4.0
)

// MouseIn starts following the pointer.
func (w *Widget) MouseIn(e *desktop.MouseEvent) { w.MouseMoved(e) }

// MouseMoved reads the chart under the pointer.
func (w *Widget) MouseMoved(e *desktop.MouseEvent) {
	w.point(float64(e.Position.X), float64(e.Position.Y))
}

// MouseOut drops the reading, so that a tooltip does not stay behind over a
// chart nobody is pointing at.
func (w *Widget) MouseOut() {
	if !w.reading.has {
		return
	}
	w.reading = held{}
	w.retip()
}

// point resolves a position and redraws only when what is read changes.
//
// Redrawing on every movement would rebuild the scene sixty times a second
// for a picture that has not changed.
func (w *Widget) point(x, y float64) {
	r, ok := readingAt(w.probe, x, y)
	was := w.reading
	w.reading = held{r: r, has: ok, x: x, y: y}
	if ok == was.has && (!ok || sameReading(r, was.r)) {
		if ok {
			// The tooltip follows the pointer even where the reading has
			// not changed, so it never sits under the hand.
			w.place()
		}
		return
	}
	w.retip()
}

// retip redraws the tooltip and nothing else.
func (w *Widget) retip() {
	if w.renderer == nil {
		return
	}
	w.renderer.retip()
	fcanvas.Refresh(w)
}

// readingAt asks a probe, allowing for there not being one.
func readingAt(p *Probe, x, y float64) (Reading, bool) {
	if p == nil {
		return Reading{}, false
	}
	return p.At(x, y)
}

// sameReading reports two readings of the same thing.
func sameReading(a, b Reading) bool {
	return a.Series == b.Series && a.Row == b.Row && a.X == b.X && a.Y == b.Y
}

// held is what the pointer is over, and where the pointer was.
type held struct {
	r    Reading
	has  bool
	x, y float64
}

// Reading is what the pointer is over, if anything.
func (w *Widget) Reading() (Reading, bool) { return w.reading.r, w.reading.has }

// Tapped hands over what was tapped, which is what a click filters on.
func (w *Widget) Tapped(e *fyne.PointEvent) {
	r, ok := readingAt(w.probe, float64(e.Position.X), float64(e.Position.Y))
	if !ok || w.OnPick == nil {
		return
	}
	w.OnPick(r)
}

// Cursor is the ordinary pointer: a chart is read, not dragged.
func (w *Widget) Cursor() desktop.Cursor { return desktop.DefaultCursor }

// tipObjects are the mark and the tooltip, drawn over the chart.
func (r *chartRenderer) tipObjects() []fyne.CanvasObject {
	h := r.w.reading
	if !h.has {
		return nil
	}
	c := r.w.chart
	ink := colourAt(c.Colours, max(h.r.Series, 0))

	// A ring rather than a filled dot, so the mark does not hide the point
	// it is marking.
	mark := fcanvas.NewCircle(color.Transparent)
	mark.StrokeColor = c.Axis
	mark.StrokeWidth = 2
	at := fyne.NewPos(float32(float64(r.w.frame.Plot.Min.X)+h.r.PX-markRadius),
		float32(float64(r.w.frame.Plot.Min.Y)+h.r.PY-markRadius))
	mark.Move(at)
	mark.Resize(fyne.NewSize(markRadius*2, markRadius*2))

	box := fcanvas.NewRectangle(c.Background)
	box.StrokeColor = c.Axis
	box.StrokeWidth = 1
	box.CornerRadius = 4

	out := []fyne.CanvasObject{mark, box}
	for _, line := range h.r.Lines {
		t := fcanvas.NewText(line, c.Axis)
		t.TextSize = tipTextSize
		out = append(out, t)
	}
	// A slice of colour down the left edge says which series was read
	// without anybody having to look back at the key.
	stripe := fcanvas.NewRectangle(ink)
	out = append(out, stripe)
	r.tip = tip{box: box, stripe: stripe, texts: out[2 : len(out)-1]}
	r.layoutTip()
	return out
}

// tip is the tooltip's parts, kept so that following the pointer moves them
// rather than building them again.
type tip struct {
	box    *fcanvas.Rectangle
	stripe *fcanvas.Rectangle
	texts  []fyne.CanvasObject
}

// layoutTip puts the tooltip beside the pointer, and inside the chart.
func (r *chartRenderer) layoutTip() {
	if r.tip.box == nil || !r.w.reading.has {
		return
	}
	h := r.w.reading
	w, lines := 0.0, len(h.r.Lines)
	for _, o := range r.tip.texts {
		w = max(w, float64(o.MinSize().Width))
	}
	tw := w + 2*tipPadding + 4
	th := float64(lines)*tipLine + 2*tipPadding - 4

	// Beside the pointer, and flipped to the other side rather than allowed
	// off the edge: a tooltip half outside the window is unreadable.
	x, y := h.x+tipGap, h.y+tipGap
	if x+tw > float64(r.size.Width) {
		x = h.x - tipGap - tw
	}
	if y+th > float64(r.size.Height) {
		y = h.y - tipGap - th
	}
	x, y = max(x, 0), max(y, 0)

	r.tip.box.Move(fyne.NewPos(float32(x), float32(y)))
	r.tip.box.Resize(fyne.NewSize(float32(tw), float32(th)))
	r.tip.stripe.Move(fyne.NewPos(float32(x), float32(y+2)))
	r.tip.stripe.Resize(fyne.NewSize(3, float32(th-4)))
	for i, o := range r.tip.texts {
		o.Move(fyne.NewPos(float32(x+tipPadding+4), float32(y+tipPadding-4+float64(i)*tipLine)))
	}
}

// place follows the pointer without rebuilding anything.
func (w *Widget) place() {
	if w.renderer == nil {
		return
	}
	w.renderer.layoutTip()
	fcanvas.Refresh(w)
}
