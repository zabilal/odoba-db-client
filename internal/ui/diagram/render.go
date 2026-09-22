package diagram

import (
	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
)

// Turning a scene into Fyne objects.
//
// The renderer draws what Draw says to draw and decides nothing itself: what
// is visible, how finely, and in what colours are all settled before it is
// called. That is what makes an exported picture the same picture — the PNG
// is this widget rendered, and the SVG is the same scene written out.

type renderer struct {
	w  *Widget
	bg *fcanvas.Rectangle

	// objects is what Fyne draws, rebuilt when what is visible changes.
	objects []fyne.CanvasObject
}

func (r *renderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.bg.Move(fyne.NewPos(0, 0))
	r.build()
}

func (r *renderer) MinSize() fyne.Size {
	if r.w.minSize.IsZero() {
		return fyne.NewSize(120, 90)
	}
	return r.w.minSize
}

func (r *renderer) Refresh() {
	r.bg.FillColor = r.w.palette.ContentBackground
	r.bg.Refresh()
	r.build()
	fcanvas.Refresh(r.w)
}

func (r *renderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *renderer) Destroy() {}

func (r *renderer) build() {
	scene := Draw(r.w.graph, r.w.view, r.w.palette, r.w.selected)
	r.objects = append([]fyne.CanvasObject{r.bg}, fyneObjects(scene)...)
}

// fyneObjects turns a scene into the objects Fyne draws.
func fyneObjects(s Scene) []fyne.CanvasObject {
	out := make([]fyne.CanvasObject, 0, len(s.Shapes))
	for _, sh := range s.Shapes {
		switch v := sh.(type) {
		case Box:
			box := fcanvas.NewRectangle(v.Fill)
			box.StrokeColor = v.Stroke
			box.StrokeWidth = float32(v.StrokeWidth)
			box.CornerRadius = float32(v.Radius)
			box.Move(fyne.NewPos(float32(v.X), float32(v.Y)))
			box.Resize(fyne.NewSize(float32(v.W), float32(v.H)))
			out = append(out, box)
		case Line:
			line := fcanvas.NewLine(v.Stroke)
			line.StrokeWidth = float32(v.Width)
			line.Position1 = fyne.NewPos(float32(v.X1), float32(v.Y1))
			line.Position2 = fyne.NewPos(float32(v.X2), float32(v.Y2))
			out = append(out, line)
		case Dot:
			c := fcanvas.NewCircle(v.Fill)
			c.Move(fyne.NewPos(float32(v.X-v.R), float32(v.Y-v.R)))
			c.Resize(fyne.NewSize(float32(v.R*2), float32(v.R*2)))
			out = append(out, c)
		case Text:
			t := fcanvas.NewText(v.S, v.Fill)
			t.TextSize = float32(v.Size)
			t.TextStyle = fyne.TextStyle{Bold: v.Bold}
			x := float32(v.X)
			if v.Trailing {
				// Fyne positions text from its left edge, so trailing text
				// is measured and moved back.
				x -= t.MinSize().Width
			}
			t.Move(fyne.NewPos(x, float32(v.Y)))
			out = append(out, t)
		}
	}
	return out
}
