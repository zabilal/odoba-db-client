package chart

import "math"

// Downsampling for display.
//
// At 100 000 points on a chart 1 000 pixels wide, a hundred points share every
// pixel column. Drawing them all is not more accurate — it is the same picture,
// painted a hundred times over. Reducing to the pixel grid is both far cheaper
// and visually identical, provided the reduction preserves shape.
//
// The crucial boundary: downsampling is for DRAWING only. Hit-testing (hit.go)
// always runs against the full data. A tooltip that reports a downsampled proxy
// point would show the user a value that is not in their result set, which for
// a tool whose purpose is reading data accurately is the one thing that must
// never happen.

// Point is one datum in data space.
type Point struct {
	X, Y float64

	// Index is the point's position in the original series, preserved through
	// downsampling so that any drawn point can be traced back to its row.
	Index int
}

// LTTB reduces a series to at most threshold points using
// Largest-Triangle-Three-Buckets (Steinarsson, 2013).
//
// Chosen over simpler schemes because it keeps peaks and troughs. Taking every
// nth point, or averaging buckets, silently flattens spikes — and in a
// database result the spike is usually the row the user is looking for.
//
// pts must be sorted by X. The first and last points are always kept.
func LTTB(pts []Point, threshold int) []Point {
	n := len(pts)
	if threshold >= n || threshold < 3 {
		out := make([]Point, n)
		copy(out, pts)
		return out
	}

	out := make([]Point, 0, threshold)
	out = append(out, pts[0])

	// Buckets exclude the fixed first and last points.
	every := float64(n-2) / float64(threshold-2)
	a := 0 // index of the previously selected point

	for i := 0; i < threshold-2; i++ {
		// Average of the NEXT bucket: the third vertex of the triangle.
		nextStart := int(math.Floor(float64(i+1)*every)) + 1
		nextEnd := int(math.Floor(float64(i+2)*every)) + 1
		if nextEnd > n {
			nextEnd = n
		}
		var avgX, avgY float64
		cnt := nextEnd - nextStart
		if cnt <= 0 {
			nextStart, nextEnd, cnt = n-1, n, 1
		}
		for j := nextStart; j < nextEnd; j++ {
			avgX += pts[j].X
			avgY += pts[j].Y
		}
		avgX /= float64(cnt)
		avgY /= float64(cnt)

		// Choose the point in THIS bucket forming the largest triangle with
		// the previous selection and the next bucket's average.
		start := int(math.Floor(float64(i)*every)) + 1
		end := int(math.Floor(float64(i+1)*every)) + 1
		if end > n-1 {
			end = n - 1
		}

		best, bestArea := start, -1.0
		ax, ay := pts[a].X, pts[a].Y
		for j := start; j < end; j++ {
			area := math.Abs((ax-avgX)*(pts[j].Y-ay) - (ax-pts[j].X)*(avgY-ay))
			if area > bestArea {
				bestArea, best = area, j
			}
		}
		out = append(out, pts[best])
		a = best
	}

	out = append(out, pts[n-1])
	return out
}

// MinMaxBuckets reduces a series to one min and one max per pixel column.
//
// Where LTTB is optimised for looking right, this is optimised for being
// exact at the pixel level: every column draws the true vertical extent of the
// data that falls in it, so no spike can be lost however narrow. It is the
// safer choice for dense time series, and the property tests use it to check
// LTTB's output is visually faithful.
func MinMaxBuckets(pts []Point, xs Scale, columns int) []Point {
	if len(pts) == 0 || columns <= 0 {
		return nil
	}
	type bucket struct {
		lo, hi Point
		used   bool
	}
	b := make([]bucket, columns)
	for _, p := range pts {
		c := int(xs.Project(p.X) / xs.Pixels * float64(columns))
		if c < 0 {
			c = 0
		}
		if c >= columns {
			c = columns - 1
		}
		if !b[c].used {
			b[c] = bucket{lo: p, hi: p, used: true}
			continue
		}
		if p.Y < b[c].lo.Y {
			b[c].lo = p
		}
		if p.Y > b[c].hi.Y {
			b[c].hi = p
		}
	}

	out := make([]Point, 0, columns*2)
	for _, bk := range b {
		if !bk.used {
			continue
		}
		// Emit in X order so the polyline does not double back on itself.
		if bk.lo.X <= bk.hi.X {
			out = append(out, bk.lo)
			if bk.hi.Index != bk.lo.Index {
				out = append(out, bk.hi)
			}
		} else {
			out = append(out, bk.hi, bk.lo)
		}
	}
	return out
}
