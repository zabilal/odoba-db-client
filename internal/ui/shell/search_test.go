package shell

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Searching a database's structure (FR-2.7).

// selectMain selects the database, which is what a search is over.
func selectMain(t *testing.T, fx *fixture) {
	t.Helper()
	selectItems(t, fx)
	fx.s.Explorer.Tree.Select(view.NodeID(fx.conns.List()[0].ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
}

// The search is offered on what holds objects, and not on one of them.
func TestWhatIsOfferedAStructureSearch(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	if fx.s.canSearchStructure() {
		t.Error("a table is offered a search of what it holds")
	}
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	if !fx.s.canSearchStructure() {
		t.Error("a database is not offered a search")
	}
	if fx.s.menuItems[cmdSearchStructure].Disabled {
		t.Error("the menu item is disabled for a database")
	}
	// Disconnected rather than released, so that the node stays selected
	// and it is being open that is in question.
	disconnected(t, fx, c.ID)
	fx.s.sync()
	if fx.s.canSearchStructure() {
		t.Error("a connection that is not open is offered a search")
	}
	// And the panel does not open on an object either, whatever asks.
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, itemsNode.Ref))
	fx.s.sync()
	fx.s.searchStructure()
	if fx.s.panelIs(panelSearch) {
		t.Error("a table opened a search of what it holds")
	}
}

// A column is found and listed, with the object it is in and the line it
// is on, and the panel says how many there are.
func TestASearchListsWhatItFinds(t *testing.T) {
	fx := newFixture(t)
	selectMain(t, fx)
	fx.s.run(cmdSearchStructure)
	p := fx.s.searchView
	if p == nil {
		t.Fatal("the panel did not open")
	}
	p.find.SetText("name")
	fx.q.Run(p.run)
	pump(t, fx.q, func() bool { return p.task == nil && len(p.hits) > 0 })
	var where []string
	for _, h := range p.hits {
		where = append(where, h.Node.Ref.Name()+": "+h.In)
	}
	if !has(where, "items: column name") {
		t.Errorf("it found %v", where)
	}
	if got := p.status.Text; !strings.Contains(got, "2 matches in main") {
		t.Errorf("the panel says %q", got)
	}
}

// Picking a match opens what it is in.
func TestPickingAMatchOpensTheObject(t *testing.T) {
	fx := newFixture(t)
	selectMain(t, fx)
	fx.s.run(cmdSearchStructure)
	p := fx.s.searchView
	p.find.SetText("name")
	fx.q.Run(p.run)
	pump(t, fx.q, func() bool { return p.task == nil && len(p.hits) > 0 })

	fx.q.Run(func() { p.open(p.hits[0]) })
	pump(t, fx.q, func() bool { return len(fx.s.open) == 1 })
	tb := fx.s.open[0]
	if tb.ref.Name() != "items" || tb.structure {
		t.Errorf("it opened %q, structure %v; a table's match opens its rows", tb.ref.Name(), tb.structure)
	}
	// A match in something with no rows opens its structure instead.
	fx.q.Run(func() {
		p.open(app.Hit{Node: model.Node{Ref: model.NewRef(model.KindRoutine, "main", "settle"),
			Label: "settle", Describable: true}, In: "the body"})
	})
	pump(t, fx.q, func() bool { return len(fx.s.open) == 2 })
	if got := fx.s.open[1]; !got.structure {
		t.Errorf("a routine's match opened %+v rather than its structure", got.ref)
	}
}

// Searching for nothing asks the server nothing and says what to do.
func TestASearchForNothingSaysWhatToDo(t *testing.T) {
	fx := newFixture(t)
	selectMain(t, fx)
	fx.s.run(cmdSearchStructure)
	p := fx.s.searchView
	p.find.SetText("   ")
	fx.q.Run(p.run)
	fx.q.Flush()
	if got := p.status.Text; got != "Type what to look for and press Return." {
		t.Errorf("a search for nothing said %q", got)
	}
	if len(p.hits) != 0 {
		t.Errorf("it found %+v", p.hits)
	}
}

// Matches from a search that has been replaced are dropped: they are the
// last question's answer, and the list is showing this one's.
func TestMatchesFromAReplacedSearchAreDropped(t *testing.T) {
	fx := newFixture(t)
	selectMain(t, fx)
	fx.s.run(cmdSearchStructure)
	p := fx.s.searchView
	p.task = &task{}
	fx.q.Run(func() { p.add(&task{}, []app.Hit{{In: "column name"}}) })
	fx.q.Flush()
	if len(p.hits) != 0 {
		t.Errorf("the list took %+v from a search it is no longer showing", p.hits)
	}
}

// A second search replaces the first's matches rather than adding to them.
func TestASecondSearchReplacesTheFirst(t *testing.T) {
	fx := newFixture(t)
	selectMain(t, fx)
	fx.s.run(cmdSearchStructure)
	p := fx.s.searchView
	p.find.SetText("name")
	fx.q.Run(p.run)
	pump(t, fx.q, func() bool { return p.task == nil && len(p.hits) > 0 })
	first := len(p.hits)

	p.find.SetText("nothing holds this")
	fx.q.Run(p.run)
	fx.q.Flush()
	if len(p.hits) != 0 {
		t.Errorf("%d matches of %d were left over when the next search started", len(p.hits), first)
	}
	pump(t, fx.q, func() bool { return strings.Contains(p.status.Text, "Nothing in main") })
}

// Closing the panel stops the search: nobody can see what it finds.
func TestClosingThePanelStopsTheSearch(t *testing.T) {
	fx := newFixture(t)
	selectMain(t, fx)
	fx.s.run(cmdSearchStructure)
	// A panel with a search running, which is the state closing it has to
	// deal with. The fake answers too fast for a real one to still be
	// going by the time the panel closes.
	p, stopped := fx.s.searchView, false
	p.stop, p.task = func() { stopped = true }, &task{}
	fx.q.Run(fx.s.closePanel)
	fx.q.Flush()
	if !stopped || p.stop != nil || p.task != nil {
		t.Errorf("the search went on after its panel closed: stopped %v", stopped)
	}
}

// What is counted is counted in English: a noun ending in a sibilant
// takes "es", and "2 matchs" is not a thing anybody wrote.
func TestCountingSaysThePluralRight(t *testing.T) {
	for _, c := range []struct {
		n          int
		noun, want string
	}{
		{1, "match", "1 match"},
		{2, "match", "2 matches"},
		{2, "index", "2 indexes"},
		{2, "row", "2 rows"},
		{1000, "row", "1,000 rows"},
	} {
		if got := nounCount(c.n, c.noun); got != c.want {
			t.Errorf("nounCount(%d, %q) = %q, want %q", c.n, c.noun, got, c.want)
		}
	}
}
