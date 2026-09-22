package shell

import (
	"os"
	"path/filepath"
	"slices"
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
	var names []string
	for _, n := range p.w.Graph().Nodes {
		names = append(names, n.ID)
	}
	slices.Sort(names)
	want := []string{"audit.log", "main.alone", "main.items", "main.lines", "main.orders"}
	if !slices.Equal(names, want) {
		t.Fatalf("it drew %v, want %v", names, want)
	}
	// Every box has a size and a place, or nothing could be drawn.
	for _, n := range p.w.Graph().Nodes {
		if n.Size.W <= 0 || n.Size.H <= 0 {
			t.Errorf("%s is %v across", n.ID, n.Size)
		}
	}
	if !strings.Contains(tb.footer.Text, "5 tables") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	if !strings.Contains(tb.footer.Text, "2 relationships") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// Moving a box keeps it there, and it is still there the next time the
// diagram is opened.
func TestABoxStaysWhereItWasPut(t *testing.T) {
	fx, tb, p := diagrammed(t)
	p.w.Graph().NodeByID("main.items").Pos = canvas.Point{X: 700, Y: 400}
	p.w.Graph().NodeByID("main.items").Pinned = true
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
	p.w.Graph().NodeByID("main.items").Pos = canvas.Point{X: 700, Y: 400}
	p.w.Graph().NodeByID("main.items").Pinned = true
	p.keep()
	pump(t, fx.q, func() bool {
		_, ok := p.arrangement()
		return ok
	})

	p.layOutAgain()
	for _, n := range p.w.Graph().Nodes {
		if n.Pinned {
			t.Errorf("%s is still pinned after laying out again", n.ID)
		}
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

// Exporting a diagram (FR-8.4).

// The format follows the name: somebody who types .svg means SVG.
func TestExportingADiagramFollowsTheNameGiven(t *testing.T) {
	for _, c := range []struct {
		name  string
		holds string
	}{
		{"schema.svg", "<svg "},
		{"schema.SVG", "<svg "},
		{"schema.png", "\x89PNG"},
		{"schema", "\x89PNG"}, // no extension: a picture, which means PNG
	} {
		fx, tb, p := diagrammed(t)
		p.export()
		if len(fx.files.saves) != 1 {
			t.Fatalf("%s: it asked to save %d times", c.name, len(fx.files.saves))
		}
		if got := fx.files.saves[0].Name; got != "main.png" {
			t.Errorf("%s: it suggests %q", c.name, got)
		}
		if got := fx.files.saves[0].Extensions; !slices.Contains(got, "png") || !slices.Contains(got, "svg") {
			t.Errorf("%s: it offers %q", c.name, got)
		}

		path := filepath.Join(t.TempDir(), c.name)
		fx.files.answer(path, nil)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !strings.Contains(string(data), c.holds) {
			t.Errorf("%s: it wrote %d bytes beginning %q", c.name, len(data), first(data, 8))
		}
		if !strings.Contains(tb.footer.Text, "Exported to "+c.name) {
			t.Errorf("%s: the footer says %q", c.name, tb.footer.Text)
		}
	}
}

func first(b []byte, n int) string {
	if len(b) < n {
		n = len(b)
	}
	return string(b[:n])
}

// Cancelling writes nothing and reports nothing.
func TestCancellingAnExport(t *testing.T) {
	fx, tb, p := diagrammed(t)
	tb.footer.SetText("nothing said yet")
	p.export()
	fx.files.answer("", nil)
	if tb.footer.Text != "nothing said yet" {
		t.Errorf("cancelling said %q", tb.footer.Text)
	}
	if fx.s.errors.text != "" {
		t.Errorf("cancelling reported a failure: %q", fx.s.errors.text)
	}
}

// A file that cannot be written is reported, not swallowed.
func TestAnExportThatCannotBeWritten(t *testing.T) {
	fx, _, p := diagrammed(t)
	p.export()
	fx.files.answer(filepath.Join(t.TempDir(), "no-such-directory", "d.png"), nil)
	if !strings.Contains(fx.s.errors.text, "could not export") {
		t.Errorf("it said %q", fx.s.errors.text)
	}
}

// A name a filesystem would read as a path does not become one.
func TestWhatAnExportedDiagramIsCalled(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"main", "main.png"},
		{"a/b", "a-b.png"},
		{`C:\thing`, "C--thing.png"},
	} {
		if got := diagramFileName(c.in); got != c.want {
			t.Errorf("%q became %q, want %q", c.in, got, c.want)
		}
	}
}

// Narrowing a diagram to what is near one table (FR-8.5).

func namesIn(p *diagramPanel) []string {
	var out []string
	for _, n := range p.w.Graph().Nodes {
		out = append(out, n.ID)
	}
	slices.Sort(out)
	return out
}

// A diagram of two hundred tables is a ball of string. One of the tables
// within a relationship or two of the one somebody is looking at is a
// diagram.
func TestFocusingOnATableNarrowsTheDiagram(t *testing.T) {
	_, tb, p := diagrammed(t)

	p.focusOn("main.items")
	if got, want := namesIn(p), []string{"main.items", "main.orders"}; !slices.Equal(got, want) {
		t.Errorf("one relationship away it drew %v, want %v", got, want)
	}
	if !strings.Contains(tb.footer.Text, "Around main.items, 1 relationship away") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}

	// Further out reaches the table beyond.
	p.degree = 2
	p.draw()
	p = tb.diagram
	if got := namesIn(p); !slices.Contains(got, "main.lines") {
		t.Errorf("two relationships away it drew %v", got)
	}
	// And never the one nothing points at.
	if slices.Contains(namesIn(p), "main.alone") {
		t.Errorf("it drew a table nothing connects to: %v", namesIn(p))
	}
}

// Just that table is a diagram of one box, which is how somebody sees what a
// table is without anything else in the way.
func TestFocusingOnJustOneTable(t *testing.T) {
	_, _, p := diagrammed(t)
	p.degree = 0
	p.focusOn("main.items")
	if got, want := namesIn(p), []string{"main.items"}; !slices.Equal(got, want) {
		t.Errorf("it drew %v, want %v", got, want)
	}
}

// A narrowed diagram says what leads out of it, or the tables it draws look
// unrelated to everything it does not.
func TestANarrowedDiagramSaysWhatLeadsOut(t *testing.T) {
	_, tb, p := diagrammed(t)
	p.degree = 0
	p.focusOn("main.orders")
	if !strings.Contains(tb.footer.Text, "lead outside") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// Showing everything widens it back.
func TestShowingEverythingAgain(t *testing.T) {
	_, tb, p := diagrammed(t)
	p.focusOn("main.items")
	p.showEverything()
	p = tb.diagram
	if got := len(namesIn(p)); got != 5 {
		t.Errorf("it drew %d tables, want all 5", got)
	}
	if strings.Contains(tb.footer.Text, "Around") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// Focusing is offered on the table somebody is looking at, which is the one
// they chose: a starting point asked for in a list they have to read is a
// starting point they have to find twice.
func TestFocusingIsOfferedOnWhatIsChosen(t *testing.T) {
	_, _, p := diagrammed(t)
	if !p.chosen.Disabled() {
		t.Error("with nothing chosen there is a table to focus on")
	}
	if !p.all.Disabled() {
		t.Error("showing everything is offered on a diagram of everything")
	}

	p.refreshFocus("main.items")
	if p.chosen.Disabled() {
		t.Error("with a table chosen there is nothing to focus on")
	}
	if !strings.Contains(p.chosen.Text, "main.items") {
		t.Errorf("it offers %q", p.chosen.Text)
	}

	p.focusOn("main.items")
	if p = p.t.diagram; p.all.Disabled() {
		t.Error("on a narrowed diagram, showing everything is not offered")
	}
}

// How far out is said in words: "one relationship away" says what it does
// and a bare number does not.
func TestHowFarOutIsSaidInWords(t *testing.T) {
	for n, want := range map[int]string{0: "Just that table", 1: "1 relationship away",
		2: "2 relationships away", 3: "3 relationships away"} {
		if got := degreeNames[n]; got != want {
			t.Errorf("%d is called %q, want %q", n, got, want)
		}
		if got := degreeOf(want); got != n {
			t.Errorf("%q read back as %d, want %d", want, got, n)
		}
	}
	// A name nobody offers reads as one relationship, which is the useful
	// answer rather than none at all.
	if got := degreeOf("whatever"); got != 1 {
		t.Errorf("an unknown name read as %d", got)
	}
}

// Focusing on nothing changes nothing. The button is disabled with nothing
// chosen, and calling it anyway is neither a way to empty the diagram nor a
// way to widen one somebody has narrowed.
func TestFocusingOnNothing(t *testing.T) {
	_, tb, p := diagrammed(t)
	p.focusOn("main.items")
	p = tb.diagram
	before := namesIn(p)

	p.focusOn("")
	if got := namesIn(tb.diagram); !slices.Equal(got, before) {
		t.Errorf("it drew %v, was %v", got, before)
	}
	if tb.diagram.focus != "main.items" {
		t.Errorf("it is now focused on %q", tb.diagram.focus)
	}
}

// A diagram opened on a schema draws that schema, and still does after it
// has been narrowed and widened again: what to draw is remembered rather
// than worked out afresh from a node nobody has any more.
func TestADiagramOfOneSchemaStaysThatSchema(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	tb := fx.s.OpenDiagram(c.ID, model.NewRef(model.KindSchema, "main", "main"))
	pump(t, fx.q, func() bool { return tb.diagram != nil })

	if got := tb.diagram.opt.Schemas; !slices.Equal(got, []string{"main"}) {
		t.Fatalf("it was asked for %v", got)
	}
	before := namesIn(tb.diagram)
	// One schema, not the other: a diagram of a schema is of that schema.
	if slices.Contains(before, "audit.log") {
		t.Errorf("a diagram of main drew %v", before)
	}

	tb.diagram.focusOn("main.items")
	tb.diagram.showEverything()
	if got := namesIn(tb.diagram); !slices.Equal(got, before) {
		t.Errorf("after narrowing and widening it draws %v, was %v", got, before)
	}
	if got := tb.diagram.opt.Schemas; !slices.Equal(got, []string{"main"}) {
		t.Errorf("it is now drawing %v", got)
	}
}

// A narrowed diagram is fitted rather than shown at the view kept for a
// larger one, which would look like a failure: a corner of a picture that no
// longer exists.
func TestANarrowedDiagramIsFitted(t *testing.T) {
	fx, tb, p := diagrammed(t)

	// Somebody has been looking closely at one corner, and that is kept.
	p.w.View().Zoom = 2.5
	p.w.View().Pan = canvas.Point{X: 400, Y: 300}
	p.w.Graph().Nodes[0].Pinned = true
	p.keep()
	pump(t, fx.q, func() bool {
		l, ok := p.arrangement()
		return ok && l.Zoom == 2.5
	})

	p.focusOn("main.items")
	if got := tb.diagram.w.View().Zoom; got == 2.5 {
		t.Errorf("the narrowed diagram is at the zoom kept for the whole one: %v", got)
	}
	// Fitted means the whole of what is drawn is on screen.
	view, whole := tb.diagram.w.View().VisibleRect(), tb.diagram.w.Graph().Bounds()
	if view.Width() < whole.Width() || view.Height() < whole.Height() {
		t.Errorf("it shows %v of %v", view, whole)
	}
}

// Choosing a box is what offers focusing on it: a starting point asked for
// in a list somebody has to read is one they have to find twice.
func TestChoosingABoxOffersFocusingOnIt(t *testing.T) {
	_, _, p := diagrammed(t)
	if !p.chosen.Disabled() {
		t.Fatal("with nothing chosen there is a table to focus on")
	}
	p.w.OnSelect("main.items")
	if p.chosen.Disabled() {
		t.Error("after choosing a box, focusing on it is not offered")
	}
	if !strings.Contains(p.chosen.Text, "main.items") {
		t.Errorf("it offers %q", p.chosen.Text)
	}
}
