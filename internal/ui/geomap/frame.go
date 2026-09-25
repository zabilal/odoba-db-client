package geomap

import (
	"image"
	"math"
	"strconv"

	"github.com/ikigai-db/ikigai-db/internal/ui/chart"
)

// Where a map's parts go.
//
// The one thing a map's frame must do that a chart's must not: keep the two axes
// to the same scale. A degree of longitude and a degree of latitude have to be
// the same length on the screen, or every shape is wrong — a square lake drawn
// as a rectangle is a drawing that misleads about the data, which is the one
// thing a picture of data may not do.
//
// That is as far as the projection goes: longitude across, latitude up, nothing
// about the curve of the Earth. It is right for a county and wrong for a
// hemisphere, and it is said where the map is drawn rather than hidden here.

// Frame is where a map's parts go, in the pixels of one drawing.
type Frame struct {
	// W and H are the size this was laid out for.
	W, H float64

	// Plot is the rectangle the places are drawn in.
	Plot image.Rectangle

	// X and Y map coordinates to pixels within Plot, to the same scale.
	X, Y chart.Scale

	// XTicks and YTicks are the graticule: the labelled degrees, in Plot's
	// coordinates.
	XTicks, YTicks []chart.Tick
}

// Layout works out a frame for a reading at a size.
//
// A reading covering nothing — one point, or every point in the same place —
// gets a rectangle around it, because a scale of zero span projects everything
// to the middle and a map of one place should show where that place is.
func Layout(b Built, w, h float64) Frame {
	f := Frame{W: w, H: h}
	left, right, top, bottom := gutterLeft, gutterRight, gutterTop, gutterBottom
	plotW, plotH := w-left-right, h-top-bottom
	if plotW < 1 || plotH < 1 {
		return f
	}
	f.Plot = image.Rect(int(left), int(top), int(left+plotW), int(top+plotH))

	minX, minY, maxX, maxY := b.MinX, b.MinY, b.MaxX, b.MaxY
	if !b.Ok {
		minX, minY, maxX, maxY = -180, -90, 180, 90
	}
	// A rectangle of no size at all is a place, not a nothing: half a degree
	// around it, which is a few kilometres and shows the neighbourhood.
	//
	// Only where it has no size in either direction. A line of places — every
	// one at the same longitude, say — needs nothing done to it: the two axes
	// are kept to the same scale below, so the axis that does have a span gives
	// the other one its own.
	if maxX-minX < smallest && maxY-minY < smallest {
		minX, maxX = minX-smallest/2, maxX+smallest/2
		minY, maxY = minY-smallest/2, maxY+smallest/2
	}
	// A margin, so that a point on the edge of the data is not a point on the
	// edge of the window.
	spanX, spanY := (maxX-minX)*margin, (maxY-minY)*margin
	midX, midY := (minX+maxX)/2, (minY+maxY)/2

	// The same units per pixel on both axes: whichever axis needs more room
	// decides, and the other gets the slack.
	perPixel := math.Max(spanX/plotW, spanY/plotH)
	halfX, halfY := perPixel*plotW/2, perPixel*plotH/2

	f.X = chart.Scale{Min: midX - halfX, Max: midX + halfX, Pixels: plotW}
	f.Y = chart.Scale{Min: midY - halfY, Max: midY + halfY, Pixels: plotH, Invert: true}
	f.XTicks = f.X.Ticks(ticksAcross(plotW), degrees)
	f.YTicks = f.Y.Ticks(ticksAcross(plotH), degrees)
	return f
}

// The room around the plot: enough for the degree labels, which are wider on the
// left than they are tall underneath.
const (
	gutterLeft   float64 = 56
	gutterRight  float64 = 12
	gutterTop    float64 = 12
	gutterBottom float64 = 24
)

// smallest is the span a map covers where the places cover less: half a degree
// is a few kilometres, which shows a point's neighbourhood rather than the point
// alone.
const smallest = 0.5

// margin is how much more than the data's own rectangle is shown.
const margin = 1.08

// ticksAcross is how many labels fit in a length, at about one every eighty
// pixels: closer than that and the degrees run together.
func ticksAcross(pixels float64) int {
	n := int(pixels / 80)
	if n < 2 {
		return 2
	}
	if n > 10 {
		return 10
	}
	return n
}

// degrees writes a coordinate as a label. The digits follow the span, because a
// map of a city that labelled its axes in whole degrees would label them all the
// same.
func degrees(v float64) string {
	switch {
	case v == math.Trunc(v):
		return strconv.FormatFloat(v, 'f', -1, 64) + "°"
	case math.Abs(v) >= 10:
		return strconv.FormatFloat(v, 'f', 2, 64) + "°"
	}
	return strconv.FormatFloat(v, 'f', -1, 64) + "°"
}

// At is where a position falls in the drawing, in pixels of the whole picture.
func (f Frame) At(x, y float64) (float64, float64) {
	return float64(f.Plot.Min.X) + f.X.Project(x), float64(f.Plot.Min.Y) + f.Y.Project(y)
}

// Coordinate is the place a pixel of the whole picture falls on, which is what
// turns a cursor into a position somebody can read.
func (f Frame) Coordinate(px, py float64) (x, y float64) {
	return f.X.Unproject(px - float64(f.Plot.Min.X)), f.Y.Unproject(py - float64(f.Plot.Min.Y))
}
