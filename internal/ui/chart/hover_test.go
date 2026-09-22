package chart

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
)

// Pointing at a chart on a screen.

// A chart is hovered and tapped, which is what the window's driver looks for.
var (
	_ desktop.Hoverable  = (*Widget)(nil)
	_ fyne.Tappable      = (*Widget)(nil)
	_ desktop.Cursorable = (*Widget)(nil)
)

// inWindow shows a chart in a window, so that a redraw reaches the renderer
// the way it does in the application: a widget with no canvas under it is
// refreshed by nobody.
func inWindow(t *testing.T, c Chart, w, h float32) (*Widget, *chartRenderer) {
	t.Helper()
	test.NewTempApp(t)
	cw := New(c)
	win := test.NewWindow(cw)
	t.Cleanup(win.Close)
	win.Resize(fyne.NewSize(w, h))
	r := test.WidgetRenderer(cw).(*chartRenderer)
	r.Layout(fyne.NewSize(w, h))
	return cw, r
}

func moveTo(w *Widget, x, y float64) {
	w.MouseMoved(&desktop.MouseEvent{PointEvent: fyne.PointEvent{
		Position: fyne.NewPos(float32(x), float32(y))}})
}

// rings are the marks drawn on what is being pointed at.
func rings(objs []fyne.CanvasObject) []*fcanvas.Circle {
	var out []*fcanvas.Circle
	for _, o := range objs {
		if c, ok := o.(*fcanvas.Circle); ok {
			out = append(out, c)
		}
	}
	return out
}

// Pointing at a chart says what is there, and marks it.
func TestPointingAtAChartSaysWhatIsThere(t *testing.T) {
	c := plain(Line, oneSeries("orders", 10, 50, 20))
	w, r := shown(t, c, 600, 400)
	f := w.Frame()

	if _, ok := w.Reading(); ok {
		t.Error("something was read before the pointer arrived")
	}
	x, y := at(f, 1, 50)
	moveTo(w, x, y)
	got, ok := w.Reading()
	if !ok {
		t.Fatal("nothing was read under the pointer")
	}
	if got.Y != 50 {
		t.Errorf("it read %v, want 50", got.Y)
	}
	said := strings.Join(texts(r.Objects()), " | ")
	if !strings.Contains(said, "50") {
		t.Errorf("the chart says %q, want the value written by the pointer", said)
	}
	ms := rings(r.Objects())
	if len(ms) != 1 {
		t.Fatalf("%d marks, want the point pointed at marked", len(ms))
	}
	// On the point, not under the hand: that is what says which of two
	// close points was read.
	want := fyne.NewPos(float32(x-markRadius), float32(y-markRadius))
	if got := ms[0].Position(); absDiff(got.X, want.X) > 1 || absDiff(got.Y, want.Y) > 1 {
		t.Errorf("the mark is at %v, the point is at %v", got, want)
	}
}

func absDiff(a, b float32) float32 {
	if a > b {
		return a - b
	}
	return b - a
}

// Taking the pointer away takes the tooltip with it.
func TestTakingThePointerAwayTakesTheTooltip(t *testing.T) {
	c := plain(Line, oneSeries("orders", 10, 50, 20))
	w, r := shown(t, c, 600, 400)
	x, y := at(w.Frame(), 1, 50)
	moveTo(w, x, y)
	before := len(r.Objects())
	w.MouseOut()
	if _, ok := w.Reading(); ok {
		t.Error("something is still read with the pointer gone")
	}
	if len(r.Objects()) >= before {
		t.Errorf("%d objects after the pointer left, %d while it was there", len(r.Objects()), before)
	}
	if len(rings(r.Objects())) != 0 {
		t.Error("the mark stayed behind")
	}
}

// Moving the pointer must not draw the chart again: a hundred thousand marks
// are rasterised once, and what changes is a few words and a ring.
func TestMovingThePointerDoesNotDrawTheChartAgain(t *testing.T) {
	c := plain(Line, oneSeries("orders", 10, 50, 20))
	w, r := inWindow(t, c, 600, 400)
	f := w.Frame()
	marks := images(r.Objects())
	if marks != 1 {
		t.Fatalf("%d images before pointing", marks)
	}
	was := r.Objects()[1]
	read := 0
	for i, v := range []float64{10, 50, 20} {
		x, y := at(f, float64(i), v)
		moveTo(w, x, y)
		if _, ok := w.Reading(); ok {
			read++
		}
	}
	if read != 3 {
		t.Fatalf("the pointer read %d of 3 points; this test needs it over the data", read)
	}
	if images(r.Objects()) != 1 {
		t.Errorf("%d images after pointing, want the one layer of marks", images(r.Objects()))
	}
	if r.Objects()[1] != was {
		t.Error("the drawing was rebuilt when only the pointer moved")
	}
}

// The tooltip stays inside the chart rather than hanging off its edge, where
// it would be unreadable.
func TestTheTooltipStaysInsideTheChart(t *testing.T) {
	// A long name and a narrow chart, so that a tooltip left where the
	// pointer put it would hang off an edge.
	c := plain(Line, oneSeries("orders placed and not yet despatched", 10, 50, 20),
		oneSeries("refunds", 1, 2, 3))
	const w, h = 320, 220
	cw, r := shown(t, c, w, h)
	f := cw.Frame()

	// The far corner, where both flips have to fire; the near corner, where
	// neither does; and the middle with the long name under the pointer,
	// where flipping would push the tooltip off the other edge instead.
	midX, midY := at(f, 1, 50)
	for _, where := range [][2]float64{
		{float64(f.Plot.Max.X) - 2, float64(f.Plot.Max.Y) - 2},
		{float64(f.Plot.Min.X) + 2, float64(f.Plot.Min.Y) + 2},
		{midX, midY},
	} {
		moveTo(cw, where[0], where[1])
		if _, ok := cw.Reading(); !ok {
			t.Fatalf("nothing was read at (%v,%v)", where[0], where[1])
		}
		box := r.tip.box
		if box == nil {
			t.Fatal("no tooltip")
		}
		if box.Position().X+box.Size().Width > w || box.Position().Y+box.Size().Height > h {
			t.Errorf("at (%v,%v) the tooltip reaches (%v,%v) of a %dx%d chart", where[0], where[1],
				box.Position().X+box.Size().Width, box.Position().Y+box.Size().Height, w, h)
		}
		if box.Position().X < 0 || box.Position().Y < 0 {
			t.Errorf("at (%v,%v) the tooltip starts at %v", where[0], where[1], box.Position())
		}
		// Wide enough for the longest line in it, with its padding.
		widest := float32(0)
		for _, o := range r.tip.texts {
			widest = max(widest, o.MinSize().Width)
			if o.Position().X < box.Position().X || o.Position().Y < box.Position().Y {
				t.Errorf("a line at %v is outside the tooltip at %v", o.Position(), box.Position())
			}
			if o.Position().X+o.MinSize().Width > box.Position().X+box.Size().Width {
				t.Errorf("a line reaching %v runs out of the tooltip ending at %v",
					o.Position().X+o.MinSize().Width, box.Position().X+box.Size().Width)
			}
		}
		if box.Size().Width < widest {
			t.Errorf("the tooltip is %v wide and its longest line is %v", box.Size().Width, widest)
		}
	}
}

// Tapping hands over what was tapped, which is what a click filters on; and
// tapping nothing hands over nothing.
func TestTappingHandsOverWhatWasTapped(t *testing.T) {
	c := plain(Line, oneSeries("orders", 10, 50, 20))
	w, _ := shown(t, c, 600, 400)
	f := w.Frame()
	var got []Reading
	w.OnPick = func(r Reading) { got = append(got, r) }

	x, y := at(f, 1, 50)
	w.Tapped(&fyne.PointEvent{Position: fyne.NewPos(float32(x), float32(y))})
	if len(got) != 1 || got[0].Row != 1 {
		t.Fatalf("it handed over %+v", got)
	}
	w.Tapped(&fyne.PointEvent{Position: fyne.NewPos(2, 2)})
	if len(got) != 1 {
		t.Errorf("tapping outside the plot handed over %+v", got[1:])
	}
}

// Drawing something else forgets what was being pointed at: a tooltip about
// a series that is no longer drawn is a tooltip about nothing.
func TestDrawingSomethingElseForgetsTheReading(t *testing.T) {
	c := plain(Line, oneSeries("orders", 10, 50, 20))
	w, r := shown(t, c, 600, 400)
	x, y := at(w.Frame(), 1, 50)
	moveTo(w, x, y)
	if _, ok := w.Reading(); !ok {
		t.Fatal("nothing was read")
	}
	next := w.Chart()
	next.Series = []Series{oneSeries("refunds", 1, 2, 3)}
	w.SetChart(next)
	r.Layout(fyne.NewSize(600, 400))
	if _, ok := w.Reading(); ok {
		t.Error("the old reading survived a new chart")
	}
	if len(rings(r.Objects())) != 0 {
		t.Error("the old mark survived a new chart")
	}
}

// A chart that cannot be drawn cannot be pointed at either.
func TestAChartThatCannotBeDrawnReadsNothing(t *testing.T) {
	w, _ := shown(t, plain(Pie, oneSeries("v", 1, -2)), 400, 300)
	moveTo(w, 200, 150)
	if _, ok := w.Reading(); ok {
		t.Error("something was read in a chart that was not drawn")
	}
	w.Tapped(&fyne.PointEvent{Position: fyne.NewPos(200, 150)})
}

// A chart that was drawn and then cannot be leaves nothing behind to point
// at: what is under the cursor is what is on the screen.
func TestAChartThatStopsBeingDrawableReadsNothing(t *testing.T) {
	c := plain(Line, oneSeries("orders", 10, 50, 20))
	w, r := shown(t, c, 600, 400)
	x, y := at(w.Frame(), 1, 50)
	moveTo(w, x, y)
	if _, ok := w.Reading(); !ok {
		t.Fatal("nothing was read while it could be drawn")
	}
	bad := w.Chart()
	bad.Kind, bad.Series = Pie, []Series{oneSeries("v", 1, -2)}
	w.SetChart(bad)
	r.Layout(fyne.NewSize(600, 400))
	moveTo(w, x, y)
	if _, ok := w.Reading(); ok {
		t.Error("the old drawing was still pointed at")
	}
}

// The pointer arriving is the same as the pointer moving: a chart entered
// with a jump of the mouse still says what is under it.
func TestThePointerArrivingReadsWhatIsUnderIt(t *testing.T) {
	c := plain(Line, oneSeries("orders", 10, 50, 20))
	w, _ := shown(t, c, 600, 400)
	x, y := at(w.Frame(), 1, 50)
	w.MouseIn(&desktop.MouseEvent{PointEvent: fyne.PointEvent{
		Position: fyne.NewPos(float32(x), float32(y))}})
	if _, ok := w.Reading(); !ok {
		t.Error("nothing was read where the pointer arrived")
	}
}

// The tooltip follows the pointer within one reading, so it never sits under
// the hand.
func TestTheTooltipFollowsThePointer(t *testing.T) {
	c := plain(Line, oneSeries("orders", 10, 50, 20))
	w, r := shown(t, c, 600, 400)
	f := w.Frame()
	x, y := at(f, 1, 50)
	moveTo(w, x, y+40)
	first := r.tip.box.Position()
	was, _ := w.Reading()

	moveTo(w, x+4, y+80)
	now, _ := w.Reading()
	if !sameReading(was, now) {
		t.Fatalf("the reading changed from %+v to %+v; this test needs it not to", was, now)
	}
	if r.tip.box.Position() == first {
		t.Errorf("the tooltip stayed at %v as the pointer moved", first)
	}
}

func TestTestDriverTapsAChart(t *testing.T) {
	c := plain(Line, oneSeries("orders", 10, 50, 20))
	test.NewTempApp(t)
	w := New(c)
	var got int
	w.OnPick = func(Reading) { got++ }
	win := test.NewWindow(w)
	t.Cleanup(win.Close)
	win.Resize(fyne.NewSize(600, 400))

	f := w.Frame()
	x, y := at(f, 1, 50)
	test.TapAt(w, fyne.NewPos(float32(x), float32(y)))
	if got != 1 {
		t.Errorf("a tap through the driver reached the chart %d times", got)
	}
}
