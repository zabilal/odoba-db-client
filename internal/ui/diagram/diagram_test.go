package diagram

import (
	"bytes"
	"image/png"
	"slices"
	"strconv"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	ftheme "fyne.io/fyne/v2/theme"

	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// A small diagram: two boxes side by side with a line between them.
func two() *canvas.Graph {
	g := canvas.NewGraph([]canvas.Node{
		{ID: "a", Title: "people", Ports: []canvas.Port{{Label: "id", Key: true}, {Label: "name"}}},
		{ID: "b", Title: "orders", Ports: []canvas.Port{{Label: "id", Key: true}, {Label: "who"}}},
	}, []canvas.Edge{{From: "b", To: "a", FromPort: 1, ToPort: 0}})
	canvas.MeasureNodes(g)
	g.Nodes[0].Pos = canvas.Point{X: 0, Y: 0}
	g.Nodes[1].Pos = canvas.Point{X: 400, Y: 0}
	return g
}

func drawn(t *testing.T) *Widget {
	t.Helper()
	test.NewTempApp(t)
	w := New(two(), theme.New().PaletteFor(ftheme.VariantLight))
	w.Resize(fyne.NewSize(800, 600))
	// Rendered, so the renderer exists and has drawn once.
	test.WidgetRenderer(w)
	return w
}

// Dragging the background moves the view and nothing else.
func TestDraggingTheBackgroundPansTheView(t *testing.T) {
	w := drawn(t)
	was := w.View().Pan
	at := w.Graph().Nodes[0].Pos

	// A drag that starts where no box is.
	w.Dragged(&fyne.DragEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(700, 500)},
		Dragged:    fyne.NewDelta(20, 10)})
	w.DragEnd()

	if w.View().Pan == was {
		t.Errorf("the view did not move: %+v", w.View().Pan)
	}
	if w.Graph().Nodes[0].Pos != at {
		t.Errorf("a box moved with the view: %+v", w.Graph().Nodes[0].Pos)
	}
	if w.Graph().Nodes[0].Pinned {
		t.Error("panning pinned a box")
	}
}

// A pan that passes over a box keeps panning. What is being dragged is
// decided once, on the first step: deciding again as the pointer moves would
// have the view stop and a box leap the moment one crossed the other.
func TestAPanThatPassesOverABoxKeepsPanning(t *testing.T) {
	w := drawn(t)
	at := w.Graph().Nodes[0].Pos

	// The first step is on the background, well right of both boxes.
	w.Dragged(&fyne.DragEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(760, 500)},
		Dragged:    fyne.NewDelta(-20, -10)})
	// The next lands inside the first box.
	w.Dragged(&fyne.DragEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(20, 20)},
		Dragged:    fyne.NewDelta(-10, -5)})
	w.DragEnd()

	if w.Graph().Nodes[0].Pos != at {
		t.Errorf("a box moved during a pan: %+v", w.Graph().Nodes[0].Pos)
	}
	if w.Graph().Nodes[0].Pinned {
		t.Error("a pan over a box pinned it")
	}
}

// Dragging a box moves the box, and says so once the drag ends rather than
// at every step of it.
func TestDraggingABoxMovesIt(t *testing.T) {
	w := drawn(t)
	moved := 0
	w.OnMoved = func() { moved++ }
	was := w.Graph().Nodes[0].Pos
	pan := w.View().Pan

	// Three steps of one drag, starting inside the first box.
	for i := 0; i < 3; i++ {
		w.Dragged(&fyne.DragEvent{
			PointEvent: fyne.PointEvent{Position: fyne.NewPos(float32(20+i*10), 20)},
			Dragged:    fyne.NewDelta(10, 5)})
	}
	w.DragEnd()

	now := w.Graph().Nodes[0].Pos
	if now.X != was.X+30 || now.Y != was.Y+15 {
		t.Errorf("it moved to %+v from %+v", now, was)
	}
	if !w.Graph().Nodes[0].Pinned {
		t.Error("a box somebody moved was not pinned, so the next layout would undo it")
	}
	if w.View().Pan != pan {
		t.Errorf("the view moved with the box: %+v", w.View().Pan)
	}
	if moved != 1 {
		t.Errorf("it said the diagram changed %d times for one drag", moved)
	}
}

// A drag at a low zoom moves a box as far as the hand went, not further.
func TestABoxFollowsTheHandAtAnyZoom(t *testing.T) {
	w := drawn(t)
	w.View().Zoom = 0.5
	was := w.Graph().Nodes[0].Pos

	w.Dragged(&fyne.DragEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(10, 10)},
		Dragged:    fyne.NewDelta(10, 0)})
	w.DragEnd()

	// Ten pixels at half zoom is twenty graph units, which is ten pixels on
	// screen: the box stays under the pointer.
	if got := w.Graph().Nodes[0].Pos.X - was.X; got != 20 {
		t.Errorf("it moved %v graph units for 10 pixels at zoom 0.5", got)
	}
}

// A drag that ends without having moved anything says nothing.
func TestADragThatMovedNothingSaysNothing(t *testing.T) {
	w := drawn(t)
	moved := 0
	w.OnMoved = func() { moved++ }
	w.DragEnd()
	w.Dragged(&fyne.DragEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(700, 500)},
		Dragged:    fyne.NewDelta(5, 5)})
	w.DragEnd()
	if moved != 0 {
		t.Errorf("it said the diagram changed %d times", moved)
	}
}

// Scrolling zooms, and what is under the pointer stays under it.
func TestScrollingZoomsAboutThePointer(t *testing.T) {
	w := drawn(t)
	at := fyne.NewPos(300, 200)
	under := w.View().ToGraph(canvas.Point{X: float64(at.X), Y: float64(at.Y)})
	was := w.View().Zoom

	w.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: at},
		Scrolled: fyne.NewDelta(0, 1)})

	if w.View().Zoom <= was {
		t.Errorf("it zoomed to %v from %v", w.View().Zoom, was)
	}
	now := w.View().ToGraph(canvas.Point{X: float64(at.X), Y: float64(at.Y)})
	if diff := now.X - under.X; diff > 0.001 || diff < -0.001 {
		t.Errorf("what was under the pointer moved by %v", diff)
	}
	// And the other way.
	w.Scrolled(&fyne.ScrollEvent{PointEvent: fyne.PointEvent{Position: at},
		Scrolled: fyne.NewDelta(0, -1)})
	if diff := w.View().Zoom - was; diff > 0.001 || diff < -0.001 {
		t.Errorf("scrolling back left the zoom at %v, not %v", w.View().Zoom, was)
	}
}

// The zoom is kept between the limits the canvas sets: below one nothing is
// legible even as a shape, above the other a diagram is a wall of text.
func TestZoomingStaysWithinWhatIsWorthDrawing(t *testing.T) {
	w := drawn(t)
	for i := 0; i < 60; i++ {
		w.Zoom(false)
	}
	if got := w.View().Zoom; got < canvas.MinZoom {
		t.Errorf("it zoomed out to %v", got)
	}
	for i := 0; i < 120; i++ {
		w.Zoom(true)
	}
	if got := w.View().Zoom; got > canvas.MaxZoom {
		t.Errorf("it zoomed in to %v", got)
	}
}

// Tapping chooses a box, and tapping the background drops the choice.
func TestTappingChoosesABox(t *testing.T) {
	w := drawn(t)
	var chosen []string
	w.OnSelect = func(id string) { chosen = append(chosen, id) }

	w.Tapped(&fyne.PointEvent{Position: fyne.NewPos(20, 20)})
	if w.Selected() != "a" {
		t.Errorf("it chose %q", w.Selected())
	}
	// Tapping the same box again says nothing: it was already chosen.
	w.Tapped(&fyne.PointEvent{Position: fyne.NewPos(25, 25)})
	w.Tapped(&fyne.PointEvent{Position: fyne.NewPos(700, 500)})
	if w.Selected() != "" {
		t.Errorf("after tapping the background it holds %q", w.Selected())
	}
	if len(chosen) != 2 || chosen[0] != "a" || chosen[1] != "" {
		t.Errorf("it reported %q", chosen)
	}
}

// Fitting puts the whole diagram on screen.
func TestFittingShowsTheWholeDiagram(t *testing.T) {
	w := drawn(t)
	w.View().Zoom = 3
	w.Fit()
	visible := w.View().VisibleRect()
	whole := w.Graph().Bounds()
	if !visible.Intersects(whole) || visible.Width() < whole.Width() {
		t.Errorf("it shows %v of %v", visible, whole)
	}
}

// Only what is on screen is drawn, which is the whole of why a large schema
// pans at all.
func TestOnlyWhatIsOnScreenIsDrawn(t *testing.T) {
	w := drawn(t)
	r := test.WidgetRenderer(w).(*renderer)

	// Culling is against the screen, so the viewport has to be told how big
	// it is: a viewport that thinks the screen is another size culls the
	// wrong things, or nothing at all.
	w.Resize(fyne.NewSize(300, 200))
	if got := w.View().Screen; got.W != 300 || got.H != 200 {
		t.Fatalf("after resizing, the viewport thinks the screen is %+v", got)
	}
	w.Resize(fyne.NewSize(800, 600))
	near := len(r.Objects())
	if near < 3 {
		t.Fatalf("it drew %d objects for two boxes and a line", near)
	}
	// Pan far away: the boxes are behind, and nothing but the background is
	// left to draw.
	w.View().PanBy(-100000, 0)
	w.Refresh()
	if got := len(r.Objects()); got != 1 {
		t.Errorf("with nothing on screen it drew %d objects", got)
	}
}

// Zoomed far out, a box is a box: drawing its columns at a size nobody can
// read is pure cost.
func TestWhatIsDrawnDependsOnHowCloseItIs(t *testing.T) {
	w := drawn(t)
	r := test.WidgetRenderer(w).(*renderer)

	w.View().Zoom = 1
	w.Refresh()
	close := len(r.Objects())

	w.View().Zoom = 0.4
	w.Refresh()
	titles := len(r.Objects())

	w.View().Zoom = 0.1
	w.Refresh()
	shapes := len(r.Objects())

	if !(close > titles && titles > shapes) {
		t.Errorf("it drew %d objects close, %d at a distance and %d far off", close, titles, shapes)
	}
}

// What a node draws (FR-8.3).

// counted is how many objects of each kind a render produced, which is how a
// test says what was drawn without asserting pixels.
func counted(w *Widget) map[string]int {
	out := map[string]int{}
	for _, o := range test.WidgetRenderer(w).(*renderer).Objects() {
		switch o.(type) {
		case *fcanvas.Text:
			out["text"]++
		case *fcanvas.Line:
			out["line"]++
		case *fcanvas.Circle:
			out["circle"]++
		case *fcanvas.Rectangle:
			out["rect"]++
		}
	}
	return out
}

func texts(w *Widget) []string {
	var out []string
	for _, o := range test.WidgetRenderer(w).(*renderer).Objects() {
		if t, ok := o.(*fcanvas.Text); ok {
			out = append(out, t.Text)
		}
	}
	return out
}

// A column is drawn with its name and its type, and a key column is marked.
func TestAColumnIsDrawnWithItsTypeAndItsMark(t *testing.T) {
	w := drawn(t)
	w.Graph().Nodes[0].Ports[0].Detail = "integer"
	w.Graph().Nodes[0].Ports[1].Detail = "text"
	w.Refresh()

	said := texts(w)
	for _, want := range []string{"people", "id", "name", "integer", "text"} {
		if !slices.Contains(said, want) {
			t.Errorf("it drew %q, with no %q in it", said, want)
		}
	}
	// One mark per key column, and the first column of each box is one.
	if got := counted(w)["circle"]; got != 2 {
		t.Errorf("it drew %d key marks for two keyed columns", got)
	}

	// A type sits against the right edge of its box, which means it is
	// moved back by its own width rather than drawn from where it ends.
	n := w.Graph().Nodes[0]
	right := float32(w.View().ToScreen(n.Pos).X + n.Size.W*w.View().Zoom)
	for _, o := range test.WidgetRenderer(w).(*renderer).Objects() {
		tx, ok := o.(*fcanvas.Text)
		if !ok || tx.Text != "integer" {
			continue
		}
		if end := tx.Position().X + tx.MinSize().Width; end > right {
			t.Errorf("the type ends at %v, past the box's edge at %v", end, right)
		}
		if tx.Position().X >= right {
			t.Errorf("the type starts at %v, at or past the box's edge at %v", tx.Position().X, right)
		}
	}
}

// A key is marked rather than only emboldened: weight alone is not a
// difference somebody can see at a glance down a column of names.
func TestAKeyIsMarkedAndNotOnlyEmboldened(t *testing.T) {
	w := drawn(t)
	w.Graph().Nodes[0].Ports[0].Key = false
	w.Refresh()
	if got := counted(w)["circle"]; got != 1 {
		t.Errorf("with one keyed column it drew %d marks", got)
	}
}

// A relationship carries a mark at each end: many where the child may have
// many rows, one where it may not.
func TestARelationshipIsMarkedAtBothEnds(t *testing.T) {
	w := drawn(t)
	many := counted(w)["line"]

	w.Graph().Edges[0].Cardinality = canvas.OneToOne
	w.Refresh()
	one := counted(w)["line"]

	// A crow's foot is three lines and a bar is one, so the one-to-one
	// drawing is two lines lighter.
	if many-one != 2 {
		t.Errorf("many drew %d lines and one drew %d", many, one)
	}
}

// Too far out, a mark is a smudge, so it is not drawn.
func TestMarksAreNotDrawnWhereTheyWouldBeSmudges(t *testing.T) {
	w := drawn(t)
	w.View().Zoom = 1
	w.Refresh()
	near := counted(w)["line"]
	w.View().Zoom = 0.1
	w.Refresh()
	far := counted(w)["line"]

	// Close up: the route, a hairline under each of the two headers, and a
	// mark at each end — a crow's foot of three lines and a bar of one. Far
	// off: the route alone. So the difference is the two rules and the four
	// lines of the two marks, and anything less means a mark was drawn
	// where it is a smudge.
	if near-far < 4 {
		t.Errorf("it drew %d lines close and %d far off, a difference of %d",
			near, far, near-far)
	}
}

// A node with more columns than are worth drawing says how many are left,
// rather than growing taller than the diagram is wide.
func TestANodeWithMoreColumnsThanFit(t *testing.T) {
	g := two()
	for i := 0; i < canvas.NodeMaxPorts+5; i++ {
		g.Nodes[0].Ports = append(g.Nodes[0].Ports, canvas.Port{Label: "c" + string(rune('a'+i))})
	}
	canvas.MeasureNodes(g)
	test.NewTempApp(t)
	w := New(g, theme.New().PaletteFor(ftheme.VariantLight))
	w.Resize(fyne.NewSize(800, 600))
	test.WidgetRenderer(w)

	said := texts(w)
	want := "+" + strconv.Itoa(len(g.Nodes[0].Ports)-canvas.NodeMaxPorts) + " more"
	if !slices.Contains(said, want) {
		t.Errorf("it drew %q, with no %q in it", said, want)
	}
	// And it drew no more rows than it says it can.
	if got := len(said); got > canvas.NodeMaxPorts+8 {
		t.Errorf("it drew %d pieces of text for a node of %d columns", got, len(g.Nodes[0].Ports))
	}
}

// Taking a diagram away (FR-8.4).

// What is exported is the whole diagram, not what happens to be on screen.
func TestAnExportIsTheWholeDiagram(t *testing.T) {
	w := drawn(t)
	// Looking at a corner of it, closely.
	w.View().Zoom = 3
	w.View().Pan = canvas.Point{X: -1000, Y: -1000}

	s := w.Scene(1)
	drawn := map[string]bool{}
	for _, sh := range s.Shapes {
		if t, ok := sh.(Text); ok {
			drawn[t.S] = true
		}
	}
	for _, want := range []string{"people", "orders"} {
		if !drawn[want] {
			t.Errorf("the export left out %q; it holds %v", want, drawn)
		}
	}
	b := w.Graph().Bounds()
	if s.W < b.Width() || s.H < b.Height() {
		t.Errorf("it is %vx%v for a diagram of %vx%v", s.W, s.H, b.Width(), b.Height())
	}
	// And it is panned so that the diagram is inside the picture: the
	// leftmost box sits a margin in, not off the top-left corner.
	left, top := s.W, s.H
	for _, sh := range s.Shapes {
		if box, ok := sh.(Box); ok {
			left, top = min(left, box.X), min(top, box.Y)
		}
	}
	if left <= 0 || top <= 0 || left > 2*exportPadding || top > 2*exportPadding {
		t.Errorf("the nearest box is at %v, %v in a picture of %vx%v", left, top, s.W, s.H)
	}
}

// Nothing is chosen in an exported picture: a selection is a thing somebody
// is doing, not a thing about the schema.
func TestAnExportHasNothingSelected(t *testing.T) {
	w := drawn(t)
	w.Tapped(&fyne.PointEvent{Position: fyne.NewPos(20, 20)})
	if w.Selected() == "" {
		t.Fatal("nothing was chosen to begin with")
	}
	for _, sh := range w.Scene(1).Shapes {
		if b, ok := sh.(Box); ok && b.StrokeWidth > 1 {
			t.Errorf("a box is drawn as chosen: %+v", b)
		}
	}
}

// A schema too large to draw at its natural size is drawn smaller rather
// than written as a file nothing will open.
func TestAVeryLargeDiagramIsDrawnSmaller(t *testing.T) {
	g := two()
	g.Nodes[1].Pos = canvas.Point{X: 200000, Y: 0}
	w := drawn(t)
	*w.Graph() = *g

	s := w.Scene(1)
	if s.W > ExportLimit+1 || s.H > ExportLimit+1 {
		t.Errorf("it is %vx%v, past the limit of %v", s.W, s.H, ExportLimit)
	}
	if s.W <= 0 || s.H <= 0 {
		t.Errorf("it is %vx%v", s.W, s.H)
	}
}

// An SVG is the same scene written as text, so what is in the picture is in
// the file — searchable and copyable, rather than outlines.
func TestAnSVGHoldsTheNamesAsText(t *testing.T) {
	w := drawn(t)
	var b strings.Builder
	if err := w.SVG(&b, 1); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		`<?xml version="1.0"`, "<svg ", "</svg>", ">people<", ">orders<", "<rect ", "<line ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the file has no %q in it", want)
		}
	}
	// A column's type sits against the right edge by being told to end
	// there, which needs no font metrics.
	if !strings.Contains(out, `text-anchor="end"`) {
		w.Graph().Nodes[0].Ports[0].Detail = "integer"
		b.Reset()
		if err := w.SVG(&b, 1); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), `text-anchor="end"`) {
			t.Error("a column's type is not aligned to the right edge")
		}
	}
}

// A table called <b> is a table called <b>, not a file that will not parse.
func TestAnSVGSaysWhatANameIs(t *testing.T) {
	w := drawn(t)
	w.Graph().Nodes[0].Title = `a & b <c> "d"`
	var b strings.Builder
	if err := w.SVG(&b, 1); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "a &amp; b &lt;c&gt; &quot;d&quot;") {
		t.Errorf("it wrote %q", out)
	}
	if strings.Contains(out, "<c>") {
		t.Error("a name was written as markup")
	}
}

// A PNG is this widget rendered, so the file is what the window shows.
func TestAPNGIsWritten(t *testing.T) {
	w := drawn(t)
	var b bytes.Buffer
	if err := w.PNG(&b, 0.5, theme.New()); err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(&b)
	if err != nil {
		t.Fatalf("it wrote something that is not a PNG: %v", err)
	}
	// The picture is the size of the diagram, not of the widget's minimum.
	want := w.Scene(0.5)
	if got := img.Bounds(); float64(got.Dx()) < want.W-2 || float64(got.Dx()) > want.W+2 ||
		float64(got.Dy()) < want.H-2 || float64(got.Dy()) > want.H+2 {
		t.Errorf("it is %v for a diagram of %vx%v", got, want.W, want.H)
	}
	// And it is not blank: a diagram drawn on a background has more than one
	// colour in it.
	seen := map[uint32]bool{}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y += 3 {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x += 3 {
			r, g, bl, _ := img.At(x, y).RGBA()
			seen[r<<20|g<<10|bl] = true
		}
	}
	if len(seen) < 2 {
		t.Errorf("the picture is one colour")
	}
}
