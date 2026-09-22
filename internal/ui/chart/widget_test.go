package chart

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
)

// A chart on the screen.

func shown(t *testing.T, c Chart, w, h float32) (*Widget, *chartRenderer) {
	t.Helper()
	test.NewTempApp(t)
	cw := New(c)
	r := test.WidgetRenderer(cw).(*chartRenderer)
	r.Layout(fyne.NewSize(w, h))
	return cw, r
}

func texts(objs []fyne.CanvasObject) []string {
	var out []string
	for _, o := range objs {
		if t, ok := o.(*fcanvas.Text); ok {
			out = append(out, t.Text)
		}
	}
	return out
}

func images(objs []fyne.CanvasObject) int {
	n := 0
	for _, o := range objs {
		if _, ok := o.(*fcanvas.Image); ok {
			n++
		}
	}
	return n
}

// The widget draws what the scene says and nothing of its own.
func TestTheWidgetDrawsTheScene(t *testing.T) {
	c := plain(Line, oneSeries("orders", 1, 5, 3))
	c.XTitle = "day"
	w, r := shown(t, c, 600, 400)
	objs := r.Objects()
	if images(objs) != 1 {
		t.Errorf("%d images, want one layer of marks", images(objs))
	}
	said := texts(objs)
	if !has(said, "day") {
		t.Errorf("labels %v, want the axis named", said)
	}
	f := w.Frame()
	if f.Plot.Dx() <= 0 || f.W != 600 || f.H != 400 {
		t.Errorf("the frame is %+v, want one laid out at 600x400", f)
	}
	if w.Err() != nil {
		t.Errorf("err %v", w.Err())
	}
}

// The frame is kept, because a hover turns a cursor position into a value
// through it.
func TestTheFrameIsKeptAtTheSizeItWasDrawnAt(t *testing.T) {
	w, r := shown(t, plain(Line, oneSeries("v", 1, 5, 3)), 600, 400)
	first := w.Frame()
	r.Layout(fyne.NewSize(300, 200))
	second := w.Frame()
	if second.Plot.Dx() >= first.Plot.Dx() {
		t.Errorf("plot %d wide at 300px and %d at 600px", second.Plot.Dx(), first.Plot.Dx())
	}
	if second.W != 300 || second.H != 200 {
		t.Errorf("the frame is %vx%v, want the size it was last drawn at", second.W, second.H)
	}
}

// Drawing something else redraws, rather than leaving the old picture up.
func TestSetChartRedraws(t *testing.T) {
	w, r := shown(t, plain(Line, oneSeries("orders", 1, 5, 3), oneSeries("refunds", 1, 2, 3)), 600, 400)
	if !has(texts(r.Objects()), "orders") {
		t.Fatalf("labels %v, want the key to name the series", texts(r.Objects()))
	}
	c := w.Chart()
	c.Series = []Series{oneSeries("returns", 2, 4, 6), oneSeries("credits", 1, 1, 1)}
	w.SetChart(c)
	r.Layout(fyne.NewSize(600, 400))
	said := texts(r.Objects())
	if has(said, "orders") {
		t.Errorf("labels %v, want the old key gone", said)
	}
	if !has(said, "returns") || !has(said, "credits") {
		t.Errorf("labels %v, want the new series named", said)
	}
}

// A chart that cannot be drawn says why, and what to do instead: a refusal
// without a way forward leaves somebody guessing at seven kinds.
func TestAChartThatCannotBeDrawnSaysWhyAndWhatToDo(t *testing.T) {
	bad := plain(Pie, oneSeries("v", 1, -2))
	w, r := shown(t, bad, 400, 300)
	if w.Err() == nil {
		t.Fatal("a pie of a negative value was drawn")
	}
	if images(r.Objects()) != 0 {
		t.Error("marks were drawn for a chart that cannot be drawn")
	}
	said := strings.Join(texts(r.Objects()), " | ")
	if !strings.Contains(said, "negative") {
		t.Errorf("it said %q, want the reason", said)
	}
	if !strings.Contains(said, "bar chart") {
		t.Errorf("it said %q, want what to do instead", said)
	}
}

// A chart of no rows is not a failure, and says so plainly.
func TestAChartOfNothingSaysSo(t *testing.T) {
	_, r := shown(t, plain(Line), 400, 300)
	said := strings.Join(texts(r.Objects()), " | ")
	if !strings.Contains(said, "nothing to draw") {
		t.Errorf("it said %q", said)
	}
}

// Smaller than its minimum and the gutters would be most of the picture.
func TestAChartHasARoomItNeeds(t *testing.T) {
	_, r := shown(t, plain(Line, oneSeries("v", 1, 2)), 600, 400)
	if m := r.MinSize(); m.Width < 100 || m.Height < 80 {
		t.Errorf("minimum %v is too small to hold a frame", m)
	}
}

// An export asks for a picture of a size by saying the widget is that big,
// which is how the software renderer is told what to capture.
func TestAnExportAsksForItsOwnSize(t *testing.T) {
	test.NewTempApp(t)
	w := New(plain(Line, oneSeries("v", 1, 2)))
	w.minSize = fyne.NewSize(800, 600)
	if got := test.WidgetRenderer(w).MinSize(); got != fyne.NewSize(800, 600) {
		t.Errorf("minimum %v, want the size asked for", got)
	}
}
