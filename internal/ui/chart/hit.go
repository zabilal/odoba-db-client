package chart

import (
	"math"
	"sort"
)

// Hit testing for tooltips and click-to-filter (FR-11.4).
//
// Two properties are non-negotiable, and both are easy to get subtly wrong:
//
//  1. Hits are computed against the FULL data, never the downsampled set drawn
//     on screen. A tooltip must report a value that is actually in the user's
//     result set. See downsample.go.
//
//  2. "Nearest" is measured in PIXEL space, not data space. When the X axis
//     spans ten years and the Y axis spans 0 to 1, the nearest point in data
//     units is almost never the one under the cursor. Distance has to be what
//     the user sees.

// HitIndex answers "which point is under the cursor" in constant average time.
type HitIndex struct {
	pts    []Point
	xs, ys Scale

	// A uniform grid over the plot area, in pixels. Each cell lists the points
	// whose projections fall in it.
	cell  float64
	cols  int
	rows  int
	cells [][]int32

	// sortedX is point indices ordered by X, for nearest-X queries, which is
	// how line, area and bar charts resolve a tooltip: by column, not by 2-D
	// proximity.
	sortedX []int32
}

// hitCellPixels is the grid cell size. Roughly a cursor's hit radius, so a
// query touches at most a 3x3 block of cells.
const hitCellPixels = 12.0

// NewHitIndex builds an index over every point.
//
// Construction is O(n) and happens once per data change, not per mouse move —
// at 100k points it is the part that must be fast enough not to stall opening
// the chart.
func NewHitIndex(pts []Point, xs, ys Scale) *HitIndex {
	h := &HitIndex{pts: pts, xs: xs, ys: ys, cell: hitCellPixels}
	h.cols = int(math.Ceil(xs.Pixels/h.cell)) + 1
	h.rows = int(math.Ceil(ys.Pixels/h.cell)) + 1
	if h.cols < 1 {
		h.cols = 1
	}
	if h.rows < 1 {
		h.rows = 1
	}
	h.cells = make([][]int32, h.cols*h.rows)

	for i, p := range pts {
		cx, cy := h.cellOf(xs.Project(p.X), ys.Project(p.Y))
		k := cy*h.cols + cx
		h.cells[k] = append(h.cells[k], int32(i))
	}

	h.sortedX = make([]int32, len(pts))
	for i := range pts {
		h.sortedX[i] = int32(i)
	}
	sort.Slice(h.sortedX, func(a, b int) bool {
		return pts[h.sortedX[a]].X < pts[h.sortedX[b]].X
	})
	return h
}

func (h *HitIndex) cellOf(px, py float64) (int, int) {
	cx := int(px / h.cell)
	cy := int(py / h.cell)
	if cx < 0 {
		cx = 0
	}
	if cy < 0 {
		cy = 0
	}
	if cx >= h.cols {
		cx = h.cols - 1
	}
	if cy >= h.rows {
		cy = h.rows - 1
	}
	return cx, cy
}

// Hit is a resolved point under the cursor.
type Hit struct {
	Point Point
	// Distance is in pixels from the cursor.
	Distance float64
}

// Nearest returns the point closest to a pixel position within radius pixels,
// for scatter charts. ok is false when nothing is close enough — a tooltip
// that snaps to a point 200 pixels away is worse than no tooltip.
func (h *HitIndex) Nearest(px, py, radius float64) (Hit, bool) {
	if len(h.pts) == 0 {
		return Hit{}, false
	}
	cx, cy := h.cellOf(px, py)
	reach := int(math.Ceil(radius / h.cell))

	best := -1
	bestD := radius * radius
	for gy := cy - reach; gy <= cy+reach; gy++ {
		if gy < 0 || gy >= h.rows {
			continue
		}
		for gx := cx - reach; gx <= cx+reach; gx++ {
			if gx < 0 || gx >= h.cols {
				continue
			}
			for _, i := range h.cells[gy*h.cols+gx] {
				p := h.pts[i]
				dx := h.xs.Project(p.X) - px
				dy := h.ys.Project(p.Y) - py
				if d := dx*dx + dy*dy; d <= bestD {
					bestD, best = d, int(i)
				}
			}
		}
	}
	if best < 0 {
		return Hit{}, false
	}
	return Hit{Point: h.pts[best], Distance: math.Sqrt(bestD)}, true
}

// NearestX returns the point whose X is closest to a pixel column, for line,
// area and bar charts. These resolve a tooltip by column: the user points at a
// time, not at a dot.
func (h *HitIndex) NearestX(px float64) (Hit, bool) {
	n := len(h.sortedX)
	if n == 0 {
		return Hit{}, false
	}
	target := h.xs.Unproject(px)

	i := sort.Search(n, func(k int) bool { return h.pts[h.sortedX[k]].X >= target })

	best := -1
	bestD := math.Inf(1)
	for _, k := range []int{i - 1, i} {
		if k < 0 || k >= n {
			continue
		}
		p := h.pts[h.sortedX[k]]
		if d := math.Abs(h.xs.Project(p.X) - px); d < bestD {
			bestD, best = d, int(h.sortedX[k])
		}
	}
	if best < 0 {
		return Hit{}, false
	}
	return Hit{Point: h.pts[best], Distance: bestD}, true
}

// InRect returns every point inside a pixel rectangle, for drag-to-select and
// click-to-filter (FR-11.4). Returns original indices, so a selection maps
// straight back to rows in the grid.
func (h *HitIndex) InRect(x0, y0, x1, y1 float64) []int {
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	cx0, cy0 := h.cellOf(x0, y0)
	cx1, cy1 := h.cellOf(x1, y1)

	var out []int
	for gy := cy0; gy <= cy1; gy++ {
		for gx := cx0; gx <= cx1; gx++ {
			for _, i := range h.cells[gy*h.cols+gx] {
				p := h.pts[i]
				px, py := h.xs.Project(p.X), h.ys.Project(p.Y)
				if px >= x0 && px <= x1 && py >= y0 && py <= y1 {
					out = append(out, p.Index)
				}
			}
		}
	}
	return out
}

// bruteNearest is the reference implementation the index is checked against.
// Exported to tests only; it is O(n) and exists purely as an oracle.
func bruteNearest(pts []Point, xs, ys Scale, px, py, radius float64) (Hit, bool) {
	best := -1
	bestD := radius * radius
	for i, p := range pts {
		dx := xs.Project(p.X) - px
		dy := ys.Project(p.Y) - py
		if d := dx*dx + dy*dy; d <= bestD {
			bestD, best = d, i
		}
	}
	if best < 0 {
		return Hit{}, false
	}
	return Hit{Point: pts[best], Distance: math.Sqrt(bestD)}, true
}
