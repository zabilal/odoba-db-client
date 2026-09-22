package shell

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
	"github.com/ikigai-db/ikigai-db/internal/ui/erd"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// The way into a diagram (FR-8.1, FR-8.2, FR-8.3).

// diagrammed opens a diagram of the fake's one schema and waits for it.
func diagrammed(t *testing.T) (*fixture, *tab, *diagramPanel) {
	t.Helper()
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	tb := fx.s.OpenDiagram(c.ID, model.NewRef(model.KindDatabase, "main"))
	pump(t, fx.q, func() bool { return tb.diagram != nil })
	return fx, tb, tb.diagram
}

// A schema opens as a diagram of its tables.
func TestASchemaOpensAsADiagram(t *testing.T) {
	_, tb, p := diagrammed(t)
	if tb.item.Text != "Diagram: main" {
		t.Errorf("the tab is called %q", tb.item.Text)
	}
	if got := len(p.w.Graph().Nodes); got != 1 {
		t.Fatalf("it drew %d tables", got)
	}
	if got := p.w.Graph().Nodes[0].ID; got != "main.items" {
		t.Errorf("it drew %q", got)
	}
	// Every box has a size and a place, or nothing could be drawn.
	n := p.w.Graph().Nodes[0]
	if n.Size.W <= 0 || n.Size.H <= 0 {
		t.Errorf("it is %v across", n.Size)
	}
	if !strings.Contains(tb.footer.Text, "1 table") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// Moving a box keeps it there, and it is still there the next time the
// diagram is opened.
func TestABoxStaysWhereItWasPut(t *testing.T) {
	fx, tb, p := diagrammed(t)
	p.w.Graph().Nodes[0].Pos = canvas.Point{X: 700, Y: 400}
	p.w.Graph().Nodes[0].Pinned = true
	p.keep()

	// The write happens off the UI goroutine, so wait for it to land.
	pump(t, fx.q, func() bool {
		l, ok := p.arrangement()
		return ok && len(l.Moved) == 1
	})

	connID := tb.connID
	fx.s.closeTab(tb.item)
	again := fx.s.OpenDiagram(connID, model.NewRef(model.KindDatabase, "main"))
	pump(t, fx.q, func() bool { return again.diagram != nil })

	got := again.diagram.w.Graph().NodeByID("main.items")
	if got == nil || got.Pos.X != 700 || got.Pos.Y != 400 {
		t.Errorf("it came back at %+v", got)
	}
	if !got.Pinned {
		t.Error("it came back unpinned, so the next layout would move it")
	}
}

// Laying out again is the way back from having moved things into a mess: it
// unpins everything and forgets the arrangement.
func TestLayingOutAgainForgetsWhatWasMoved(t *testing.T) {
	fx, _, p := diagrammed(t)
	p.w.Graph().Nodes[0].Pos = canvas.Point{X: 700, Y: 400}
	p.w.Graph().Nodes[0].Pinned = true
	p.keep()
	pump(t, fx.q, func() bool {
		_, ok := p.arrangement()
		return ok
	})

	p.layOutAgain()
	if p.w.Graph().Nodes[0].Pinned {
		t.Error("a box is still pinned after laying out again")
	}
	pump(t, fx.q, func() bool {
		_, ok := p.arrangement()
		return !ok
	})
}

// Opening the same diagram twice brings the first tab forward: two diagrams
// of one schema could be arranged differently and only one is kept.
func TestOpeningTheSameDiagramTwiceOpensOneTab(t *testing.T) {
	fx, tb, _ := diagrammed(t)
	if again := fx.s.OpenDiagram(tb.connID, tb.ref); again != tb {
		t.Error("it opened a second diagram of the same schema")
	}
	if got := len(fx.s.open); got != 1 {
		t.Errorf("%d tabs are open", got)
	}
}

// A diagram is offered on what holds the tables, and not on one of them.
func TestWhichObjectsAreOfferedADiagram(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	if fx.s.canDiagram() {
		t.Error("a table is offered a diagram of itself")
	}
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	if !fx.s.canDiagram() {
		t.Error("a database is not offered a diagram")
	}
	if fx.s.menuItems[cmdDiagram].Disabled {
		t.Error("the menu item is disabled for a database")
	}
	fx.s.release(c.ID, false)
	if fx.s.canDiagram() {
		t.Error("a closed connection is offered a diagram")
	}
}

// A schema node draws that schema; a database draws everything in it.
func TestWhatANodeAsksToBeDrawn(t *testing.T) {
	got := opts(model.NewRef(model.KindSchema, "main", "public"))
	if len(got.Schemas) != 1 || got.Schemas[0] != "public" {
		t.Errorf("a schema asked for %+v", got)
	}
	if got := opts(model.NewRef(model.KindDatabase, "main")); len(got.Schemas) != 0 {
		t.Errorf("a database asked for %+v", got)
	}
}

// A schema that cannot be read fails the tab rather than drawing an empty
// diagram, which would read as a database with no tables in it.
func TestADiagramOfASchemaThatWillNotRead(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "boom", nil)
	tb := fx.s.OpenDiagram(c.ID, model.NewRef(model.KindDatabase, "boom"))
	pump(t, fx.q, func() bool { return strings.Contains(labelText(tb.body), "could not read the schema") })
	if tb.diagram != nil {
		t.Error("it drew a diagram it could not read")
	}
}

// A diagram of part of a schema says what leads out of it, or the tables it
// draws look unrelated to everything it does not.
//
// Nothing in the window asks for part of a schema yet — that is the subset
// T3.20's neighbourhood filter will ask for — so this is put to the line
// that will say it.
func TestADiagramSaysWhatLeadsOutOfIt(t *testing.T) {
	_, tb, p := diagrammed(t)

	p.say(erd.Diagram{Graph: canvas.NewGraph(
		[]canvas.Node{{ID: "a"}, {ID: "b"}}, []canvas.Edge{{From: "a", To: "b"}}), Outside: 3}, "")
	said := tb.footer.Text
	for _, want := range []string{"2 tables", "1 relationship", "3 relationships lead outside"} {
		if !strings.Contains(said, want) {
			t.Errorf("it says %q, with no %q in it", said, want)
		}
	}

	// A diagram of everything leads nowhere else, and says nothing about it.
	p.say(erd.Diagram{Graph: canvas.NewGraph([]canvas.Node{{ID: "a"}}, nil)}, "")
	if strings.Contains(tb.footer.Text, "outside") {
		t.Errorf("a whole schema says %q", tb.footer.Text)
	}
}

// Choosing a box names it, so the footer says what is selected as well as
// what is there.
func TestChoosingABoxNamesIt(t *testing.T) {
	_, tb, p := diagrammed(t)
	p.w.OnSelect("main.items")
	if !strings.Contains(tb.footer.Text, "main.items") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}
