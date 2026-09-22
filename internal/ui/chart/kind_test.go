package chart

import (
	"errors"
	"image"
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The kinds a result can be drawn as (FR-11.1).

func oneSeries(name string, ys ...float64) Series {
	s := Series{Name: name}
	for i, y := range ys {
		s.Points = append(s.Points, Point{X: float64(i), Y: y, Index: i})
	}
	return s
}

// A pie shows parts of a whole, so a negative part is refused — and the
// refusal says what to draw instead, because somebody asking for a pie wants
// a chart rather than a no.
func TestWhatAPieWillNotDraw(t *testing.T) {
	for _, c := range []struct {
		what   string
		series []Series
		says   string
	}{
		{"negative values", []Series{oneSeries("a", 5, -3)}, "negative"},
		{"two series", []Series{oneSeries("a", 1), oneSeries("b", 2)}, "one set of parts"},
		{"every value zero", []Series{oneSeries("a", 0, 0)}, "no parts"},
	} {
		err := Check(Pie, c.series)
		var bad *Unplottable
		if !errors.As(err, &bad) {
			t.Errorf("%s: it said %v", c.what, err)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: it said %q", c.what, err)
		}
		if c.what != "every value zero" && bad.Advice == "" {
			t.Errorf("%s: it says what is wrong and not what to do instead", c.what)
		}
	}
	// And a pie of parts is drawn.
	if err := Check(Pie, []Series{oneSeries("a", 1, 2, 3)}); err != nil {
		t.Errorf("a pie of parts said %v", err)
	}
}

// Stacking adds values together, which means something only if they have the
// same sign: a stack of +5 and −5 is a bar of nothing standing for two
// numbers that are not nothing.
func TestWhatAStackWillNotDraw(t *testing.T) {
	err := Check(StackedBar, []Series{oneSeries("a", 5), oneSeries("b", -5)})
	if !strings.Contains(errText(err), "negative") || !strings.Contains(errText(err), "grouped bar") {
		t.Errorf("it said %v", err)
	}
	if err := Check(StackedBar, []Series{oneSeries("a", 5), oneSeries("b", 5)}); err != nil {
		t.Errorf("a stack of positives said %v", err)
	}
}

// A histogram counts one set of values.
func TestAHistogramCountsOneThing(t *testing.T) {
	if err := Check(Histogram, []Series{oneSeries("a", 1), oneSeries("b", 2)}); !strings.Contains(errText(err), "one set") {
		t.Errorf("it said %v", err)
	}
}

// A chart of nothing is not a failure: a query that returned no rows has no
// chart, and saying so is the answer.
func TestAChartOfNothing(t *testing.T) {
	for _, k := range Kinds {
		if err := Check(k, nil); !errors.Is(err, ErrNoData) {
			t.Errorf("%s of nothing said %v", k, err)
		}
		if err := Check(k, []Series{{Name: "a"}}); !errors.Is(err, ErrNoData) {
			t.Errorf("%s of an empty series said %v", k, err)
		}
	}
}

// A bar's length is its value, so its axis reaches zero: an axis starting at
// 90 draws 91 as ten times 90.1.
func TestWhichAxesReachZero(t *testing.T) {
	high := []Series{oneSeries("a", 90, 91, 92)}
	for _, k := range []Kind{Bar, StackedBar, Area, Histogram} {
		b, err := Extent(k, high)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if b.MinY != 0 {
			t.Errorf("%s starts at %v", k, b.MinY)
		}
	}
	// A line is about change, and cutting the axis is how change is seen.
	for _, k := range []Kind{Line, Scatter} {
		b, err := Extent(k, high)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if b.MinY == 0 {
			t.Errorf("%s was stretched to zero", k)
		}
	}
}

// A stack is as tall as its parts together, not as its tallest part.
func TestAStacksAxisHoldsTheWholeStack(t *testing.T) {
	b, err := Extent(StackedBar, []Series{oneSeries("a", 3, 3), oneSeries("b", 4, 4)})
	if err != nil {
		t.Fatal(err)
	}
	if b.MaxY < 7 {
		t.Errorf("its axis reaches %v, and a column is 7 tall", b.MaxY)
	}
}

// A flat series still needs an axis with room in it, or every value sits on
// one line and the chart says nothing.
func TestAFlatSeriesStillHasAnAxis(t *testing.T) {
	b, err := Extent(Line, []Series{{Name: "a", Points: []Point{{X: 1, Y: 5}}}})
	if err != nil {
		t.Fatal(err)
	}
	if b.MinY >= b.MaxY || b.MinX >= b.MaxX {
		t.Errorf("it is %+v", b)
	}
}

// A histogram's edges are round numbers: bins running from 3.7194 to 12.8831
// are bins nobody can say anything about.
func TestHistogramEdgesAreNumbersSomebodyCanRead(t *testing.T) {
	// Starting somewhere that is not already a round number, or a first
	// edge taken straight from the data would look correct by accident.
	var values []float64
	for i := 0; i < 100; i++ {
		values = append(values, 3.7+float64(i)*1.37)
	}
	bins := Bins(values, 10)
	if len(bins) < 2 {
		t.Fatalf("it made %d bins", len(bins))
	}
	width := bins[0].Max - bins[0].Min
	for _, b := range bins {
		if got := b.Max - b.Min; math.Abs(got-width) > 1e-9 {
			t.Errorf("a bin is %v wide and another is %v", got, width)
		}
	}
	// A round width, and a first edge that is a multiple of it.
	if !isNice(width) {
		t.Errorf("the bins are %v wide", width)
	}
	if got := math.Mod(bins[0].Min, width); math.Abs(got) > 1e-9 {
		t.Errorf("the first bin starts at %v, which is not a multiple of %v", bins[0].Min, width)
	}
	// Every value is counted exactly once.
	total := 0
	for _, b := range bins {
		total += b.Count
	}
	if total != len(values) {
		t.Errorf("it counted %d of %d values", total, len(values))
	}
}

func isNice(w float64) bool {
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		e := math.Log10(w / m)
		if math.Abs(e-math.Round(e)) < 1e-9 {
			return true
		}
	}
	return false
}

// A value never falls in a bin that ends at it, and the largest value is
// still counted.
func TestTheLargestValueIsInABin(t *testing.T) {
	bins := Bins([]float64{0, 5, 10}, 2)
	total := 0
	for _, b := range bins {
		total += b.Count
	}
	if total != 3 {
		t.Errorf("it counted %d of 3: %+v", total, bins)
	}
	for _, b := range bins {
		if b.Min >= b.Max {
			t.Errorf("a bin is %v to %v", b.Min, b.Max)
		}
	}
}

// Every value the same is one bin holding all of them, not a division by
// nothing.
func TestAHistogramOfOneValueOverAndOver(t *testing.T) {
	bins := Bins([]float64{7, 7, 7}, 10)
	if len(bins) != 1 || bins[0].Count != 3 {
		t.Fatalf("it made %+v", bins)
	}
	b := bins[0]
	// An interval with a left and a right, holding the value it counted.
	switch {
	case !(b.Min < b.Max):
		t.Errorf("its one bin is %v to %v", b.Min, b.Max)
	case math.IsNaN(b.Min) || math.IsNaN(b.Max) || math.IsInf(b.Min, 0) || math.IsInf(b.Max, 0):
		t.Errorf("its one bin is %v to %v", b.Min, b.Max)
	case !(b.Min <= 7 && 7 < b.Max):
		t.Errorf("its one bin is %v to %v, and does not hold 7", b.Min, b.Max)
	}
}

func TestAHistogramOfNothing(t *testing.T) {
	if got := Bins(nil, 10); got != nil {
		t.Errorf("it made %+v", got)
	}
	if got := Bins([]float64{1, 2}, 0); got != nil {
		t.Errorf("no bins asked for made %+v", got)
	}
}

// Past a dozen wedges a pie is a colour wheel. The tail is gathered rather
// than dropped, and says how many it holds, because a chart that quietly
// left data out is the one thing this must not do.
func TestAPieGathersItsTailRatherThanDroppingIt(t *testing.T) {
	var ys []float64
	for i := 0; i < 40; i++ {
		ys = append(ys, float64(40-i))
	}
	s := oneSeries("a", ys...)
	slices := Slices(s, func(i int) string { return "n" + strconv.Itoa(i) })

	if len(slices) != PieLimit {
		t.Fatalf("it made %d wedges, want %d", len(slices), PieLimit)
	}
	last := slices[len(slices)-1]
	if !strings.Contains(last.Name, "Other") || !strings.Contains(last.Name, "29") {
		t.Errorf("the gathered wedge is called %q", last.Name)
	}
	// Every value is still in the pie: the shares add to one.
	total := 0.0
	for _, w := range slices {
		total += w.Share
	}
	if math.Abs(total-1) > 1e-9 {
		t.Errorf("the wedges are %v of the whole", total)
	}
}

// Wedges are largest first, because a pie is read clockwise from the top and
// the eye compares neighbours.
func TestAPieIsLargestFirst(t *testing.T) {
	got := Slices(oneSeries("a", 1, 9, 5), nil)
	var values []float64
	for _, w := range got {
		values = append(values, w.Value)
	}
	if !slices.IsSortedFunc(values, func(a, b float64) int {
		switch {
		case a > b:
			return -1
		case a < b:
			return 1
		}
		return 0
	}) {
		t.Errorf("it made %v", values)
	}
	// And a small pie is not gathered at all.
	if len(got) != 3 {
		t.Errorf("it made %d wedges of 3 values", len(got))
	}
}

func TestAPieOfNothing(t *testing.T) {
	if got := Slices(oneSeries("a", 0, 0), nil); got != nil {
		t.Errorf("it made %+v", got)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Drawing the shapes.

func canvasOf(t *testing.T, w, h int) *image.NRGBA {
	t.Helper()
	return NewCanvas(w, h)
}

// painted counts the pixels a drawing put down, which is how a test says
// something was drawn without asserting which pixels.
func painted(img *image.NRGBA) int {
	n := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 0 {
			n++
		}
	}
	return n
}

var ink = color.NRGBA{R: 20, G: 40, B: 200, A: 255}

// A bar reaches the axis, so a negative value hangs below it rather than
// growing upward from the floor.
func TestABarHangsBelowTheAxisWhereTheValueIsNegative(t *testing.T) {
	xs := Scale{Min: -1, Max: 2, Pixels: 100}
	ys := Scale{Min: -10, Max: 10, Pixels: 100, Invert: true}
	axis := int(ys.Project(0))

	up := canvasOf(t, 100, 100)
	RasterBars(up, []Point{{X: 0, Y: 5}}, xs, ys, ink, 10)
	down := canvasOf(t, 100, 100)
	RasterBars(down, []Point{{X: 0, Y: -5}}, xs, ys, ink, 10)

	if aboveAxis(up, axis) == 0 || belowAxis(up, axis) != 0 {
		t.Errorf("a positive bar has %d above and %d below", aboveAxis(up, axis), belowAxis(up, axis))
	}
	if belowAxis(down, axis) == 0 || aboveAxis(down, axis) != 0 {
		t.Errorf("a negative bar has %d above and %d below", aboveAxis(down, axis), belowAxis(down, axis))
	}
}

func aboveAxis(img *image.NRGBA, axis int) int { return inRows(img, 0, axis) }
func belowAxis(img *image.NRGBA, axis int) int { return inRows(img, axis+1, img.Bounds().Dy()) }

func inRows(img *image.NRGBA, from, to int) int {
	n := 0
	for y := from; y < to && y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.NRGBAAt(x, y).A > 0 {
				n++
			}
		}
	}
	return n
}

// A stack stands on what is already there, so two layers of five are ten
// tall and not five.
func TestAStackStandsOnWhatIsBelowIt(t *testing.T) {
	xs := Scale{Min: -1, Max: 2, Pixels: 100}
	ys := Scale{Min: 0, Max: 20, Pixels: 100, Invert: true}
	img := canvasOf(t, 100, 100)
	base := map[float64]float64{}

	RasterStack(img, []Point{{X: 0, Y: 5}}, xs, ys, ink, 10, base)
	one := painted(img)
	RasterStack(img, []Point{{X: 0, Y: 5}}, xs, ys, ink, 10, base)
	two := painted(img)

	if base[0] != 10 {
		t.Errorf("after two layers of five the column is %v tall", base[0])
	}
	// The second layer painted about as much again, rather than over the
	// first.
	if two < one*2-8 {
		t.Errorf("one layer painted %d pixels and two painted %d", one, two)
	}
}

// A histogram's bars touch, because the values between two edges are one
// continuous run; gaps would read as categories.
func TestAHistogramsBarsTouch(t *testing.T) {
	bins := []Bin{{Min: 0, Max: 5, Count: 3}, {Min: 5, Max: 10, Count: 4}}
	xs := Scale{Min: 0, Max: 10, Pixels: 100}
	ys := Scale{Min: 0, Max: 5, Pixels: 100, Invert: true}
	img := canvasOf(t, 100, 100)
	RasterHistogram(img, bins, xs, ys, ink)

	// Along the row just above the axis, every column between the first and
	// last edge is painted.
	y := int(ys.Project(1))
	for x := 1; x < 99; x++ {
		if img.NRGBAAt(x, y).A == 0 {
			t.Fatalf("there is a gap at %d", x)
		}
	}
}

// An empty bin draws nothing: a bar of height zero would read as a bar.
func TestAnEmptyBinDrawsNothing(t *testing.T) {
	xs := Scale{Min: 0, Max: 10, Pixels: 100}
	// An axis with room below zero, so that a bar of no height would have
	// somewhere to be drawn rather than being clipped off the bottom.
	ys := Scale{Min: -5, Max: 5, Pixels: 100, Invert: true}
	img := canvasOf(t, 100, 100)
	RasterHistogram(img, []Bin{{Min: 0, Max: 5, Count: 0}}, xs, ys, ink)
	if got := painted(img); got != 0 {
		t.Errorf("an empty bin painted %d pixels", got)
	}
}

// A pie starts at the top and goes clockwise, because that is how every pie
// anybody has read is drawn.
func TestAPieStartsAtTheTopAndGoesClockwise(t *testing.T) {
	img := canvasOf(t, 101, 101)
	red := color.NRGBA{R: 255, A: 255}
	blue := color.NRGBA{B: 255, A: 255}
	RasterPie(img, []Slice{{Share: 0.25}, {Share: 0.75}}, 50, 50, 40,
		[]color.NRGBA{red, blue})

	// A quarter from the top clockwise is the upper right.
	if got := img.NRGBAAt(70, 35); got.R == 0 {
		t.Errorf("the upper right is %+v, and the first wedge is red", got)
	}
	// Anywhere else in the circle is the second wedge.
	if got := img.NRGBAAt(30, 35); got.B == 0 {
		t.Errorf("the upper left is %+v, and the second wedge is blue", got)
	}
}

// A mark too small to round to a pixel is still a mark: drawing nothing
// would say the value is absent.
func TestAValueTooSmallToSeeIsStillDrawn(t *testing.T) {
	xs := Scale{Min: -1, Max: 2, Pixels: 100}
	ys := Scale{Min: 0, Max: 1000000, Pixels: 100, Invert: true}
	img := canvasOf(t, 100, 100)
	RasterBars(img, []Point{{X: 0, Y: 1}}, xs, ys, ink, 4)
	if painted(img) == 0 {
		t.Error("a very small value drew nothing at all")
	}

	// And a rectangle with no width or height at all still puts a pixel
	// down, which is what that promise rests on.
	flat := canvasOf(t, 100, 100)
	fillRect(flat, 50, 50, 50, 50, ink)
	if painted(flat) == 0 {
		t.Error("a rectangle of nothing drew nothing at all")
	}
}

// Bars are separated, which is what makes a bar chart read as separate
// values rather than as a filled area.
func TestBarsAreSeparated(t *testing.T) {
	if got := BarWidth(100, 10); got >= 10 {
		t.Errorf("ten bars across a hundred pixels are %v wide", got)
	}
	// However many there are, a bar is at least a pixel.
	if got := BarWidth(100, 1000); got < 1 {
		t.Errorf("a thousand bars are %v wide", got)
	}
	if got := BarWidth(100, 0); got != 0 {
		t.Errorf("no bars are %v wide", got)
	}
}

// An area is filled between the line and the axis, which is the shape a line
// alone does not have.
func TestAnAreaIsFilledToTheAxis(t *testing.T) {
	xs := Scale{Min: 0, Max: 10, Pixels: 100}
	ys := Scale{Min: 0, Max: 10, Pixels: 100, Invert: true}
	line := canvasOf(t, 100, 100)
	RasterLine(line, []Point{{X: 0, Y: 5}, {X: 10, Y: 5}}, xs, ys, ink)
	area := canvasOf(t, 100, 100)
	RasterArea(area, []Point{{X: 0, Y: 5}, {X: 10, Y: 5}}, xs, ys, ink)

	if painted(area) <= painted(line) {
		t.Errorf("an area painted %d pixels and a line painted %d", painted(area), painted(line))
	}
	// And it reaches the axis.
	if area.NRGBAAt(50, 99).A == 0 {
		t.Error("the fill does not reach the axis")
	}
}

// An area of one point is nothing to fill between.
func TestAnAreaOfOnePoint(t *testing.T) {
	img := canvasOf(t, 100, 100)
	RasterArea(img, []Point{{X: 0, Y: 5}}, Scale{Max: 10, Pixels: 100},
		Scale{Max: 10, Pixels: 100, Invert: true}, ink)
	if painted(img) != 0 {
		t.Error("one point was filled to the axis")
	}
}
