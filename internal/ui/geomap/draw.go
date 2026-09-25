package geomap

import (
	"image/color"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
)

// Drawing the places (FR-11.5).
//
// Said once, as a scene, for the reason every other drawing here is: a picture
// shown on a screen and a picture written to a file must be the same picture
// (ADR-0130).
//
// Lines are drawn as lines rather than filled. A filled polygon needs a fill
// rule — which way round a hole goes — and a rule applied to somebody's data
// wrongly draws a lake where there is land. An outline cannot be wrong that way.

// Map is everything needed to draw one.
type Map struct {
	Built Built
	Frame Frame

	// Colours are taken in turn, one per row, so that two rows are told apart.
	Colours []color.NRGBA
	// Axis is the colour of the graticule's lines and labels.
	Axis color.NRGBA
	// Grid is the colour of the graticule across the plot.
	Grid color.NRGBA
	// Background is what the map sits on.
	Background color.NRGBA

	// Highlight is the row drawn as chosen, or -1. It is how the map answers
	// the grid's selection: the row somebody is looking at is the mark that
	// stands out.
	Highlight int
}

// Draw says what to draw, in the order it is drawn: the graticule first, then
// the places on top of it.
func Draw(m Map) scene.Scene {
	s := scene.Scene{W: m.Frame.W, H: m.Frame.H, Background: m.Background}
	f := m.Frame
	if f.Plot.Dx() < 1 || f.Plot.Dy() < 1 {
		return s
	}
	plotX, plotY := float64(f.Plot.Min.X), float64(f.Plot.Min.Y)
	plotW, plotH := float64(f.Plot.Dx()), float64(f.Plot.Dy())

	// The graticule: a line for each degree, and its label outside the plot.
	for _, t := range f.XTicks {
		x := plotX + t.Pixel
		s.Add(scene.Line{X1: x, Y1: plotY, X2: x, Y2: plotY + plotH, Stroke: m.Grid, Width: 1})
		s.Add(scene.Text{X: x, Y: plotY + plotH + 4, S: t.Label, Size: labelSize,
			Fill: m.Axis, Align: scene.Center})
	}
	for _, t := range f.YTicks {
		y := plotY + t.Pixel
		s.Add(scene.Line{X1: plotX, Y1: y, X2: plotX + plotW, Y2: y, Stroke: m.Grid, Width: 1})
		s.Add(scene.Text{X: plotX - 6, Y: y, S: t.Label, Size: labelSize,
			Fill: m.Axis, Align: scene.Trailing, Middle: true})
	}
	s.Add(scene.Box{X: plotX, Y: plotY, W: plotW, H: plotH, Stroke: m.Axis, StrokeWidth: 1})

	for _, p := range m.Built.Places {
		col := m.colour(p.Row)
		width := lineWidth
		radius := dotRadius
		if p.Row == m.Highlight {
			width, radius = lineWidth*2, dotRadius*1.8
		}
		for _, line := range p.Lines {
			for i := 1; i < len(line); i++ {
				x1, y1 := f.At(line[i-1].X, line[i-1].Y)
				x2, y2 := f.At(line[i].X, line[i].Y)
				if !inside(f, x1, y1) && !inside(f, x2, y2) {
					// Both ends outside is a segment nobody can see. Drawn
					// anyway it would run across the labels, because nothing
					// here clips.
					continue
				}
				s.Add(scene.Line{X1: x1, Y1: y1, X2: x2, Y2: y2, Stroke: col, Width: width})
			}
		}
		for _, pt := range p.Points {
			x, y := f.At(pt.X, pt.Y)
			if !inside(f, x, y) {
				continue
			}
			s.Add(scene.Dot{X: x, Y: y, R: radius, Fill: col})
		}
	}
	return s
}

// inside reports whether a pixel falls in the plot. Nothing here clips a line to
// the plot's edge: a segment with one end inside is drawn whole, which can run a
// little into the margin and is why the margin is there.
func inside(f Frame, x, y float64) bool {
	return x >= float64(f.Plot.Min.X)-slack && x <= float64(f.Plot.Max.X)+slack &&
		y >= float64(f.Plot.Min.Y)-slack && y <= float64(f.Plot.Max.Y)+slack
}

const (
	labelSize = 11
	lineWidth = 1.5
	dotRadius = 3.5
	slack     = 2
	// pickWithin is how near a cursor must be to a place to be reading it: a
	// finger's width, in the same terms the chart's hover uses.
	pickWithin float64 = 12
)

// colour is the colour of a row's marks.
func (m Map) colour(row int) color.NRGBA {
	if len(m.Colours) == 0 {
		return color.NRGBA{R: 0x1f, G: 0x6f, B: 0xeb, A: 0xff}
	}
	return m.Colours[row%len(m.Colours)]
}

// Reading is what the cursor is over: the row, and where on the map.
type Reading struct {
	// Row is the row the nearest place belongs to, or -1 where the cursor is
	// not near one.
	Row int
	// X and Y are the coordinates under the cursor, which are worth showing
	// whether or not there is a place there.
	X, Y float64
}

// Read says what is under a pixel. The nearest position within a finger's width
// wins, and a tie goes to the first row, so that hovering the same pixel twice
// says the same thing.
func Read(m Map, px, py float64) Reading {
	x, y := m.Frame.Coordinate(px, py)
	out := Reading{Row: -1, X: x, Y: y}
	best := pickWithin * pickWithin
	consider := func(row int, at model.Position) {
		ax, ay := m.Frame.At(at.X, at.Y)
		dx, dy := ax-px, ay-py
		if d := dx*dx + dy*dy; d < best {
			best, out.Row = d, row
		}
	}
	for _, p := range m.Built.Places {
		for _, pt := range p.Points {
			consider(p.Row, pt)
		}
		for _, line := range p.Lines {
			for _, pt := range line {
				consider(p.Row, pt)
			}
		}
	}
	return out
}
