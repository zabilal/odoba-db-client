// Package spark draws a sparkline: how a number has been moving, in a picture
// small enough to sit on a line of controls (FR-13.17).
//
// It is not a chart. A chart is a statement about a result that somebody chose
// to draw, with axes to read values off and a legend to say what is what; a
// sparkline is a shape, read at a glance, next to the number it is the history
// of. So it has no axes, no ticks, no legend and no hover, and the number it is
// about is written beside it by whatever put it there rather than on it.
//
// What it does share is the drawing: the marks are rasterised by the same code
// the charts use (chart.RasterLine, chart.RasterArea), so a line here is the
// same line there.
package spark

import (
	"image"
	"image/color"

	"github.com/ikigai-db/ikigai-db/internal/ui/chart"
	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
)

// Spark is what to draw: the values, oldest first, and the colours to draw them
// in.
type Spark struct {
	// Values are the points, oldest first. Two are the fewest that can be
	// drawn, because one value has no shape.
	Values []float64

	// Line is the colour of the line, and Fill of the area under it. A fill of
	// nothing draws the line alone.
	Line, Fill color.NRGBA

	// Background is what it sits on.
	Background color.NRGBA
}

// Bounds are the range the line is drawn against: nought to the largest value,
// so that the height of the line is how much rather than how much more than the
// quietest moment.
//
// A sparkline scaled to its own minimum would make a topic carrying between a
// thousand and a thousand and one records a second look as busy as one going
// from nothing to a thousand, which is the opposite of what it is for.
func (s Spark) Bounds() (low, high float64) {
	high = 0
	for _, v := range s.Values {
		if v > high {
			high = v
		}
	}
	if high <= 0 {
		// Nothing has happened. The line is drawn along the bottom rather than
		// scaled to fill the box with nothing.
		return 0, 1
	}
	return 0, high
}

// Draw is the picture at a size.
func Draw(s Spark, w, h float64) scene.Scene {
	out := scene.Scene{W: w, H: h, Background: s.Background}
	if w < 2 || h < 2 || len(s.Values) < 2 {
		// One value has no shape, and a box too small to hold a line would draw
		// a line that said something about its own rounding.
		return out
	}
	img := Raster(s, int(w), int(h))
	out.Add(scene.Raster{X: 0, Y: 0, W: w, H: h, Img: img})
	return out
}

// Raster is the marks alone, at a size in pixels.
func Raster(s Spark, w, h int) *image.NRGBA {
	img := chart.NewCanvas(w, h)
	if w < 2 || h < 2 || len(s.Values) < 2 {
		return img
	}
	low, high := s.Bounds()
	// The whole box: the busiest moment is the top row and the quietest the
	// bottom one, because a picture this small has no room to spare.
	xs := chart.Scale{Min: 0, Max: float64(len(s.Values) - 1), Pixels: float64(w - 1)}
	ys := chart.Scale{Min: low, Max: high, Pixels: float64(h - 1), Invert: true}
	pts := make([]chart.Point, 0, len(s.Values))
	for i, v := range s.Values {
		pts = append(pts, chart.Point{X: float64(i), Y: v, Index: i})
	}
	// The area first, so the line is drawn over it. A fill of nothing marks
	// nothing, which is what a sparkline with no fill asks for.
	chart.RasterArea(img, pts, xs, ys, s.Fill)
	chart.RasterLine(img, pts, xs, ys, s.Line)
	return img
}
