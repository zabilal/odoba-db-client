package diagram

import (
	"testing"

	"fyne.io/fyne/v2"
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
