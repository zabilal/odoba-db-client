package chart

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
)

// Saying the drawing once: what a screen, a PNG and an SVG all read.

func drawn(c Chart, w, h int, t *testing.T) (Frame, scene.Scene) {
	t.Helper()
	f, err := Layout(c, w, h)
	if err != nil {
		t.Fatal(err)
	}
	return f, Draw(c, f)
}

func shapesOf[T scene.Shape](s scene.Scene) []T {
	var out []T
	for _, sh := range s.Shapes {
		if v, ok := sh.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func labelsOf(s scene.Scene) []string {
	var out []string
	for _, t := range shapesOf[scene.Text](s) {
		out = append(out, t.S)
	}
	return out
}

// The marks are one image, in the plot, at the plot's size.
func TestTheMarksAreOneImageFillingThePlot(t *testing.T) {
	f, s := drawn(plain(Line, oneSeries("v", 1, 5, 3)), 600, 400, t)
	imgs := shapesOf[scene.Raster](s)
	if len(imgs) != 1 {
		t.Fatalf("%d images, want one layer of marks", len(imgs))
	}
	r := imgs[0]
	if r.X != float64(f.Plot.Min.X) || r.Y != float64(f.Plot.Min.Y) {
		t.Errorf("marks at (%v,%v), plot at %v", r.X, r.Y, f.Plot.Min)
	}
	if r.W != float64(f.Plot.Dx()) || r.H != float64(f.Plot.Dy()) {
		t.Errorf("marks are %vx%v, plot is %dx%d", r.W, r.H, f.Plot.Dx(), f.Plot.Dy())
	}
	if r.Img == nil || r.Img.Bounds().Dx() != f.Plot.Dx() {
		t.Error("the marks were not drawn at the size they are placed at")
	}
	if s.W != f.W || s.H != f.H {
		t.Errorf("scene is %vx%v, frame is %vx%v", s.W, s.H, f.W, f.H)
	}
}

// Every tick is written, and the axes are two lines somebody can see.
func TestEveryTickIsWrittenAndTheAxesAreDrawn(t *testing.T) {
	c := plain(Line, oneSeries("v", 1, 5, 3))
	c.XTitle, c.YTitle = "day", "count"
	f, s := drawn(c, 600, 400, t)
	said := labelsOf(s)
	for _, tick := range f.YTicks {
		if !has(said, tick.Label) {
			t.Errorf("the Y tick %q was not written", tick.Label)
		}
	}
	for _, tick := range xLabels(f) {
		if !has(said, tick.Label) {
			t.Errorf("the X tick %q was not written", tick.Label)
		}
	}
	if !has(said, "day") || !has(said, "count") {
		t.Errorf("titles: %v, want both axes named", said)
	}
	// The topmost label is centred on the top of the plot, so the room for
	// its upper half has to be above it or it is cut in two.
	for _, tx := range shapesOf[scene.Text](s) {
		if tx.Middle && tx.Y-tx.Size/2 < 0 {
			t.Errorf("the label %q reaches %v, above the top of the picture", tx.S, tx.Y-tx.Size/2)
		}
	}
	for _, tx := range shapesOf[scene.Text](s) {
		if tx.S == "count" && tx.Y >= float64(f.Plot.Min.Y) {
			t.Errorf("the upright axis's name is at %v, inside the plot at %d", tx.Y, f.Plot.Min.Y)
		}
	}
	// The two axis lines are the ones that run the length of a side.
	var sides int
	for _, l := range shapesOf[scene.Line](s) {
		down := l.X1 == l.X2 && l.X1 == float64(f.Plot.Min.X)
		along := l.Y1 == l.Y2 && l.Y1 == float64(f.Plot.Max.Y)
		if down || along {
			sides++
		}
	}
	if sides < 2 {
		t.Errorf("%d axis lines, want one down the side and one along the bottom", sides)
	}
}

// A gridline for each Y tick, drawn before the marks so the data covers them
// rather than the other way round.
func TestGridlinesAreUnderTheMarks(t *testing.T) {
	c := plain(Line, oneSeries("v", 1, 5, 3))
	f, s := drawn(c, 600, 400, t)
	drawnYet, grid := false, 0
	for _, sh := range s.Shapes {
		switch v := sh.(type) {
		case scene.Raster:
			drawnYet = true
		case scene.Line:
			if v.Stroke == c.Grid && !drawnYet {
				grid++
			}
		}
	}
	if grid != len(f.YTicks) {
		t.Errorf("%d gridlines before the marks, want %d", grid, len(f.YTicks))
	}
}

// A pie has no axes, so nothing of an axis is drawn for it.
func TestAPieIsDrawnWithoutAxes(t *testing.T) {
	c := plain(Pie, oneSeries("v", 3, 1, 2))
	c.Labels = []string{"a", "b", "c"}
	c.XTitle, c.YTitle = "x", "y"
	_, s := drawn(c, 400, 400, t)
	if lines := shapesOf[scene.Line](s); len(lines) != 0 {
		t.Errorf("%d lines on a pie", len(lines))
	}
	said := labelsOf(s)
	if has(said, "x") || has(said, "y") {
		t.Errorf("a pie was given axis titles: %v", said)
	}
	for _, want := range []string{"a", "b", "c"} {
		if !has(said, want) {
			t.Errorf("the wedge %q is not named; a pie's only labels are its key", want)
		}
	}
}

// The key is a block of colour and a name for each series, above the plot.
func TestTheKeyIsAColouredBlockAndANamePerSeries(t *testing.T) {
	c := plain(Line, oneSeries("orders", 1, 2), oneSeries("refunds", 3, 4))
	f, s := drawn(c, 600, 400, t)
	blocks := shapesOf[scene.Box](s)
	if len(blocks) != 2 {
		t.Fatalf("%d blocks, want one per series", len(blocks))
	}
	for _, b := range blocks {
		if b.Y+b.H > float64(f.Plot.Min.Y) {
			t.Errorf("a key block reaches %v, the plot starts at %d", b.Y+b.H, f.Plot.Min.Y)
		}
	}
	if blocks[0].Fill == blocks[1].Fill {
		t.Error("both series were keyed in one colour")
	}
	said := labelsOf(s)
	if !has(said, "orders") || !has(said, "refunds") {
		t.Errorf("labels %v, want both series named", said)
	}
}

// A key too wide for one row takes two, rather than running off the picture.
func TestAKeyTooWideForOneRowWraps(t *testing.T) {
	var many []Series
	for _, n := range []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel"} {
		many = append(many, oneSeries(strings.Repeat(n, 3), 1, 2))
	}
	_, s := drawn(plain(Line, many...), 600, 400, t)
	rows := map[float64]bool{}
	for _, b := range shapesOf[scene.Box](s) {
		rows[b.Y] = true
		if b.X+b.W > 600 {
			t.Errorf("a key block reaches %v, past the edge of the picture", b.X+b.W)
		}
	}
	if len(rows) < 2 {
		t.Errorf("the key is on %d row(s), want it wrapped", len(rows))
	}
}

// A Y tick label ends at the axis and sits on the line it marks; an X tick
// label is centred under its position.
func TestTickLabelsSitAgainstTheAxis(t *testing.T) {
	f, s := drawn(plain(Line, oneSeries("v", 1, 5, 3)), 600, 400, t)
	var down, along int
	for _, tx := range shapesOf[scene.Text](s) {
		switch {
		case tx.X < float64(f.Plot.Min.X):
			down++
			if tx.Align != scene.Trailing || !tx.Middle {
				t.Errorf("the Y label %q is %+v, want it ending at the axis and on its line", tx.S, tx)
			}
		case tx.Y >= float64(f.Plot.Max.Y):
			along++
			if tx.Align != scene.Center {
				t.Errorf("the X label %q is %+v, want it centred on its tick", tx.S, tx)
			}
		}
	}
	if down != len(f.YTicks) {
		t.Errorf("%d labels left of the plot, want one per Y tick (%d)", down, len(f.YTicks))
	}
	if along != len(xLabels(f)) {
		t.Errorf("%d labels under the axis, want one per X tick (%d)", along, len(xLabels(f)))
	}
}

// An axis of sixty dates is sixty labels written over one another unless
// the drawing uses the thinned set the frame worked out.
func TestAnAxisOfManyCategoriesIsNotWrittenOverItself(t *testing.T) {
	var ys []float64
	var labels []string
	for i := 0; i < 60; i++ {
		ys = append(ys, float64(i%7))
		labels = append(labels, fmt.Sprintf("2026-01-%02d 09:30", i%28+1))
	}
	c := plain(Bar, oneSeries("v", ys...))
	c.Labels = labels
	f, s := drawn(c, 600, 400, t)
	under := 0
	for _, tx := range shapesOf[scene.Text](s) {
		// Left of the plot is the upright axis, whose bottom label sits on
		// the same line as these do.
		if tx.X >= float64(f.Plot.Min.X) && tx.Y >= float64(f.Plot.Max.Y) {
			under++
		}
	}
	if under != len(xLabels(f)) {
		t.Errorf("%d labels under the axis, want the %d that fit", under, len(xLabels(f)))
	}
	if under >= len(f.XTicks) {
		t.Errorf("%d labels for %d categories; they would be written over one another",
			under, len(f.XTicks))
	}
}

// Nothing in a chart is written in a colour that is not the chart's.
func TestEverythingIsDrawnInTheChartsOwnColours(t *testing.T) {
	c := plain(Line, oneSeries("a", 1, 2), oneSeries("b", 3, 4))
	_, s := drawn(c, 600, 400, t)
	if s.Background != c.Background {
		t.Errorf("background %v, want %v", s.Background, c.Background)
	}
	for _, tx := range shapesOf[scene.Text](s) {
		if tx.Fill != c.Axis {
			t.Errorf("%q is written in %v, want the axis colour %v", tx.S, tx.Fill, c.Axis)
		}
	}
	for _, b := range shapesOf[scene.Box](s) {
		if b.Fill != c.Colours[0] && b.Fill != c.Colours[1] {
			t.Errorf("a key block is %v, which is no series' colour", b.Fill)
		}
	}
}

func has(all []string, want string) bool {
	for _, s := range all {
		if s == want || strings.TrimSpace(s) == want {
			return true
		}
	}
	return false
}
