package shell

import (
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/query"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// The way into the visual query designer (FR-9.1).

// designing opens a designer over the fake's schema and waits for it.
func designingQuery(t *testing.T) (*fixture, *tab, *designerPanel) {
	t.Helper()
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	tb := fx.s.OpenDesigner(c.ID, model.NewRef(model.KindDatabase, "main"))
	pump(t, fx.q, func() bool { return tb.designer != nil })
	return fx, tb, tb.designer
}

func TestADesignerOpensEmptyAndSaysSo(t *testing.T) {
	_, tb, p := designingQuery(t)
	if tb.item.Text != "Design: main" {
		t.Errorf("the tab is called %q", tb.item.Text)
	}
	if len(p.w.Graph().Nodes) != 0 {
		t.Errorf("it opened with %d tables on it", len(p.w.Graph().Nodes))
	}
	// A designer showing nothing with no explanation would leave somebody
	// guessing what it was waiting for.
	if !strings.Contains(tb.footer.Text, "Add a table") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// Opening it twice is one tab: the design is in it, and a second tab would be
// a second design over the same schema with no way to tell them apart.
func TestOpeningADesignerTwiceIsOneTab(t *testing.T) {
	fx, tb, _ := designingQuery(t)
	if again := fx.s.OpenDesigner(tb.connID, tb.ref); again != tb {
		t.Error("it opened a second designer over the same schema")
	}
}

// pickIn is the overlay's picker, which must be there.
func pickIn(t *testing.T, o fyne.CanvasObject) *widget.Select {
	t.Helper()
	got := findSelect(o)
	if got == nil {
		t.Fatal("there is nothing to choose from")
	}
	return got
}

// addTable is the dialog, answered.
func addTable(t *testing.T, fx *fixture, p *designerPanel, name string) {
	t.Helper()
	p.addTable()
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("it asked nothing")
	}
	pick := findSelect(top)
	if pick == nil {
		t.Fatal("there is nothing to choose from")
	}
	pick.SetSelected(name)
	test.Tap(findButton(top, "Add"))
}

// A table added is a box on the canvas, with its columns.
func TestATableAddedIsABoxWithItsColumns(t *testing.T) {
	fx, tb, p := designingQuery(t)
	addTable(t, fx, p, "main.orders")
	g := p.w.Graph()
	if len(g.Nodes) != 1 || g.Nodes[0].ID != "orders" {
		t.Fatalf("it drew %+v", g.Nodes)
	}
	if len(g.Nodes[0].Ports) != 2 {
		t.Errorf("its columns are %+v", g.Nodes[0].Ports)
	}
	if !strings.Contains(tb.footer.Text, "1 table") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// Two tables the schema relates are offered a join, and taking it draws the
// line. Nothing is joined until somebody asks: a join the catalogue implied is
// a suggestion, and one accepted is a decision (FR-9.1).
func TestTheJoinsTheSchemaImpliesAreOfferedAndNotTaken(t *testing.T) {
	fx, tb, p := designingQuery(t)
	addTable(t, fx, p, "main.orders")
	addTable(t, fx, p, "main.items")
	if len(p.w.Graph().Edges) != 0 {
		t.Fatalf("it drew a join nobody asked for: %+v", p.w.Graph().Edges)
	}
	if p.suggest.Disabled() {
		t.Fatal("there is a join to suggest and the button is off")
	}
	// Until then the design is not a query: a table nothing joins would be a
	// cross product with everything before it.
	if !strings.Contains(tb.footer.Text, "no join reaches") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	test.Tap(p.suggest)
	edges := p.w.Graph().Edges
	if len(edges) != 1 {
		t.Fatalf("it drew %+v", edges)
	}
	if !strings.Contains(edges[0].Label, "from the schema") {
		t.Errorf("the line is labelled %q", edges[0].Label)
	}
	if !p.suggest.Disabled() {
		t.Error("there is nothing left to suggest and the button is still on")
	}
	if !strings.Contains(tb.footer.Text, "1 join") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	// And the joins list says what it does, in words rather than as a line to
	// click on (NFR-A1).
	if said := p.joinLabel(0); !strings.Contains(said, "orders.item = items.id") {
		t.Errorf("the list says %q", said)
	}
}

// A join can be changed: its kind, and the columns it matches. A join somebody
// changed is theirs rather than the schema's, and the canvas says so.
func TestAJoinCanBeChanged(t *testing.T) {
	fx, _, p := designingQuery(t)
	addTable(t, fx, p, "main.orders")
	addTable(t, fx, p, "main.items")
	test.Tap(p.suggest)

	p.editJoin(0)
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("it asked nothing")
	}
	pickIn(t, top).SetSelected(string(query.JoinLeft))
	test.Tap(findButton(top, "Change"))

	if len(p.design.Joins) != 1 || p.design.Joins[0].Kind != query.JoinLeft {
		t.Fatalf("its joins are %+v", p.design.Joins)
	}
	if p.design.Joins[0].Inferred {
		t.Error("a join somebody changed still says the schema suggested it")
	}
	if label := p.joinLabel(0); !strings.HasPrefix(label, "LEFT") {
		t.Errorf("the list says %q", label)
	}
	if e := p.w.Graph().Edges[0]; e.Label != "LEFT" {
		t.Errorf("the line is labelled %q", e.Label)
	}
	// The columns it matches survived being left alone.
	if len(p.design.Joins[0].On) != 1 || p.design.Joins[0].On[0].Left != "item" {
		t.Errorf("it matches %+v", p.design.Joins[0].On)
	}
}

// A cross join matches nothing, so changing a join to one takes its columns
// away: keeping them would render a statement no server takes.
func TestACrossJoinLosesItsColumns(t *testing.T) {
	fx, _, p := designingQuery(t)
	addTable(t, fx, p, "main.orders")
	addTable(t, fx, p, "main.items")
	test.Tap(p.suggest)
	p.editJoin(0)
	top := fx.s.win.Canvas().Overlays().Top()
	pickIn(t, top).SetSelected(string(query.JoinCross))
	test.Tap(findButton(top, "Change"))
	if len(p.design.Joins[0].On) != 0 {
		t.Errorf("it still matches %+v", p.design.Joins[0].On)
	}
	// And it renders, which is the whole point of taking them away.
	if err := p.design.Validate(); err != nil {
		t.Errorf("it is not a query: %v", err)
	}
}

// A join removed is a line gone, and the tables stay.
func TestAJoinCanBeRemoved(t *testing.T) {
	fx, _, p := designingQuery(t)
	addTable(t, fx, p, "main.orders")
	addTable(t, fx, p, "main.items")
	test.Tap(p.suggest)
	p.removeJoin(0)
	if len(p.design.Joins) != 0 || len(p.w.Graph().Edges) != 0 {
		t.Errorf("its joins are %+v", p.design.Joins)
	}
	if len(p.design.Tables) != 2 {
		t.Errorf("it removed a table too: %v", p.design.Aliases())
	}
	// Removing one that is not there changes nothing.
	p.removeJoin(0)
	p.removeJoin(-1)
}

// The same table twice is two boxes, told apart by the name the query calls
// them: a self-join is the commonest thing a designer is used for.
func TestTheSameTableTwiceIsTwoBoxes(t *testing.T) {
	fx, _, p := designingQuery(t)
	addTable(t, fx, p, "main.items")
	addTable(t, fx, p, "main.items")
	var ids []string
	for _, n := range p.w.Graph().Nodes {
		ids = append(ids, n.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"items", "items2"}) {
		t.Errorf("it drew %v", ids)
	}
}

// A table removed takes its joins with it: a design that kept them would
// render a statement naming a table that is not in it.
func TestATableRemovedTakesItsJoins(t *testing.T) {
	fx, _, p := designingQuery(t)
	addTable(t, fx, p, "main.orders")
	addTable(t, fx, p, "main.items")
	test.Tap(p.suggest)
	if p.remove.Disabled() != true {
		t.Error("nothing is chosen and Remove is on")
	}
	// Choosing a box is what the Remove button is about, and it names it. The
	// canvas reports a choice through OnSelect, which is what the widget calls
	// when a box is tapped.
	p.w.OnSelect("items")
	if p.w.Selected() != "" && p.w.Selected() != "items" {
		t.Fatalf("the canvas says %q is chosen", p.w.Selected())
	}
	if p.remove.Disabled() {
		t.Fatal("a box is chosen and Remove is off")
	}
	if !strings.Contains(p.remove.Text, "items") {
		t.Errorf("the button says %q", p.remove.Text)
	}
	test.Tap(p.remove)
	if len(p.design.Tables) != 1 || p.design.Tables[0].Alias != "orders" {
		t.Fatalf("the canvas holds %v", p.design.Aliases())
	}
	if len(p.design.Joins) != 0 {
		t.Errorf("a join to a table that has gone survived: %+v", p.design.Joins)
	}
}

// The design becomes SQL in a query tab: unsaved and unrun, as every script
// this window writes is.
func TestADesignOpensAsAQuery(t *testing.T) {
	fx, tb, p := designingQuery(t)
	addTable(t, fx, p, "main.orders")
	addTable(t, fx, p, "main.items")
	test.Tap(p.suggest)
	before := len(fx.s.open)
	p.openAsQuery()
	if len(fx.s.open) != before+1 {
		t.Fatalf("it opened %d tabs", len(fx.s.open)-before)
	}
	opened := fx.s.open[len(fx.s.open)-1]
	if opened.connID != tb.connID {
		t.Errorf("it opened on %q", opened.connID)
	}
	sql := opened.query.editor.Document().Text()
	for _, want := range []string{"SELECT", `"orders".*`, `"items".*`, "INNER JOIN", `"orders"."item" = "items"."id"`} {
		if !strings.Contains(sql, want) {
			t.Errorf("it wrote\n%s\nwhich lacks %q", sql, want)
		}
	}
}

// A design that is not a query yet says so rather than opening an empty tab.
func TestADesignThatIsNotAQueryYetSaysSo(t *testing.T) {
	fx, _, p := designingQuery(t)
	addTable(t, fx, p, "main.orders")
	addTable(t, fx, p, "main.items")
	before := len(fx.s.open)
	p.openAsQuery() // nothing joins them
	if len(fx.s.open) != before {
		t.Errorf("it opened a tab for a design that is not a query")
	}
	if !fx.s.errors.shown() {
		t.Error("it said nothing")
	}
	if !strings.Contains(fx.s.errors.message.Text, "not a query yet") {
		t.Errorf("it says %q", fx.s.errors.message.Text)
	}
}

// A source with no SQL is not offered a designer: a design becomes a statement,
// and there would be nothing to write it in (REQ-DB-2).
//
// noSQL wraps a source so that only what source.Source declares is promoted,
// which is what a source without a query language looks like — every driver
// that has one has it as methods of its own type, so the only way to make one
// without is to hide them.
type noSQL struct{ source.Source }

func TestASourceWithNoSQLIsNotOfferedADesigner(t *testing.T) {
	full := fakeSource{}
	if !canDesign(full) {
		t.Fatal("a source with a schema and SQL is not offered one")
	}
	if canDesign(noSQL{full}) {
		t.Error("a source with no SQL is offered one")
	}
}

// A box keeps its place when something else is added: a table dragged into a
// corner that jumped on the next change would be a canvas nobody could arrange.
func TestABoxKeepsItsPlaceWhenATableIsAdded(t *testing.T) {
	fx, _, p := designingQuery(t)
	addTable(t, fx, p, "main.orders")
	// Put somewhere nothing would lay it out, so that keeping it and laying it
	// out afresh cannot be mistaken for each other.
	was := canvas.Point{X: -777, Y: 555}
	p.w.Graph().NodeByID("orders").Pos = was

	addTable(t, fx, p, "main.items")
	after := p.w.Graph().NodeByID("orders")
	if after == nil {
		t.Fatal("the first table has gone")
	}
	if after.Pos != was {
		t.Errorf("it moved from %v to %v", was, after.Pos)
	}
	// And the one added is somewhere of its own rather than on top of it.
	other := p.w.Graph().NodeByID("items")
	if other == nil {
		t.Fatal("the second table was not drawn")
	}
	if other.Pos == was {
		t.Errorf("both boxes are at %v", was)
	}
}

// The command is offered where a schema is, and not on a source with no SQL to
// write at the end of it.
func TestWhereDesigningAQueryIsOffered(t *testing.T) {
	fx := newFixture(t)
	if fx.s.canDesignQuery() {
		t.Error("it is offered with nothing selected")
	}
	c := selectItems(t, fx)
	if fx.s.canDesignQuery() {
		t.Error("a table is offered a designer of itself")
	}
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	if !fx.s.canDesignQuery() {
		t.Error("a database is not offered a designer")
	}
	if fx.s.menuItems[cmdDesignQuery].Disabled {
		t.Error("the menu item is disabled for a database")
	}
	fx.s.release(c.ID, false)
	if fx.s.canDesignQuery() {
		t.Error("a closed connection is offered a designer")
	}
}
