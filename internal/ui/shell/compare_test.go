package shell

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/diff"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/schemafile"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Showing what differs (FR-7.2).

// savedModel writes a model to compare the fake connection against. The
// fake's database holds one table, items, with id and name.
func savedModel(t *testing.T, change func(*model.Database)) string {
	t.Helper()
	db := &model.Database{Name: "main", Schemas: []model.Schema{{Name: "main",
		Tables: []model.Table{{Name: "items", RowsEstimate: -1, Columns: fakeColumns()}}}}}
	if change != nil {
		change(db)
	}
	dir := filepath.Join(t.TempDir(), "model")
	if err := schemafile.Write(dir, db); err != nil {
		t.Fatal(err)
	}
	return dir
}

// comparing opens a comparison against a saved model and waits for it.
func comparing(t *testing.T, change func(*model.Database)) (*fixture, *tab, *comparePanel) {
	t.Helper()
	fx := newFixture(t)
	c := selectItems(t, fx)
	dir := savedModel(t, change)
	tb := fx.s.OpenComparison(c.ID, model.NewRef(model.KindDatabase, "main"), dir)
	pump(t, fx.q, func() bool { return tb.compare != nil })
	return fx, tb, tb.compare
}

// A database compared against a model of itself differs in nothing, and says
// so rather than showing an empty tree.
func TestADatabaseThatMatchesTheSavedModel(t *testing.T) {
	_, tb, p := comparing(t, nil)
	if tb.item.Text != "Compare: main" {
		t.Errorf("the tab is called %q", tb.item.Text)
	}
	if got := p.summary.Text; !strings.HasPrefix(got, "Nothing differs.") {
		t.Errorf("it says %q", got)
	}
	if p.root.Differs() {
		t.Errorf("it compared as %s", p.root.Status)
	}
	if !strings.Contains(tb.footer.Text, "changes neither") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// What the database is missing and what only it has are named as those, not
// as "added" and "removed", which are ambiguous with two sides in front of
// somebody.
func TestWhatEachSideIsMissingIsNamedFromWhereYouAreStanding(t *testing.T) {
	_, _, p := comparing(t, func(db *model.Database) {
		// A table the saved model has and the database does not.
		db.Schemas[0].Tables = append(db.Schemas[0].Tables,
			model.Table{Name: "orders", RowsEstimate: -1})
	})
	missing := p.find(t, "orders")
	if missing.Status != diff.Added {
		t.Fatalf("it compared as %s", missing.Status)
	}
	if got := rowText(missing); got != "orders — missing here" {
		t.Errorf("the row reads %q", got)
	}
	if got := statusLine(missing); !strings.Contains(got, "not in this database") {
		t.Errorf("it says %q", got)
	}
	// And the other way round, for something only the database has.
	only := diff.Node{Name: "extra", Status: diff.Removed}
	if got := rowText(only); got != "extra — only here" {
		t.Errorf("the row reads %q", got)
	}
	if got := statusLine(only); !strings.Contains(got, "not in the saved model") {
		t.Errorf("it says %q", got)
	}
}

// find walks the panel's tree to a node by name.
func (p *comparePanel) find(t *testing.T, name string) diff.Node {
	t.Helper()
	for _, n := range p.nodes {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("no %q in the comparison", name)
	return diff.Node{}
}

// findID is the same, answering the id the tree knows it by.
func (p *comparePanel) findID(t *testing.T, name string) string {
	t.Helper()
	for id, n := range p.nodes {
		if n.Name == name {
			return id
		}
	}
	t.Fatalf("no %q in the comparison", name)
	return ""
}

// Everything compared is in the tree, identical objects included, because a
// tree of differences alone could not be filtered into one that shows them.
func TestTheWholeComparisonIsKeptAndTheFilterChangesWhatIsDrawn(t *testing.T) {
	_, _, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables = append(db.Schemas[0].Tables,
			model.Table{Name: "orders", RowsEstimate: -1})
	})
	items := p.findID(t, "items")
	orders := p.findID(t, "orders")

	p.filter = showEverything
	if !p.visible(items) || !p.visible(orders) {
		t.Error("showing everything hides something")
	}
	p.filter = showDifferences
	if p.visible(items) {
		t.Error("a table that did not change is drawn among the differences")
	}
	if !p.visible(orders) {
		t.Error("the table that differs is not drawn")
	}
	p.filter = showRemoved
	if p.visible(orders) {
		t.Error("a missing table is drawn as one only this database has")
	}
	// The whole comparison is still there whatever is drawn.
	if _, held := p.nodes[items]; !held {
		t.Error("filtering threw away what it did not draw")
	}
}

// A filter keeps the way down to what it draws, or what it draws could not
// be reached.
func TestAFilterKeepsTheWayDownToWhatItDraws(t *testing.T) {
	_, _, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables[0].Columns = append(db.Schemas[0].Tables[0].Columns,
			model.Column{Name: "note", Position: 3,
				Type: model.DataType{Class: model.TypeString, Native: "text", Length: -1}})
	})
	p.filter = showAdded
	// The column is what is missing; its table and schema are only changed,
	// and both have to be drawn to reach it.
	for _, name := range []string{"note", "items", "main"} {
		if !p.visible(p.findID(t, name)) {
			t.Errorf("%q is not drawn, so the column under it cannot be reached", name)
		}
	}
	// And a table with nothing missing under it is not drawn.
	if p.visible(p.findID(t, "id")) {
		t.Error("a column that did not change is drawn when filtering to what is missing")
	}
}

// Choosing an object shows what differs about it, with both values.
func TestChoosingAnObjectShowsBothValues(t *testing.T) {
	_, _, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables[0].Columns[1].Type.Native = "varchar(40)"
	})
	p.showDetail(p.findID(t, "name"))
	said := labelText(p.detail)
	if !strings.Contains(said, "text") || !strings.Contains(said, "varchar(40)") {
		t.Errorf("the detail says %q", said)
	}
	if !strings.Contains(said, "In the saved model") || !strings.Contains(said, "Here") {
		t.Errorf("it does not say which side is which: %q", said)
	}
}

// A value that is absent on one side reads as absent rather than as a blank
// cell nobody can tell from a space.
func TestAValueThatIsNotThere(t *testing.T) {
	if got := blankAsNothing(""); got != "—" {
		t.Errorf("nothing reads as %q", got)
	}
	if got := blankAsNothing("text"); got != "text" {
		t.Errorf("a value reads as %q", got)
	}
}

// The line above the tree says how much there is before anybody reads it.
func TestTheSummaryCountsBothSides(t *testing.T) {
	_, _, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables = append(db.Schemas[0].Tables,
			model.Table{Name: "orders", RowsEstimate: -1})
	})
	got := p.summary.Text
	for _, want := range []string{"1 object missing here", "0 objects only here", "changed", "the same"} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary says %q, with no %q in it", got, want)
		}
	}
}

// A comparison is offered where the structure can be read, and on what holds
// the objects rather than on one of them.
func TestWhichObjectsAreOfferedAComparison(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	// The selection is a table, which is not what a schema comparison is of.
	if fx.s.canCompareSelected() {
		t.Error("a table is offered a schema comparison")
	}
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	if !fx.s.canCompareSelected() {
		t.Error("a database is not offered a schema comparison")
	}
	if fx.s.menuItems[cmdCompare].Disabled {
		t.Error("the menu item is disabled for a database")
	}
	fx.s.release(c.ID, false)
	if fx.s.canCompareSelected() {
		t.Error("a closed connection is offered a comparison")
	}
}

// The chooser picks a file inside the model, because a model is a tree of
// them; the model is the directory it is in.
func TestChoosingASavedModelTakesTheDirectoryItIsIn(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	fx.s.run(cmdCompare)

	if len(fx.files.opens) != 1 {
		t.Fatalf("it asked for %d files", len(fx.files.opens))
	}
	if got := fx.files.opens[0].Extensions; !slices.Contains(got, "json") {
		t.Errorf("it offers %q", got)
	}
	dir := savedModel(t, nil)
	fx.files.answer(filepath.Join(dir, "database.json"), nil)
	pump(t, fx.q, func() bool { return len(fx.s.open) > 0 && fx.s.open[len(fx.s.open)-1].compare != nil })
	if got := fx.s.open[len(fx.s.open)-1].item.Text; got != "Compare: main" {
		t.Errorf("it opened %q", got)
	}
}

// Comparing the same two things twice brings the tab already on them
// forward: two comparisons of one pair could disagree, and reading both
// would be reading one of them for nothing.
func TestComparingTheSameTwoThingsTwiceOpensOneTab(t *testing.T) {
	fx, tb, _ := comparing(t, nil)
	again := fx.s.OpenComparison(tb.connID, tb.ref, strings.TrimPrefix(tb.key,
		"compare:"+tb.connID+":"+tb.ref.String()+":"))
	if again != tb {
		t.Error("it opened a second comparison of the same two things")
	}
	if got := len(fx.s.open); got != 1 {
		t.Errorf("%d tabs are open", got)
	}
}

// A model that is not there fails the tab rather than showing an empty
// comparison, which would read as a database with nothing in it.
func TestComparingAgainstAModelThatIsNotThere(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	tb := fx.s.OpenComparison(c.ID, model.NewRef(model.KindDatabase, "main"), t.TempDir())
	pump(t, fx.q, func() bool { return strings.Contains(labelText(tb.body), "could not compare") })
	if tb.compare != nil {
		t.Error("it drew a comparison it could not make")
	}
}
