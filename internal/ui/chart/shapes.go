package chart

import (
	"image"
	"image/color"
	"math"
)

// Drawing the kinds that are not points and lines.
//
// Each of these goes into the same NRGBA buffer the spike's scatter and line
// do, and blends rather than paints, so that overlapping marks read as
// density (ADR-0004).

// RasterBars draws one bar per point, from the axis to the value.
//
// The bar reaches the axis rather than the bottom of the chart, so a
// negative value hangs below it. A bar that always grew upward from the
// floor would draw −5 and +5 the same.
func RasterBars(img *image.NRGBA, pts []Point, xs, ys Scale, c color.NRGBA, width float64) {
	axis := ys.Project(0)
	for _, p := range pts {
		x := xs.Project(p.X)
		y := ys.Project(p.Y)
		fillRect(img, x-width/2, math.Min(y, axis), x+width/2, math.Max(y, axis), c)
	}
}

// RasterStack draws one bar per point, standing on what is already there.
//
// base is how high each column has been built so far, in data units, and is
// added to as the stack grows: a stacked bar is only meaningful if every
// layer knows where the one below it ended.
func RasterStack(img *image.NRGBA, pts []Point, xs, ys Scale, c color.NRGBA,
	width float64, base map[float64]float64) {
	for _, p := range pts {
		from := base[p.X]
		to := from + p.Y
		x := xs.Project(p.X)
		fillRect(img, x-width/2, ys.Project(to), x+width/2, ys.Project(from), c)
		base[p.X] = to
	}
}

// RasterArea fills between a series and the axis.
//
// The fill stops at the axis, not at the foot of the chart, for the same
// reason a bar does: the area under a line that crosses zero is two areas.
func RasterArea(img *image.NRGBA, pts []Point, xs, ys Scale, c color.NRGBA) {
	// One point has nothing to fill between, which the loop below already
	// says by not running.
	axis := ys.Project(0)
	for i := 1; i < len(pts); i++ {
		x0, y0 := xs.Project(pts[i-1].X), ys.Project(pts[i-1].Y)
		x1, y1 := xs.Project(pts[i].X), ys.Project(pts[i].Y)
		if x1 < x0 {
			x0, y0, x1, y1 = x1, y1, x0, y0
		}
		// A column of pixels per step across, each reaching from the line
		// down to the axis: the shape between two points is a trapezium,
		// and a column at a time is how one is filled without a polygon
		// filler this package does not have.
		for x := math.Floor(x0); x <= math.Ceil(x1); x++ {
			t := 0.0
			if x1 != x0 {
				t = (x - x0) / (x1 - x0)
			}
			y := y0 + t*(y1-y0)
			fillRect(img, x, math.Min(y, axis), x+1, math.Max(y, axis), c)
		}
	}
}

// RasterPie draws wedges clockwise from the top.
//
// From the top and clockwise because that is how every pie anybody has read
// is drawn, and a chart that starts somewhere else is one somebody has to
// work out before they can read it.
func RasterPie(img *image.NRGBA, slices []Slice, cx, cy, radius float64, colours []color.NRGBA) {
	if radius <= 0 {
		return
	}
	angle := -math.Pi / 2 // twelve o'clock
	for i, s := range slices {
		sweep := s.Share * 2 * math.Pi
		wedge(img, cx, cy, radius, angle, angle+sweep, colourAt(colours, i))
		angle += sweep
	}
}

// wedge fills one slice by walking the pixels of its bounding box and
// keeping those inside both the circle and the angles.
func wedge(img *image.NRGBA, cx, cy, radius, from, to float64, c color.NRGBA) {
	for y := int(cy - radius); y <= int(cy+radius); y++ {
		for x := int(cx - radius); x <= int(cx+radius); x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			if dx*dx+dy*dy > radius*radius {
				continue
			}
			if within(math.Atan2(dy, dx), from, to) {
				blend(img, x, y, c)
			}
		}
	}
}

// within reports whether an angle lies in a sweep, with both put into the
// same turn of the circle first.
func within(a, from, to float64) bool {
	norm := func(v float64) float64 {
		for v < 0 {
			v += 2 * math.Pi
		}
		return math.Mod(v, 2*math.Pi)
	}
	a, span := norm(a-from), to-from
	if span >= 2*math.Pi {
		return true
	}
	return a < span
}

// RasterHistogram draws a bar per bin, each spanning its own interval.
//
// A histogram's bars touch, because the values between two edges are one
// continuous run. Gaps between them would read as categories, which is the
// other chart.
func RasterHistogram(img *image.NRGBA, bins []Bin, xs, ys Scale, c color.NRGBA) {
	axis := ys.Project(0)
	for _, b := range bins {
		if b.Count == 0 {
			continue
		}
		fillRect(img, xs.Project(b.Min), ys.Project(float64(b.Count)),
			xs.Project(b.Max), axis, c)
	}
}

// fillRect blends a rectangle, clipped to the image.
func fillRect(img *image.NRGBA, x0, y0, x1, y1 float64, c color.NRGBA) {
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	// At least one pixel: a bar for a value too small to round to a pixel is
	// still a bar, and drawing nothing would say the value is absent.
	left, right := int(math.Floor(x0)), int(math.Ceil(x1))
	top, bottom := int(math.Floor(y0)), int(math.Ceil(y1))
	if right <= left {
		right = left + 1
	}
	if bottom <= top {
		bottom = top + 1
	}
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			blend(img, x, y, c)
		}
	}
}

// colourAt picks a colour, going round again where there are more marks than
// colours.
func colourAt(colours []color.NRGBA, i int) color.NRGBA {
	if len(colours) == 0 {
		return color.NRGBA{A: 255}
	}
	return colours[i%len(colours)]
}

// BarWidth is how wide a bar is, given how many there are across the axis.
//
// Bars are separated by a gap of a fifth, which is what makes a bar chart
// read as separate values rather than as a filled area.
func BarWidth(pixels float64, bars int) float64 {
	if bars < 1 {
		return 0
	}
	w := pixels / float64(bars) * 0.8
	return math.Max(w, 1)
}
