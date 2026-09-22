package chart

import (
	"math"
	"strconv"
)

// What is under the cursor (FR-11.4).
//
// The spike settled the two properties that are easy to get subtly wrong and
// not negotiable (hit.go): a reading is resolved against the whole series and
// never the reduced set that was drawn, and "nearest" is measured in pixels
// rather than in data units. This is the layer above that — one probe over a
// whole chart, which knows how each kind is pointed at.
//
// A line, an area and a stack are pointed at by column: somebody points at a
// day, not at a dot. A scatter is pointed at by proximity, because its dots
// are the thing. A bar, a wedge and a bin are pointed at by containment —
// the shape is large and the cursor is either in it or not.

// NoRow is a reading that no single row stands behind: a wedge that gathered
// a tail, or a histogram bin, which is many rows counted.
const NoRow = -1

// Reading is what the cursor is over.
type Reading struct {
	// Series is which series it is in, or NoRow where the kind has one.
	Series int
	// Name is what to call it: the series' name, or the wedge's.
	Name string

	// X and Y are the values read. Where the kind counts rather than plots
	// — a histogram, a pie — Y is the count or the share and X is the left
	// edge of what was counted.
	X, Y float64

	// Row is the row of the result it came from, or NoRow.
	Row int

	// Lines are what to write, in the order to write them.
	Lines []string

	// PX and PY are where in the plot the thing read sits, so that it can be
	// marked. A mark on the point rather than on the cursor is what says
	// which of two close points was read.
	PX, PY float64
}

// hitRadius is how far a cursor may be from a dot and still be pointing at
// it. Wider than the dot, because a dot is three pixels across and a hand is
// not that steady; narrow enough that a tooltip does not snap to something
// the cursor is nowhere near.
const hitRadius = 18.0

// plotSlack is how far outside the plot still counts as inside it.
//
// The plot is whole pixels and the scales are not, so a point at the very
// end of an axis projects a fraction past the last pixel of the rectangle it
// was drawn in. Without this the last point of every chart would be the one
// that could not be pointed at.
const plotSlack = 1.0

// Probe answers what is under a position in a chart.
//
// It is built once per drawing and asked on every movement of the pointer,
// which is the way round the index was designed for.
type Probe struct {
	c Chart
	f Frame

	// at is one index per series, over that series' whole data.
	at []*HitIndex

	// stacks are the running totals a stacked bar is drawn from, so that a
	// reading says which layer the cursor is in rather than which series
	// happens to own the nearest number.
	stacks []map[float64]float64

	wedges []Slice
}

// NewProbe indexes a chart for pointing at.
func NewProbe(c Chart, f Frame) *Probe {
	p := &Probe{c: c, f: f}
	switch c.Kind {
	case Pie:
		p.wedges = Wedges(c)
		return p
	case Histogram:
		return p
	}
	for _, s := range c.Series {
		p.at = append(p.at, NewHitIndex(s.Points, f.X, f.Y))
	}
	if c.Kind == StackedBar {
		p.stacks = runningTotals(c.Series)
	}
	return p
}

// runningTotals is how tall each series' layer stands at each position, which
// is where its bar actually is on the screen.
func runningTotals(series []Series) []map[float64]float64 {
	out := make([]map[float64]float64, len(series))
	below := map[float64]float64{}
	for i, s := range series {
		out[i] = map[float64]float64{}
		for _, pt := range s.Points {
			out[i][pt.X] = below[pt.X] + pt.Y
		}
		for _, pt := range s.Points {
			below[pt.X] = out[i][pt.X]
		}
	}
	return out
}

// At is what the chart says at a position, given in the whole picture's
// coordinates rather than the plot's.
func (p *Probe) At(x, y float64) (Reading, bool) {
	px := x - float64(p.f.Plot.Min.X)
	py := y - float64(p.f.Plot.Min.Y)
	if p.c.Kind != Pie && (px < -plotSlack || py < -plotSlack ||
		px > float64(p.f.Plot.Dx())+plotSlack || py > float64(p.f.Plot.Dy())+plotSlack) {
		// Outside the plot is outside the data. A tooltip over the axis
		// would name a value nothing there stands for.
		return Reading{}, false
	}
	switch p.c.Kind {
	case Pie:
		return p.inWedge(x, y)
	case Histogram:
		return p.inBin(px, py)
	case Scatter:
		return p.nearestDot(px, py)
	case StackedBar:
		return p.inStack(px, py)
	case Bar:
		return p.inBar(px, py)
	}
	return p.alongColumn(px, py)
}

// alongColumn reads a line or an area: the point nearest the cursor's column,
// in whichever series has one closest to the cursor.
func (p *Probe) alongColumn(px, py float64) (Reading, bool) {
	best, bestSeries, found := Hit{}, 0, false
	bestD := math.Inf(1)
	for i, h := range p.at {
		hit, ok := h.NearestX(px)
		if !ok {
			continue
		}
		// Between two series at the same column, the one whose value is
		// nearest the cursor: that is the line being pointed at.
		d := math.Abs(p.f.Y.Project(hit.Point.Y) - py)
		if d < bestD {
			best, bestSeries, bestD, found = hit, i, d, true
		}
	}
	if !found {
		return Reading{}, false
	}
	return p.readingOf(bestSeries, best.Point), true
}

// nearestDot reads a scatter, where the dots are the thing being pointed at
// and a cursor nowhere near one is pointing at nothing.
func (p *Probe) nearestDot(px, py float64) (Reading, bool) {
	best, bestSeries, found := Hit{Distance: math.Inf(1)}, 0, false
	for i, h := range p.at {
		hit, ok := h.Nearest(px, py, hitRadius)
		if ok && hit.Distance < best.Distance {
			best, bestSeries, found = hit, i, true
		}
	}
	if !found {
		return Reading{}, false
	}
	return p.readingOf(bestSeries, best.Point), true
}

// inBar reads a grouped bar chart: which bar the cursor is inside.
//
// The bars were moved aside to stand beside one another, so that is where
// they are pointed at — but what is read is the point they stand for, whose
// position is the category's.
func (p *Probe) inBar(px, py float64) (Reading, bool) {
	for i, s := range p.c.Series {
		beside := offsetBars(s.Points, i, len(p.c.Series), p.f)
		for j, pt := range s.Points {
			if p.overBar(px, py, beside[j].X, 0, pt.Y) {
				return p.readingAt(i, pt, p.f.X.Project(beside[j].X), p.f.Y.Project(pt.Y)), true
			}
		}
	}
	return Reading{}, false
}

// inStack reads a stacked bar: which layer of which column.
func (p *Probe) inStack(px, py float64) (Reading, bool) {
	for i, s := range p.c.Series {
		for _, pt := range s.Points {
			top := p.stacks[i][pt.X]
			if p.overBar(px, py, pt.X, top-pt.Y, top) {
				return p.readingAt(i, pt, p.f.X.Project(pt.X), p.f.Y.Project(top)), true
			}
		}
	}
	return Reading{}, false
}

// overBar reports whether a position is inside the bar at a place, standing
// from one value to another.
func (p *Probe) overBar(px, py, at, from, to float64) bool {
	mid := p.f.X.Project(at)
	if math.Abs(px-mid) > p.f.Bars/2 {
		return false
	}
	lo, hi := p.f.Y.Project(from), p.f.Y.Project(to)
	if lo > hi {
		lo, hi = hi, lo
	}
	return py >= lo && py <= hi
}

// inBin reads a histogram: which bin the cursor is over, and how many fell
// in it. No row stands behind a count.
func (p *Probe) inBin(px, py float64) (Reading, bool) {
	for _, b := range p.f.Bins {
		if b.Count == 0 {
			continue
		}
		if px < p.f.X.Project(b.Min) || px > p.f.X.Project(b.Max) {
			continue
		}
		if py < p.f.Y.Project(float64(b.Count)) {
			continue
		}
		return Reading{
			Series: NoRow, Name: p.c.YTitle, X: b.Min, Y: float64(b.Count), Row: NoRow,
			PX: (p.f.X.Project(b.Min) + p.f.X.Project(b.Max)) / 2,
			PY: p.f.Y.Project(float64(b.Count)),
			Lines: []string{
				formatNumber(b.Min) + " to " + formatNumber(b.Max),
				count(b.Count, "value"),
			},
		}, true
	}
	return Reading{}, false
}

// inWedge reads a pie: which wedge the cursor is in.
func (p *Probe) inWedge(x, y float64) (Reading, bool) {
	cx := float64(p.f.Plot.Min.X) + float64(p.f.Plot.Dx())/2
	cy := float64(p.f.Plot.Min.Y) + float64(p.f.Plot.Dy())/2
	r := math.Min(float64(p.f.Plot.Dx()), float64(p.f.Plot.Dy()))/2 - 4
	dx, dy := x-cx, y-cy
	if dx*dx+dy*dy > r*r {
		return Reading{}, false
	}
	// The same angle the wedges are drawn from: clockwise from the top.
	a := math.Atan2(dy, dx) + math.Pi/2
	if a < 0 {
		a += 2 * math.Pi
	}
	at := 0.0
	for i, w := range p.wedges {
		next := at + w.Share*2*math.Pi
		if a >= at && a < next {
			return Reading{
				Series: i, Name: w.Name, X: float64(i), Y: w.Value, Row: w.Index,
				PX: cx - float64(p.f.Plot.Min.X), PY: cy - float64(p.f.Plot.Min.Y),
				Lines: []string{w.Name, formatNumber(w.Value) + " — " + share(w.Share)},
			}, true
		}
		at = next
	}
	return Reading{}, false
}

// count says how many, with the word for what they are.
func count(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}

// share writes a proportion as a percentage somebody can read.
func share(v float64) string {
	return strconv.FormatFloat(v*100, 'f', 1, 64) + "%"
}

// readingOf is one point of one series, marked where it is drawn.
func (p *Probe) readingOf(series int, pt Point) Reading {
	return p.readingAt(series, pt, p.f.X.Project(pt.X), p.f.Y.Project(pt.Y))
}

// readingAt is one point of one series, said in words and marked at a place.
//
// The mark's place is given rather than worked out, because a bar standing
// beside its neighbours and a layer standing on what is below it are both
// drawn somewhere their own value does not say.
func (p *Probe) readingAt(series int, pt Point, mx, my float64) Reading {
	r := Reading{
		Series: series, Name: p.c.Series[series].Name,
		X: pt.X, Y: pt.Y, Row: pt.Index,
		PX: mx, PY: my,
	}
	if len(p.c.Series) > 1 {
		r.Lines = append(r.Lines, r.Name)
	}
	r.Lines = append(r.Lines, p.along(pt.X)+": "+formatNumber(pt.Y))
	return r
}

// along is what a position on the bottom axis is called: the category, the
// moment, or the number.
func (p *Probe) along(x float64) string {
	if len(p.c.Labels) > 0 {
		return labelAt(p.c.Labels, int(math.Round(x)))
	}
	return p.c.xFormat()(x)
}

// Along is what a position on the bottom axis is called, for anybody who has
// a reading and needs to say where it was.
func Along(c Chart, x float64) string {
	return (&Probe{c: c}).along(x)
}
