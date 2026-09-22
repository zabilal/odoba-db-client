package scene

import (
	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
)

// Turning a scene into Fyne objects.
//
// This decides nothing: what is drawn, where, and in what colours are all
// settled before it is called. That is what makes an exported picture the
// same picture — the PNG is a widget of these objects rendered off-screen,
// and the SVG is the same scene written out.

// Objects is what Fyne draws for a scene.
func Objects(s Scene) []fyne.CanvasObject {
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
		case Raster:
			img := fcanvas.NewImageFromImage(v.Img)
			// The image was drawn at exactly the size it is placed at, so
			// it is stretched rather than fitted: fitting would reserve a
			// minimum size of its own and move it.
			img.FillMode = fcanvas.ImageFillStretch
			img.Move(fyne.NewPos(float32(v.X), float32(v.Y)))
			img.Resize(fyne.NewSize(float32(v.W), float32(v.H)))
			out = append(out, img)
		case Text:
			t := fcanvas.NewText(v.S, v.Fill)
			t.TextSize = float32(v.Size)
			t.TextStyle = fyne.TextStyle{Bold: v.Bold}
			// Fyne positions text from its top-left corner, so anything
			// else is measured and moved.
			size := t.MinSize()
			x, y := float32(v.X), float32(v.Y)
			switch v.Align {
			case Center:
				x -= size.Width / 2
			case Trailing:
				x -= size.Width
			}
			if v.Middle {
				y -= size.Height / 2
			}
			t.Move(fyne.NewPos(x, y))
			out = append(out, t)
		}
	}
	return out
}
