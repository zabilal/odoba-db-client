package chart

import (
	"image/color"
	"math"
	"strconv"
	"strings"
	"testing"
)

// The frame of a chart: where the plot goes, what the axes cover, and what
// is written on them.

func plain(k Kind, series ...Series) Chart {
	return Chart{Kind: k, Series: series, Colours: []color.NRGBA{{R: 1, A: 255}, {G: 1, A: 255}},
		Axis: color.NRGBA{A: 255}, Grid: color.NRGBA{A: 40}, Background: color.NRGBA{R: 255, G: 255, B: 255, A: 255}}
}

// Everything the frame is made of has to be inside the picture: a label half
// off the edge is a number somebody cannot read.
func TestThePlotLeavesRoomForWhatIsWrittenRoundIt(t *testing.T) {
	c := plain(Line, oneSeries("v", 1, 5, 3))
	c.XTitle, c.YTitle = "day", "count"
	f, err := Layout(c, 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	if f.Plot.Min.X <= 0 || f.Plot.Min.Y <= 0 {
		t.Errorf("plot starts at %v, which leaves no room for the labels", f.Plot.Min)
	}
	if f.Plot.Max.X >= 600 || f.Plot.Max.Y >= 400 {
		t.Errorf("plot ends at %v, which is at or past the edge of 600x400", f.Plot.Max)
	}
	if f.W != 600 || f.H != 400 {
		t.Errorf("frame is %vx%v, want the size it was laid out for", f.W, f.H)
	}
	// With no titles and no key there is nothing above the plot but the
	// topmost tick label, which is centred on the plot's own top edge: half
	// of it has to fit above that or it is cut in two.
	bare, err := Layout(plain(Line, oneSeries("v", 1, 5, 3)), 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	if float64(bare.Plot.Min.Y) < edge+TickTextSize/2 {
		t.Errorf("the plot starts at %d, which leaves no room for the upper half of the topmost label",
			bare.Plot.Min.Y)
	}
}

// Wider numbers need a wider gutter, or the axis is written over the plot.
func TestTheLeftGutterFollowsTheWidthOfItsNumbers(t *testing.T) {
	narrow, err := Layout(plain(Line, oneSeries("v", 1, 2, 3)), 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	wide, err := Layout(plain(Line, oneSeries("v", 1e8, 2e8, 3e8)), 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	if wide.Plot.Min.X <= narrow.Plot.Min.X {
		t.Errorf("gutters: wide %d, narrow %d; wide numbers need more room",
			wide.Plot.Min.X, narrow.Plot.Min.X)
	}
}

// A key takes a row above the plot, and two rows where one will not hold it.
func TestTheKeyTakesRoomAboveThePlot(t *testing.T) {
	one, err := Layout(plain(Line, oneSeries("v", 1, 2)), 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	two, err := Layout(plain(Line, oneSeries("a", 1, 2), oneSeries("b", 3, 4)), 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	if two.Plot.Min.Y <= one.Plot.Min.Y {
		t.Errorf("tops: keyed %d, unkeyed %d; a key needs a row", two.Plot.Min.Y, one.Plot.Min.Y)
	}
	var many []Series
	for _, n := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel"} {
		many = append(many, oneSeries(strings.Repeat(n, 3), 1, 2))
	}
	wrapped, err := Layout(plain(Line, many...), 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	if wrapped.Plot.Min.Y <= two.Plot.Min.Y {
		t.Errorf("tops: wrapped %d, one row %d; a key too wide for a row takes two",
			wrapped.Plot.Min.Y, two.Plot.Min.Y)
	}
}

// A chart with no room is refused rather than laid out inside out.
func TestAChartWithNoRoomIsRefused(t *testing.T) {
	for _, size := range [][2]int{{600, 8}, {8, 400}} {
		if _, err := Layout(plain(Line, oneSeries("v", 1, 2)), size[0], size[1]); err == nil {
			t.Errorf("%dx%d was laid out; there is no room in it", size[0], size[1])
		}
	}
}

// What the kind refuses to draw, the frame refuses to lay out: nothing is
// measured for a picture that will not be drawn.
func TestTheFrameRefusesWhatTheKindRefuses(t *testing.T) {
	if _, err := Layout(plain(Pie, oneSeries("v", 1, -2)), 600, 400); err == nil {
		t.Error("a pie of a negative value was laid out")
	}
	if _, err := Layout(plain(Line), 600, 400); err != ErrNoData {
		t.Errorf("err %v, want ErrNoData", err)
	}
}

// A category is labelled by what it is, not by where it stands.
func TestEveryCategoryIsLabelledAtItsOwnPosition(t *testing.T) {
	c := plain(Bar, oneSeries("v", 4, 9, 2))
	c.Labels = []string{"Mon", "Tue", "Wed"}
	f, err := Layout(c, 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.XTicks) != 3 {
		t.Fatalf("%d ticks, want one per category", len(f.XTicks))
	}
	for i, want := range c.Labels {
		if f.XTicks[i].Label != want || f.XTicks[i].Value != float64(i) {
			t.Errorf("tick %d is %q at %v, want %q at %d", i, f.XTicks[i].Label, f.XTicks[i].Value, want, i)
		}
	}
	// The axis reaches half a step past each end, so an end bar is whole.
	if f.X.Min != -0.5 || f.X.Max != 2.5 {
		t.Errorf("axis runs %v..%v, want -0.5..2.5 so the end bars are whole", f.X.Min, f.X.Max)
	}
}

// A histogram's axes come from its bins, which are worked out once: a bar
// drawn against an axis that binned differently would start in the wrong
// place.
func TestAHistogramsAxesComeFromItsOwnBins(t *testing.T) {
	// Four thousand rows of forty values: the fullest bin holds far more
	// than the largest value is, so an axis of the values rather than of
	// the counts cannot pass for one.
	var s Series
	for i := 0; i < 4000; i++ {
		s.Points = append(s.Points, Point{X: float64(i), Y: float64(i % 40), Index: i})
	}
	f, err := Layout(plain(Histogram, s), 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Bins) == 0 {
		t.Fatal("no bins")
	}
	if f.X.Min != f.Bins[0].Min || f.X.Max != f.Bins[len(f.Bins)-1].Max {
		t.Errorf("axis %v..%v, bins %v..%v; they must be the same line",
			f.X.Min, f.X.Max, f.Bins[0].Min, f.Bins[len(f.Bins)-1].Max)
	}
	tallest := 0
	for _, b := range f.Bins {
		tallest = max(tallest, b.Count)
	}
	if f.Y.Max < float64(tallest) {
		t.Errorf("axis reaches %v, tallest bin is %d", f.Y.Max, tallest)
	}
	if f.Y.Max > float64(tallest)*1.5 {
		t.Errorf("axis reaches %v for a tallest bin of %d; it is measuring something else",
			f.Y.Max, tallest)
	}
	if f.Y.Min != 0 {
		t.Errorf("axis starts at %v; a count starts at none", f.Y.Min)
	}
	// The values are 0..39 and the rows run 0..3999: a histogram counts what
	// is in the column, not which row it was in.
	if f.Bins[len(f.Bins)-1].Max > 60 {
		t.Errorf("the bins reach %v, want them over the values, which end at 39",
			f.Bins[len(f.Bins)-1].Max)
	}
}

// How many bins there are is a property of the data, not of the window. Bins
// that changed as somebody resized would mean the same rows told two
// different stories.
func TestBinsDoNotChangeWithTheSizeOfTheWindow(t *testing.T) {
	var s Series
	for i := 0; i < 900; i++ {
		s.Points = append(s.Points, Point{X: float64(i), Y: math.Mod(float64(i)*1.7, 50), Index: i})
	}
	small, err := Layout(plain(Histogram, s), 300, 200)
	if err != nil {
		t.Fatal(err)
	}
	large, err := Layout(plain(Histogram, s), 1600, 900)
	if err != nil {
		t.Fatal(err)
	}
	if len(small.Bins) != len(large.Bins) {
		t.Errorf("%d bins at 300px, %d at 1600px; the data decides, not the window",
			len(small.Bins), len(large.Bins))
	}
}

func TestBinTargetGrowsWithTheCountAndStops(t *testing.T) {
	if got := binTarget(0); got != 1 {
		t.Errorf("binTarget(0) = %d, want at least one bin", got)
	}
	if binTarget(10_000) <= binTarget(100) {
		t.Error("more values should ask for more bins")
	}
	if got := binTarget(100_000_000); got > 50 {
		t.Errorf("binTarget of a hundred million = %d, want a cap", got)
	}
}

// Bars beside each other are centred on their position, so the label still
// names the group under it.
func TestAGroupOfBarsIsCentredOnItsPosition(t *testing.T) {
	c := plain(Bar, oneSeries("a", 1, 2), oneSeries("b", 3, 4))
	c.Labels = []string{"x", "y"}
	f, err := Layout(c, 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	pts := []Point{{X: 0, Y: 1}}
	left := offsetBars(pts, 0, 2, f)
	right := offsetBars(pts, 1, 2, f)
	mid := (f.X.Project(left[0].X) + f.X.Project(right[0].X)) / 2
	if math.Abs(mid-f.X.Project(0)) > 0.001 {
		t.Errorf("the group sits at %v, the position is at %v", mid, f.X.Project(0))
	}
	if only := offsetBars(pts, 0, 1, f); only[0].X != 0 {
		t.Errorf("one series was moved to %v; there is nothing to stand beside", only[0].X)
	}
}

// Downsampling is for drawing, and only where there is more data than there
// are pixels to draw it in.
func TestADenseSeriesIsReducedToThePixelGridAndASparseOneIsNot(t *testing.T) {
	f, err := Layout(plain(Line, oneSeries("v", 1, 2, 3)), 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	sparse := []Point{{X: 0, Y: 1}, {X: 1, Y: 2}}
	if got := forDrawing(sparse, f); len(got) != 2 {
		t.Errorf("%d points from 2; a short series is drawn as it is", len(got))
	}
	var dense []Point
	for i := 0; i < 50_000; i++ {
		dense = append(dense, Point{X: float64(i), Y: float64(i % 7), Index: i})
	}
	got := forDrawing(dense, f)
	if len(got) >= len(dense) {
		t.Errorf("%d points from %d; a dense series is reduced", len(got), len(dense))
	}
	if len(got) > 2*f.Plot.Dx() {
		t.Errorf("%d points for %d columns; at most two a column", len(got), f.Plot.Dx())
	}
}

// A spike has to survive the reduction, because the spike is usually the row
// somebody is looking for.
func TestReducingForDrawingKeepsTheTallestPoint(t *testing.T) {
	f, err := Layout(plain(Line, oneSeries("v", 1, 2, 3)), 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	var dense []Point
	for i := 0; i < 20_000; i++ {
		dense = append(dense, Point{X: float64(i), Y: 1, Index: i})
	}
	dense[7777].Y = 999
	f.X = Scale{Min: 0, Max: 19_999, Pixels: float64(f.Plot.Dx())}
	tallest := 0.0
	for _, p := range forDrawing(dense, f) {
		tallest = math.Max(tallest, p.Y)
	}
	if tallest != 999 {
		t.Errorf("the tallest drawn point is %v, want the spike at 999", tallest)
	}
}

// A key of one name tells nobody anything: there is nothing to tell it apart
// from.
func TestOneSeriesHasNoKey(t *testing.T) {
	if keys := Keys(plain(Line, oneSeries("only", 1, 2))); len(keys) != 0 {
		t.Errorf("%d keys for one series, want none", len(keys))
	}
	keys := Keys(plain(Line, oneSeries("a", 1), oneSeries("b", 2)))
	if len(keys) != 2 || keys[0].Name != "a" || keys[1].Name != "b" {
		t.Fatalf("keys %+v, want one per series in order", keys)
	}
	if keys[0].Colour == keys[1].Colour {
		t.Error("two series were given one colour")
	}
}

// A pie's key is its wedges, because the whole chart is one series.
func TestAPiesKeyNamesItsWedges(t *testing.T) {
	c := plain(Pie, oneSeries("sales", 3, 1, 2))
	c.Labels = []string{"north", "south", "east"}
	keys := Keys(c)
	if len(keys) != 3 {
		t.Fatalf("%d keys, want one per wedge", len(keys))
	}
	// Largest first, as the wedges are drawn.
	if keys[0].Name != "north" {
		t.Errorf("the first key is %q, want the largest wedge", keys[0].Name)
	}
}

// A wedge is named by which category it is, not by where its row happened to
// fall: two rows can share a category.
func TestAWedgeIsNamedByItsCategory(t *testing.T) {
	c := plain(Pie, Series{Name: "sales", Points: []Point{{X: 2, Y: 5, Index: 0}, {X: 0, Y: 1, Index: 1}}})
	c.Labels = []string{"north", "south", "east"}
	w := Wedges(c)
	if len(w) != 2 || w[0].Name != "east" || w[1].Name != "north" {
		t.Errorf("wedges %+v, want east then north", w)
	}
}

// An axis of times is written as dates. Seconds since 1970 are a number
// nobody reads as a moment.
func TestAnAxisOfMomentsIsWrittenAsDates(t *testing.T) {
	c := plain(Line, oneSeries("v", 1, 2))
	c.Times = true
	got := c.xFormat()(float64(day(3).Unix()))
	if !strings.HasPrefix(got, "2026-01-03") {
		t.Errorf("a moment was written %q", got)
	}
	if got := plain(Line, oneSeries("v", 1)).xFormat()(1234); got != "1234" {
		t.Errorf("a number was written %q", got)
	}
}

func TestAxisNumbersAreWrittenWithoutExponentsOrTrailingZeros(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"0", "0"}, {"1000000", "1000000"}, {"-25", "-25"},
	} {
		v, err := strconv.ParseFloat(tc.in, 64)
		if err != nil {
			t.Fatal(err)
		}
		if got := formatNumber(v); got != tc.want {
			t.Errorf("formatNumber(%v) = %q, want %q", v, got, tc.want)
		}
	}
	if got := formatNumber(0.125); got != "0.125" {
		t.Errorf("formatNumber(0.125) = %q", got)
	}
}

// Labels too close together are thinned, not written over one another.
func TestCrowdedLabelsAreThinned(t *testing.T) {
	var f Frame
	for i := 0; i < 60; i++ {
		f.XTicks = append(f.XTicks, Tick{Value: float64(i), Pixel: float64(i) * 4, Label: "2026-01-01 00:00"})
	}
	got := xLabels(f)
	if len(got) >= len(f.XTicks) {
		t.Errorf("%d labels from %d; crowded labels are thinned", len(got), len(f.XTicks))
	}
	if len(got) < 2 {
		t.Errorf("%d labels; an axis with no numbers on it says nothing", len(got))
	}
	gap := got[1].Pixel - got[0].Pixel
	if gap < widest(f.XTicks, TickTextSize) {
		t.Errorf("labels %v apart and %v wide; they would touch", gap, widest(f.XTicks, TickTextSize))
	}
	// Room enough, and every tick keeps its label.
	roomy := Frame{XTicks: []Tick{{Pixel: 0, Label: "1"}, {Pixel: 300, Label: "2"}}}
	if len(xLabels(roomy)) != 2 {
		t.Error("labels far apart were thinned anyway")
	}
	// One tick has nothing to be crowded by, and is not measured against a
	// second that is not there.
	if got := xLabels(Frame{XTicks: []Tick{{Pixel: 10, Label: "only"}}}); len(got) != 1 {
		t.Errorf("%d labels from one tick", len(got))
	}
	if got := xLabels(Frame{}); len(got) != 0 {
		t.Errorf("%d labels from no ticks", len(got))
	}
}

// A pie has no axes, so it is given none of their room.
func TestAPieIsGivenNoRoomForAxesItDoesNotHave(t *testing.T) {
	pie, err := Layout(plain(Pie, oneSeries("v", 1, 2, 3)), 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	if len(pie.XTicks) != 0 || len(pie.YTicks) != 0 {
		t.Errorf("a pie was given %d and %d ticks", len(pie.XTicks), len(pie.YTicks))
	}
	line, err := Layout(plain(Line, oneSeries("v", 1, 2, 3)), 600, 400)
	if err != nil {
		t.Fatal(err)
	}
	if pie.Plot.Dx() <= line.Plot.Dx() {
		t.Errorf("pie plot %d wide, line plot %d; a pie needs no gutter", pie.Plot.Dx(), line.Plot.Dx())
	}
}

// The marks are drawn into an image the size of the plot, whatever the kind.
func TestEveryKindDrawsIntoThePlot(t *testing.T) {
	for _, k := range Kinds {
		c := plain(k, oneSeries("v", 3, 1, 4, 1, 5))
		c.Labels = []string{"a", "b", "c", "d", "e"}
		f, err := Layout(c, 500, 300)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		img := Raster(c, f)
		if img.Bounds().Dx() != f.Plot.Dx() || img.Bounds().Dy() != f.Plot.Dy() {
			t.Errorf("%s: marks are %v, the plot is %v", k, img.Bounds(), f.Plot)
		}
		if painted(img) == 0 {
			t.Errorf("%s: nothing was drawn", k)
		}
	}
}
