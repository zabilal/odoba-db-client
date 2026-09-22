package chart

import (
	"math"
	"strings"
	"testing"
)

// What is under the cursor. Every kind is pointed at in the way its marks
// are shaped: by column, by proximity, or by containment.

// probed lays a chart out and indexes it.
func probed(t *testing.T, c Chart, w, h int) (Frame, *Probe) {
	t.Helper()
	f, err := Layout(c, w, h)
	if err != nil {
		t.Fatal(err)
	}
	return f, NewProbe(c, f)
}

// at is the picture position of a data point.
func at(f Frame, x, y float64) (float64, float64) {
	return float64(f.Plot.Min.X) + f.X.Project(x), float64(f.Plot.Min.Y) + f.Y.Project(y)
}

// A line is pointed at by column: somebody points at a day, not at a dot.
func TestALineIsReadByColumn(t *testing.T) {
	c := plain(Line, oneSeries("orders", 10, 50, 20))
	f, p := probed(t, c, 600, 400)
	x, y := at(f, 1, 50)
	r, ok := p.At(x, y+60) // well below the line, and still on its column
	if !ok {
		t.Fatal("nothing was read")
	}
	if r.X != 1 || r.Y != 50 {
		t.Errorf("read (%v,%v), want the point at (1,50)", r.X, r.Y)
	}
	if r.Row != 1 {
		t.Errorf("row %d, want the second", r.Row)
	}
	if !strings.Contains(strings.Join(r.Lines, " "), "50") {
		t.Errorf("it says %v, want the value in it", r.Lines)
	}
}

// Between two lines at one column, the one the cursor is nearest.
func TestTheLineNearestTheCursorIsTheOneRead(t *testing.T) {
	c := plain(Line, oneSeries("orders", 10, 90), oneSeries("refunds", 5, 8))
	f, p := probed(t, c, 600, 400)
	x, _ := at(f, 1, 0)

	_, high := at(f, 1, 90)
	r, ok := p.At(x, high)
	if !ok || r.Name != "orders" {
		t.Errorf("near the high line it read %+v, want orders", r)
	}
	_, low := at(f, 1, 8)
	r, ok = p.At(x, low)
	if !ok || r.Name != "refunds" {
		t.Errorf("near the low line it read %+v, want refunds", r)
	}
	if !strings.Contains(strings.Join(r.Lines, " "), "refunds") {
		t.Errorf("it says %v, want the series named where there are several", r.Lines)
	}
}

// One series needs no naming: there is nothing to tell it apart from.
func TestOneSeriesIsNotNamedInItsOwnTooltip(t *testing.T) {
	c := plain(Line, oneSeries("orders", 10, 50))
	f, p := probed(t, c, 600, 400)
	x, y := at(f, 1, 50)
	r, ok := p.At(x, y)
	if !ok {
		t.Fatal("nothing was read")
	}
	if len(r.Lines) != 1 {
		t.Errorf("it says %v, want one line", r.Lines)
	}
}

// A scatter is pointed at by proximity, and a cursor nowhere near a dot is
// pointing at nothing: a tooltip that snapped to a point two hundred pixels
// away would be worse than none.
func TestAScatterIsReadByProximity(t *testing.T) {
	c := plain(Scatter, Series{Name: "latency", Points: []Point{
		{X: 0, Y: 0, Index: 0}, {X: 10, Y: 100, Index: 1}}})
	f, p := probed(t, c, 600, 400)
	x, y := at(f, 10, 100)
	if r, ok := p.At(x-2, y+2); !ok || r.Row != 1 {
		t.Errorf("on the dot it read %+v, %v", r, ok)
	}
	if _, ok := p.At(x-120, y+90); ok {
		t.Error("a cursor nowhere near a dot read one anyway")
	}
}

// A bar is pointed at by containment: the shape is large, and the cursor is
// either in it or not.
func TestABarIsReadWhereItStands(t *testing.T) {
	c := plain(Bar, oneSeries("a", 10, 90), oneSeries("b", 80, 20))
	c.Labels = []string{"Mon", "Tue"}
	f, p := probed(t, c, 600, 400)

	// The bars of one position stand beside each other, so each is read at
	// its own place rather than at the category's.
	aAt := f.X.Project(0) - f.Bars/2
	bAt := f.X.Project(0) + f.Bars/2
	_, y := at(f, 0, 5)

	r, ok := p.At(float64(f.Plot.Min.X)+aAt, y)
	if !ok || r.Name != "a" || r.Y != 10 {
		t.Errorf("on the left bar it read %+v, %v", r, ok)
	}
	r, ok = p.At(float64(f.Plot.Min.X)+bAt, y)
	if !ok || r.Name != "b" || r.Y != 80 {
		t.Errorf("on the right bar it read %+v, %v", r, ok)
	}
	// Above every bar of the pair is above the chart's data.
	_, high := at(f, 0, 95)
	if _, ok := p.At(float64(f.Plot.Min.X)+aAt, high); ok {
		t.Error("the air above a bar was read as the bar")
	}
	// The mark goes on the bar, not on the category's own position.
	r, _ = p.At(float64(f.Plot.Min.X)+bAt, y)
	if math.Abs(r.PX-bAt) > 1 {
		t.Errorf("the mark is at %v, the bar is at %v", r.PX, bAt)
	}
	if !strings.Contains(strings.Join(r.Lines, " "), "Mon") {
		t.Errorf("it says %v, want the category named", r.Lines)
	}
}

// A stack's layers stand on one another, so which layer the cursor is in is
// what it is pointing at — not whichever series owns the nearest number.
func TestAStackIsReadByLayer(t *testing.T) {
	c := plain(StackedBar, oneSeries("a", 10), oneSeries("b", 40))
	c.Labels = []string{"Mon"}
	f, p := probed(t, c, 600, 400)
	x := float64(f.Plot.Min.X) + f.X.Project(0)

	_, low := at(f, 0, 5)
	if r, ok := p.At(x, low); !ok || r.Name != "a" {
		t.Errorf("in the bottom layer it read %+v, %v", r, ok)
	}
	_, high := at(f, 0, 30)
	r, ok := p.At(x, high)
	if !ok || r.Name != "b" || r.Y != 40 {
		t.Errorf("in the top layer it read %+v, %v", r, ok)
	}
	// The mark sits on the top of the layer, which is where it is drawn.
	if math.Abs(r.PY-f.Y.Project(50)) > 1 {
		t.Errorf("the mark is at %v, the layer's top is at %v", r.PY, f.Y.Project(50))
	}
	_, above := at(f, 0, 60)
	if _, ok := p.At(x, above); ok {
		t.Error("the air above a stack was read as the stack")
	}
}

// A histogram's bar counts an interval, and no single row stands behind it.
func TestAHistogramIsReadAsAnIntervalAndACount(t *testing.T) {
	var s Series
	for i := 0; i < 400; i++ {
		s.Points = append(s.Points, Point{X: float64(i), Y: float64(i % 20), Index: i})
	}
	c := plain(Histogram, s)
	f, p := probed(t, c, 600, 400)
	if len(f.Bins) == 0 {
		t.Fatal("no bins")
	}
	b := f.Bins[0]
	x := float64(f.Plot.Min.X) + (f.X.Project(b.Min)+f.X.Project(b.Max))/2
	y := float64(f.Plot.Min.Y) + f.Y.Project(float64(b.Count)/2)
	r, ok := p.At(x, y)
	if !ok {
		t.Fatal("nothing was read over a bar")
	}
	if r.Row != NoRow {
		t.Errorf("row %d, want none: a count is of many rows", r.Row)
	}
	if r.Y != float64(b.Count) {
		t.Errorf("it read %v, want the count %d", r.Y, b.Count)
	}
	said := strings.Join(r.Lines, " | ")
	if !strings.Contains(said, formatNumber(b.Min)) || !strings.Contains(said, formatNumber(b.Max)) {
		t.Errorf("it says %q, want both edges of the interval", said)
	}
	// Above the bar is not the bar.
	high := float64(f.Plot.Min.Y) + f.Y.Project(float64(b.Count))/2
	if _, ok := p.At(x, high); ok {
		t.Error("the air above a bar was read as the bar")
	}
}

// A wedge is read by the angle the cursor is at, and knows the row it came
// from, which is what makes it something to click.
func TestAWedgeIsReadByItsAngle(t *testing.T) {
	c := plain(Pie, Series{Name: "sales", Points: []Point{
		{X: 0, Y: 75, Index: 4}, {X: 1, Y: 25, Index: 9}}})
	c.Labels = []string{"north", "south"}
	f, p := probed(t, c, 400, 400)
	cx := float64(f.Plot.Min.X) + float64(f.Plot.Dx())/2
	cy := float64(f.Plot.Min.Y) + float64(f.Plot.Dy())/2

	// The first wedge is drawn clockwise from the top, so a little to the
	// right of the centre is in it.
	r, ok := p.At(cx+40, cy-10)
	if !ok || r.Name != "north" {
		t.Errorf("to the right of the top it read %+v, %v", r, ok)
	}
	if r.Row != 4 {
		t.Errorf("row %d, want the row the wedge came from", r.Row)
	}
	if !strings.Contains(strings.Join(r.Lines, " "), "%") {
		t.Errorf("it says %v, want the share in it", r.Lines)
	}
	// Three quarters round is the other wedge.
	if r, ok := p.At(cx-40, cy-10); !ok || r.Name != "south" {
		t.Errorf("to the left of the top it read %+v, %v", r, ok)
	}
	// Outside the circle is outside the pie.
	if _, ok := p.At(cx+300, cy); ok {
		t.Error("something outside the pie was read as a wedge")
	}
}

// A wedge that gathered a tail stands for several rows, so it names none.
func TestAGatheredWedgeNamesNoRow(t *testing.T) {
	// The largest is deliberately not the first row, so that a wedge which
	// never learned where it came from cannot pass by being zero.
	var pts []Point
	for i := 0; i < PieLimit+5; i++ {
		pts = append(pts, Point{X: float64(i), Y: float64(i), Index: i})
	}
	c := plain(Pie, Series{Name: "sales", Points: pts})
	w := Wedges(c)
	if len(w) != PieLimit {
		t.Fatalf("%d wedges, want %d", len(w), PieLimit)
	}
	if w[len(w)-1].Index != NoRow {
		t.Errorf("the gathered wedge names row %d", w[len(w)-1].Index)
	}
	if w[0].Index != PieLimit+4 {
		t.Errorf("the largest wedge names row %d, want the row it came from", w[0].Index)
	}
}

// Outside the plot is outside the data: a tooltip over the axis would name a
// value nothing there stands for.
func TestOutsideThePlotReadsNothing(t *testing.T) {
	c := plain(Line, oneSeries("v", 10, 50, 20))
	f, p := probed(t, c, 600, 400)
	for _, where := range [][2]float64{
		{float64(f.Plot.Min.X) - 5, float64(f.Plot.Min.Y) + 10},
		{float64(f.Plot.Min.X) + 10, float64(f.Plot.Max.Y) + 5},
		{float64(f.Plot.Max.X) + 5, float64(f.Plot.Min.Y) + 10},
	} {
		if _, ok := p.At(where[0], where[1]); ok {
			t.Errorf("(%v,%v) is outside the plot and read something", where[0], where[1])
		}
	}
}

// A reading is resolved against the whole series and never the reduced set
// that was drawn. A tooltip reporting a point the reduction invented would
// show a value that is not in the result.
func TestAReadingComesFromTheWholeSeriesNotTheDrawnOne(t *testing.T) {
	var s Series
	for i := 0; i < 40_000; i++ {
		s.Points = append(s.Points, Point{X: float64(i), Y: float64(i % 13), Index: i})
	}
	s.Points[31_337].Y = 999
	c := plain(Line, s)
	f, p := probed(t, c, 600, 400)
	if drawn := forDrawing(s.Points, f); len(drawn) >= len(s.Points) {
		t.Fatalf("%d points drawn from %d; this test needs a reduced drawing", len(drawn), len(s.Points))
	}
	x, y := at(f, 31_337, 999)
	r, ok := p.At(x, y)
	if !ok {
		t.Fatal("nothing was read")
	}
	if r.Row != 31_337 || r.Y != 999 {
		t.Errorf("read row %d at %v, want row 31337 at 999", r.Row, r.Y)
	}
	// And a point the reduction left out is still readable.
	x, y = at(f, 12_345, float64(12_345%13))
	r, ok = p.At(x, y)
	if !ok || r.Row != 12_345 {
		t.Errorf("read %+v, want row 12345", r)
	}
}

// An axis of moments is said as a date in a tooltip too, and a category by
// its name.
func TestAReadingSaysWhereItWasInTheAxisOwnTerms(t *testing.T) {
	c := plain(Line, oneSeries("v", 1, 2))
	c.Times = true
	c.Series[0].Points[1].X = float64(day(4).Unix())
	f, p := probed(t, c, 600, 400)
	x, y := at(f, c.Series[0].Points[1].X, 2)
	r, ok := p.At(x, y)
	if !ok || !strings.Contains(strings.Join(r.Lines, " "), "2026-01-04") {
		t.Errorf("it says %v, want the moment written as a date", r.Lines)
	}

	bars := plain(Bar, oneSeries("v", 5, 6))
	bars.Labels = []string{"north", "south"}
	if got := Along(bars, 1); got != "south" {
		t.Errorf("position 1 is called %q, want its category", got)
	}
}

// A probe over a chart that cannot be drawn is never built, and a probe over
// nothing answers nothing rather than falling over.
func TestAProbeOverNothing(t *testing.T) {
	var p *Probe
	if _, ok := readingAt(p, 10, 10); ok {
		t.Error("a chart with no probe read something")
	}
	c := plain(Scatter, Series{Name: "v", Points: []Point{{X: 1, Y: 1}}})
	f, probe := probed(t, c, 600, 400)
	if _, ok := probe.At(float64(f.Plot.Min.X)+1, float64(f.Plot.Min.Y)+1); ok {
		t.Error("a corner far from the only dot read it")
	}
}

// The risk the spike was run for: a tooltip that stays accurate and quick at
// a hundred thousand points (FR-11.4).
func benchChart(k Kind, n int) Chart {
	var s Series
	for i := 0; i < n; i++ {
		s.Points = append(s.Points, Point{X: float64(i), Y: math.Sin(float64(i)/900) * 100, Index: i})
	}
	return plain(k, s)
}

func BenchmarkProbe100k(b *testing.B) {
	c := benchChart(Line, 100_000)
	f, err := Layout(c, 1200, 700)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("build", func(b *testing.B) {
		for b.Loop() {
			NewProbe(c, f)
		}
	})
	p := NewProbe(c, f)
	b.Run("read", func(b *testing.B) {
		x0, y0 := float64(f.Plot.Min.X), float64(f.Plot.Min.Y)
		i := 0
		for b.Loop() {
			i++
			p.At(x0+float64(i%f.Plot.Dx()), y0+float64(i%f.Plot.Dy()))
		}
	})
}

func BenchmarkProbeScatter100k(b *testing.B) {
	c := benchChart(Scatter, 100_000)
	f, err := Layout(c, 1200, 700)
	if err != nil {
		b.Fatal(err)
	}
	p := NewProbe(c, f)
	x0, y0 := float64(f.Plot.Min.X), float64(f.Plot.Min.Y)
	i := 0
	for b.Loop() {
		i++
		p.At(x0+float64(i%f.Plot.Dx()), y0+float64(i%f.Plot.Dy()))
	}
}
