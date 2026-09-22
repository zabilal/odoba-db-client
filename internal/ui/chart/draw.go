package chart

import (
	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
)

// A chart, said once (FR-11.1, FR-11.3).
//
// The same rule the diagram settled (ADR-0130): the drawing is described
// here, and the window, the PNG and the SVG each read that description. An
// exported chart cannot differ from the one it was exported from, because
// there is only one of them.
//
// The marks are the exception and are pixels by the time they arrive
// (ADR-0004). Everything around them — the axes, their labels, the titles
// and the key — stays text, so it is drawn in the application's type on a
// screen and can be searched for and copied in a file.

// Draw says what to draw of a chart in a frame.
func Draw(c Chart, f Frame) scene.Scene {
	s := scene.Scene{W: f.W, H: f.H, Background: c.Background}
	x0, y0 := float64(f.Plot.Min.X), float64(f.Plot.Min.Y)
	x1, y1 := float64(f.Plot.Max.X), float64(f.Plot.Max.Y)

	if HasAxes(c.Kind) {
		// Gridlines first, so the marks are drawn over them: a line of data
		// crossing a gridline should hide it, not be hidden by it.
		for _, t := range f.YTicks {
			s.Add(scene.Line{X1: x0, Y1: y0 + t.Pixel, X2: x1, Y2: y0 + t.Pixel,
				Stroke: c.Grid, Width: 1})
		}
	}

	s.Add(scene.Raster{X: x0, Y: y0, W: x1 - x0, H: y1 - y0, Img: Raster(c, f)})

	if HasAxes(c.Kind) {
		s.Add(
			scene.Line{X1: x0, Y1: y0, X2: x0, Y2: y1, Stroke: c.Axis, Width: 1},
			scene.Line{X1: x0, Y1: y1, X2: x1, Y2: y1, Stroke: c.Axis, Width: 1},
		)
		for _, t := range f.YTicks {
			s.Add(scene.Text{X: x0 - tickGap, Y: y0 + t.Pixel, S: t.Label,
				Size: TickTextSize, Fill: c.Axis, Align: scene.Trailing, Middle: true})
		}
		for _, t := range xLabels(f) {
			s.Add(scene.Text{X: x0 + t.Pixel, Y: y1 + tickGap, S: t.Label,
				Size: TickTextSize, Fill: c.Axis, Align: scene.Center})
		}
		if c.XTitle != "" {
			s.Add(scene.Text{X: (x0 + x1) / 2, Y: y1 + tickGap + TickTextSize + axisTitleGap,
				S: c.XTitle, Size: TitleTextSize, Fill: c.Axis, Align: scene.Center, Bold: true})
		}
		if c.YTitle != "" {
			// Along the top rather than turned on its side: rotated text is
			// harder to read, and a screen reader cannot turn its head.
			s.Add(scene.Text{X: x0, Y: edge, S: c.YTitle,
				Size: TitleTextSize, Fill: c.Axis, Bold: true})
		}
	}
	s.Add(keyShapes(c, f)...)
	return s
}

// xLabels thins the labels along the bottom so that two never touch.
//
// Category labels are one per bar, and a chart of sixty days would write
// sixty dates over one another. Every nth is written instead, which is a
// readable axis rather than a smear.
func xLabels(f Frame) []Tick {
	if len(f.XTicks) < 2 {
		return f.XTicks
	}
	need := widest(f.XTicks, TickTextSize) + tickGap
	gap := f.XTicks[1].Pixel - f.XTicks[0].Pixel
	if gap < 0 {
		gap = -gap
	}
	every := 1
	if gap > 0 {
		for float64(every)*gap < need {
			every++
		}
	}
	if every == 1 {
		return f.XTicks
	}
	out := make([]Tick, 0, len(f.XTicks)/every+1)
	for i := 0; i < len(f.XTicks); i += every {
		out = append(out, f.XTicks[i])
	}
	return out
}

// keyShapes is the key: a colour block and a name for each series, or for
// each wedge of a pie.
//
// It sits above the plot rather than beside it, because a key down the right
// takes width from the data, and the data is what somebody is reading.
func keyShapes(c Chart, f Frame) []scene.Shape {
	keys := Keys(c)
	if len(keys) == 0 {
		return nil
	}
	width := f.W - 2*edge
	var out []scene.Shape
	x, y := edge, edge
	if c.YTitle != "" && HasAxes(c.Kind) {
		y += TitleTextSize + axisTitleGap
	}
	for _, k := range keys {
		w := keyWidth(k)
		if x > edge && x+w > width {
			x, y = edge, y+legendRow
		}
		out = append(out,
			scene.Box{X: x, Y: y + (legendRow-legendSwatch)/2, W: legendSwatch, H: legendSwatch,
				Fill: k.Colour, Stroke: k.Colour, StrokeWidth: 0, Radius: 2},
			scene.Text{X: x + legendSwatch + tickGap, Y: y + legendRow/2, S: k.Name,
				Size: LegendTextSize, Fill: c.Axis, Middle: true},
		)
		x += w + legendGap
	}
	return out
}
