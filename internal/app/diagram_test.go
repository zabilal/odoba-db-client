package app

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
)

// Keeping a diagram as somebody arranged it (FR-8.2).

func laidOut() *canvas.Graph {
	g := canvas.NewGraph([]canvas.Node{
		{ID: "a", Title: "people", Pos: canvas.Point{X: 10, Y: 10}},
		{ID: "b", Title: "orders", Pos: canvas.Point{X: 200, Y: 10}},
	}, nil)
	canvas.MeasureNodes(g)
	return g
}

// Only what somebody moved is kept. Keeping every position would mean a
// diagram never laid itself out again.
func TestOnlyWhatWasMovedIsKept(t *testing.T) {
	g := laidOut()
	g.Nodes[1].Pos = canvas.Point{X: 500, Y: 300}
	g.Nodes[1].Pinned = true

	l := LayoutOf(g, nil)
	if len(l.Moved) != 1 {
		t.Fatalf("it kept %d positions", len(l.Moved))
	}
	if got := l.Moved["b"]; got.X != 500 || got.Y != 300 {
		t.Errorf("it kept %+v", got)
	}
	// Nothing moved, nothing kept.
	if got := LayoutOf(laidOut(), nil); len(got.Moved) != 0 {
		t.Errorf("an untouched diagram kept %+v", got.Moved)
	}
}

// A kept arrangement is put back on a freshly laid out graph, and what was
// put back is pinned so the next layout does not undo it.
func TestAKeptArrangementIsPutBack(t *testing.T) {
	g := laidOut()
	ApplyLayout(g, localdb.DiagramLayout{Moved: map[string]localdb.NodePlace{
		"b": {X: 500, Y: 300},
	}})
	if got := g.NodeByID("b"); got.Pos.X != 500 || got.Pos.Y != 300 || !got.Pinned {
		t.Errorf("it came back as %+v", got)
	}
	// And a node nobody moved is left exactly as the layout put it.
	if got := g.NodeByID("a"); got.Pos.X != 10 || got.Pinned {
		t.Errorf("an untouched node is %+v", got)
	}
}

// A kept position for a table the schema no longer has is ignored: a table
// dropped is a position nobody needs.
func TestAPositionForSomethingNoLongerThere(t *testing.T) {
	g := laidOut()
	ApplyLayout(g, localdb.DiagramLayout{Moved: map[string]localdb.NodePlace{
		"gone": {X: 500, Y: 300},
	}})
	for _, n := range g.Nodes {
		if n.Pinned {
			t.Errorf("%s was pinned by a position for something else", n.ID)
		}
	}
}

// Where somebody was looking is kept too, so reopening a diagram shows the
// part of it they were reading.
func TestWhereTheViewWasLeft(t *testing.T) {
	view := canvas.NewViewport(canvas.Size{W: 800, H: 600})
	view.Pan = canvas.Point{X: -120, Y: 40}
	view.Zoom = 1.5

	l := LayoutOf(laidOut(), view)
	back := canvas.NewViewport(canvas.Size{W: 800, H: 600})
	if !RestoreView(back, l) {
		t.Fatal("it did not put the view back")
	}
	if back.Pan != view.Pan || back.Zoom != view.Zoom {
		t.Errorf("it came back at %+v zoom %v", back.Pan, back.Zoom)
	}
}

// A zoom outside what the canvas allows means the file was written by
// something else, and guessing at what it meant is worse than showing the
// diagram whole.
func TestAViewThatCannotBeRestored(t *testing.T) {
	for _, zoom := range []float64{0, -1, canvas.MinZoom / 2, canvas.MaxZoom * 2} {
		view := canvas.NewViewport(canvas.Size{W: 800, H: 600})
		if RestoreView(view, localdb.DiagramLayout{Zoom: zoom}) {
			t.Errorf("a zoom of %v was put back", zoom)
		}
		if view.Zoom != 1 {
			t.Errorf("a zoom of %v left the view at %v", zoom, view.Zoom)
		}
	}
	if RestoreView(nil, localdb.DiagramLayout{Zoom: 1}) {
		t.Error("it put a view back into nothing")
	}
}
