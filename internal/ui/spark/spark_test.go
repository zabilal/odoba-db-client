package spark

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
)

// A sparkline: how a number has been moving, in a picture read at a glance
// (FR-13.17).

var ink = color.NRGBA{R: 40, G: 90, B: 200, A: 255}

func sparkOf(values ...float64) Spark {
	return Spark{Values: values, Line: ink,
		Fill:       color.NRGBA{R: 40, G: 90, B: 200, A: 60},
		Background: color.NRGBA{A: 0}}
}

// It is drawn against nought rather than against its own quietest moment: a
// topic going from a thousand to a thousand and one a second is not as busy as
// one going from nothing to a thousand, and a line scaled to its own minimum
// would draw them the same.
func TestASparklineIsDrawnAgainstNothing(t *testing.T) {
	low, high := sparkOf(1000, 1001, 1000).Bounds()
	if low != 0 || high != 1001 {
		t.Errorf("a busy steady line is drawn between %v and %v", low, high)
	}
	low, high = sparkOf(0, 500, 1000).Bounds()
	if low != 0 || high != 1000 {
		t.Errorf("a rising line is drawn between %v and %v", low, high)
	}
	// Nothing at all has a top anyway, so that the line lies along the bottom
	// rather than filling the box with a shape made of rounding.
	low, high = sparkOf(0, 0, 0).Bounds()
	if low != 0 || high <= 0 {
		t.Errorf("a quiet line is drawn between %v and %v", low, high)
	}
}

// One value has no shape, and neither has a box too small to hold a line.
func TestThereIsNothingToDrawYet(t *testing.T) {
	for _, c := range []struct {
		what string
		s    Spark
		w, h float64
	}{
		{"nothing measured", sparkOf(), 120, 18},
		{"one reading", sparkOf(5), 120, 18},
		{"no room", sparkOf(1, 2, 3), 1, 18},
		{"no height", sparkOf(1, 2, 3), 120, 1},
	} {
		got := Draw(c.s, c.w, c.h)
		if len(got.Shapes) != 0 {
			t.Errorf("%s draws %d shapes", c.what, len(got.Shapes))
		}
		// And the scene still says what size it was asked for, so what holds it
		// lays out the same either way.
		if got.W != c.w || got.H != c.h {
			t.Errorf("%s is %vx%v", c.what, got.W, got.H)
		}
	}
}

// Two readings are a shape, drawn as one raster the size of the box.
func TestTwoReadingsAreAShape(t *testing.T) {
	got := Draw(sparkOf(0, 10), 120, 18)
	if len(got.Shapes) != 1 {
		t.Fatalf("it draws %d shapes", len(got.Shapes))
	}
	r, ok := got.Shapes[0].(scene.Raster)
	if !ok {
		t.Fatalf("it draws a %T", got.Shapes[0])
	}
	if r.W != 120 || r.H != 18 || r.X != 0 || r.Y != 0 {
		t.Errorf("the marks are at %v,%v and %vx%v", r.X, r.Y, r.W, r.H)
	}
	if r.Img == nil || r.Img.Bounds().Dx() != 120 {
		t.Errorf("the marks are %v", r.Img)
	}
}

// The line is where the values are: a rising series darkens the bottom left and
// the top right, and a falling one the other two corners. Read off the raster,
// because a picture nobody looked at is a picture that can be drawn upside down.
func TestTheLineGoesWhereTheValuesGo(t *testing.T) {
	const w, h = 40, 20
	// Without the fill, because the area under a line marks the same corners
	// the line does: what is being read off here is where the line goes.
	rising := Raster(Spark{Values: []float64{0, 1}, Line: ink}, w, h)
	falling := Raster(Spark{Values: []float64{1, 0}, Line: ink}, w, h)
	drawn := func(img interface{ At(x, y int) color.Color }, x, y int) bool {
		_, _, _, a := img.At(x, y).RGBA()
		return a > 0
	}
	// The left edge: nought is the bottom row for the rising line and the top
	// row for the falling one, and the picture uses the whole box.
	if !drawn(rising, 0, h-1) {
		t.Error("a rising line does not start on the bottom row")
	}
	if !drawn(falling, 0, 0) {
		t.Error("a falling line does not start on the top row")
	}
	// And the right edge is the other way about.
	if !drawn(rising, w-1, 0) {
		t.Error("a rising line does not end on the top row")
	}
	if !drawn(falling, w-1, h-1) {
		t.Error("a falling line does not end on the bottom row")
	}
}

// A fill of nothing draws the line alone, which is what a sparkline on a busy
// line of controls sometimes wants.
func TestAFillOfNothingIsALineAlone(t *testing.T) {
	const w, h = 40, 20
	bare := Spark{Values: []float64{0, 1}, Line: ink}
	filled := sparkOf(0, 1)
	count := func(s Spark) int {
		img := Raster(s, w, h)
		n := 0
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
					n++
				}
			}
		}
		return n
	}
	bareInk, filledInk := count(bare), count(filled)
	if bareInk == 0 {
		t.Fatal("a line alone draws nothing")
	}
	if filledInk <= bareInk {
		t.Errorf("a filled sparkline marks %d pixels and a bare one %d", filledInk, bareInk)
	}
}

// The widget draws what it is given, and draws something else when it is told
// to: a sparkline that kept its first shape would be a picture of a moment.
func TestTheWidgetDrawsWhatItIsGiven(t *testing.T) {
	test.NewTempApp(t)
	w := New(sparkOf(0, 1, 2))
	win := test.NewWindow(w)
	t.Cleanup(win.Close)
	win.Resize(fyne.NewSize(160, 40))

	r := test.WidgetRenderer(w)
	r.Layout(fyne.NewSize(120, 18))
	if len(r.Objects()) != 2 {
		t.Fatalf("it draws %d objects", len(r.Objects()))
	}
	// Told something else, it says something else.
	w.SetSpark(sparkOf(5, 4, 3))
	if got := w.Spark().Values; got[0] != 5 {
		t.Errorf("it holds %v", got)
	}
	// And a size too small for a shape is a background and nothing else.
	r.Layout(fyne.NewSize(0, 0))
	if len(r.Objects()) != 1 {
		t.Errorf("at no size it draws %d objects", len(r.Objects()))
	}
	if got := w.MinSize(); got.Width < 40 || got.Height < 10 {
		t.Errorf("it asks for %v", got)
	}
}
