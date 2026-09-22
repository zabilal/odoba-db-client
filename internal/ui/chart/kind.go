package chart

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// The kinds of chart a result can be drawn as (FR-11.1).
//
// The spike settled how marks reach a screen — rasterised into one image
// rather than drawn as thousands of canvas objects (ADR-0004) — and gave
// scales, ticks, downsampling and a hit index. What it did not give is the
// shapes: a bar is not a line, and a pie is not either.
//
// Nothing here draws anything a result did not say. A chart is read as a
// statement about data, so a mark nobody's row put there is a lie told in a
// picture, which is harder to catch than one told in a number.

// Kind is how a result is drawn.
type Kind string

const (
	Line       Kind = "line"
	Bar        Kind = "bar"
	StackedBar Kind = "stacked bar"
	Area       Kind = "area"
	Pie        Kind = "pie"
	Scatter    Kind = "scatter"
	Histogram  Kind = "histogram"
)

// Kinds are every kind, in the order they are offered: the ones that answer
// "how did this change" first, then "how much of each", then the two that
// are about a distribution.
var Kinds = []Kind{Line, Area, Bar, StackedBar, Pie, Scatter, Histogram}

// Series is one named run of points, drawn in one colour.
type Series struct {
	Name   string
	Points []Point
}

// ErrNoData is a chart of nothing, which is not a failure: a query that
// returned no rows has no chart, and saying so is the answer.
var ErrNoData = errors.New("chart: there is nothing to draw")

// Unplottable is a chart that cannot honestly be drawn this way.
//
// It carries what to do instead, because a person who asked for a pie of
// something that is not parts of a whole wants a chart, not a refusal.
type Unplottable struct {
	Why    string
	Advice string
}

func (e *Unplottable) Error() string {
	if e.Advice == "" {
		return "chart: " + e.Why
	}
	return "chart: " + e.Why + ". " + e.Advice
}

// Check says whether a kind can honestly draw these series, before anything
// is drawn.
//
// Every refusal here is about meaning rather than about taste. A chart this
// cannot draw truthfully is one it does not draw.
func Check(k Kind, series []Series) error {
	if len(series) == 0 || countPoints(series) == 0 {
		return ErrNoData
	}
	switch k {
	case Pie:
		// A pie shows parts of a whole. A negative part has no share of
		// one, and a whole of nothing has no parts.
		if len(series) != 1 {
			return &Unplottable{
				Why:    "a pie shows one set of parts and this has " + plural(len(series), "series"),
				Advice: "Draw it as a stacked bar, which compares several.",
			}
		}
		total := 0.0
		for _, p := range series[0].Points {
			if p.Y < 0 {
				return &Unplottable{
					Why:    "a pie shows parts of a whole and this has negative values",
					Advice: "Draw it as a bar chart, which has room below the axis.",
				}
			}
			total += p.Y
		}
		if total == 0 {
			return &Unplottable{Why: "every value is zero, so there are no parts to show"}
		}
	case StackedBar:
		// Stacking adds values together, which means something only if they
		// are all the same sign: a stack of +5 and -5 is a bar of zero
		// height standing for two numbers that are not zero.
		for _, s := range series {
			for _, p := range s.Points {
				if p.Y < 0 {
					return &Unplottable{
						Why:    "stacking adds values together and this has negative ones",
						Advice: "Draw it as a grouped bar chart, where each value has its own bar.",
					}
				}
			}
		}
	case Histogram:
		if len(series) != 1 {
			return &Unplottable{
				Why:    "a histogram counts one set of values and this has " + plural(len(series), "series"),
				Advice: "Draw one column at a time.",
			}
		}
	}
	return nil
}

func countPoints(series []Series) int {
	n := 0
	for _, s := range series {
		n += len(s.Points)
	}
	return n
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %s", n, what)
}

// Bounds are the data limits a set of series needs an axis to cover.
type Bounds struct{ MinX, MaxX, MinY, MaxY float64 }

// Extent is the limits of these series, drawn this way.
//
// A bar chart's Y axis reaches zero whatever the data does: a bar's length
// is its value, so an axis starting at 90 draws 91 as ten times 90.1. A line
// chart's does not, because a line is about change and cutting the axis is
// how change is seen.
func Extent(k Kind, series []Series) (Bounds, error) {
	if err := Check(k, series); err != nil {
		return Bounds{}, err
	}
	b := Bounds{MinX: math.Inf(1), MaxX: math.Inf(-1), MinY: math.Inf(1), MaxY: math.Inf(-1)}
	for _, s := range series {
		for _, p := range s.Points {
			b.MinX, b.MaxX = math.Min(b.MinX, p.X), math.Max(b.MaxX, p.X)
			b.MinY, b.MaxY = math.Min(b.MinY, p.Y), math.Max(b.MaxY, p.Y)
		}
	}
	if k == StackedBar {
		// A stack is as tall as its parts together, not as its tallest part.
		b.MaxY = math.Max(b.MaxY, tallestStack(series))
	}
	if zeroed(k) {
		b.MinY = math.Min(b.MinY, 0)
		b.MaxY = math.Max(b.MaxY, 0)
	}
	if b.MinY == b.MaxY {
		// A flat series still needs an axis with room in it, or every value
		// sits on one line and the chart says nothing.
		b.MinY, b.MaxY = b.MinY-1, b.MaxY+1
	}
	if b.MinX == b.MaxX {
		b.MinX, b.MaxX = b.MinX-1, b.MaxX+1
	}
	return b, nil
}

// zeroed reports the kinds whose axis must reach zero, which is every kind
// drawn as a length from the axis.
func zeroed(k Kind) bool {
	return k == Bar || k == StackedBar || k == Area || k == Histogram
}

// tallestStack is the height of the tallest column once its parts are added.
func tallestStack(series []Series) float64 {
	sum := map[float64]float64{}
	for _, s := range series {
		for _, p := range s.Points {
			sum[p.X] += p.Y
		}
	}
	tallest := 0.0
	for _, v := range sum {
		tallest = math.Max(tallest, v)
	}
	return tallest
}

// Bin is one bar of a histogram: the values from Min up to but not including
// Max, and how many there were.
type Bin struct {
	Min, Max float64
	Count    int
}

// Histogram counts values into bins whose edges are round numbers.
//
// The edges matter as much as the counts. Bins running from 3.7194 to
// 12.8831 are bins nobody can say anything about, and a histogram exists to
// be said something about — so the width is chosen the way an axis's ticks
// are, and the first edge is a multiple of it.
func Bins(values []float64, target int) []Bin {
	if len(values) == 0 || target < 1 {
		return nil
	}
	lo, hi := values[0], values[0]
	for _, v := range values[1:] {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	// Every value the same needs no special case: a span of nothing asks for
	// a width of nothing, niceStep answers 1, and the one bin that follows
	// holds them all between two round numbers. The special case this used
	// to have answered an interval from the value to the next float after
	// it, which is an interval nobody can read.
	width := niceStep((hi - lo) / float64(target))
	start := math.Floor(lo/width) * width
	n := int(math.Ceil((hi - start) / width))
	if n < 1 {
		n = 1
	}

	out := make([]Bin, n)
	for i := range out {
		out[i] = Bin{Min: start + float64(i)*width, Max: start + float64(i+1)*width}
	}
	for _, v := range values {
		i := int((v - start) / width)
		if i >= n {
			// The largest value falls on the last edge, which belongs in
			// the bin below it: a value is never in a bin that ends at it.
			i = n - 1
		}
		if i < 0 {
			i = 0
		}
		out[i].Count++
	}
	// Trailing empty bins are an axis stretched past the data.
	for len(out) > 1 && out[len(out)-1].Count == 0 {
		out = out[:len(out)-1]
	}
	return out
}

// Slice is one wedge of a pie: a name, a value, and the share of the whole
// it is.
type Slice struct {
	Name  string
	Value float64
	Share float64
}

// PieLimit is how many wedges a pie is drawn with before the rest are
// gathered together.
//
// Past a dozen, a pie is a colour wheel: the wedges are too narrow to
// compare and too many to label. The tail is gathered rather than dropped,
// and says how many it holds, because a chart that quietly left data out
// would be the one thing this must not do.
const PieLimit = 12

// Slices turns a series into wedges, largest first, with the tail gathered.
func Slices(s Series, names func(int) string) []Slice {
	total := 0.0
	for _, p := range s.Points {
		total += p.Y
	}
	if total == 0 {
		return nil
	}
	out := make([]Slice, 0, len(s.Points))
	for i, p := range s.Points {
		out = append(out, Slice{Name: nameOf(names, i), Value: p.Y, Share: p.Y / total})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Value > out[j].Value })
	if len(out) <= PieLimit {
		return out
	}
	rest := out[PieLimit-1:]
	gathered := Slice{Name: fmt.Sprintf("Other (%d)", len(rest))}
	for _, s := range rest {
		gathered.Value += s.Value
		gathered.Share += s.Share
	}
	return append(out[:PieLimit-1:PieLimit-1], gathered)
}

func nameOf(names func(int) string, i int) string {
	if names == nil {
		return fmt.Sprint(i)
	}
	return names(i)
}
