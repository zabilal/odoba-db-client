package shell

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

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

// Choosing differences, and writing a script for them (FR-7.3).

// A difference is ticked, and what is the same cannot be: there is nothing
// to write for it, and a tick box that does nothing is one somebody ticks.
func TestOnlyADifferenceCanBeChosen(t *testing.T) {
	_, tb, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables = append(db.Schemas[0].Tables,
			model.Table{Name: "orders", RowsEstimate: -1})
	})
	if !p.write.Disabled() {
		t.Error("with nothing chosen there is a script to write")
	}

	p.choose(p.findID(t, "orders"), true)
	if p.write.Disabled() {
		t.Error("with a difference chosen there is no script to write")
	}
	if !strings.Contains(tb.footer.Text, "1 difference chosen") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}

	// Unticking it puts things back.
	p.choose(p.findID(t, "orders"), false)
	if !p.write.Disabled() || strings.Contains(tb.footer.Text, "chosen") {
		t.Errorf("after unticking: %v, %q", p.write.Disabled(), tb.footer.Text)
	}

	// And what is the same has no tick box to tick: there is nothing to
	// write for it, and one that does nothing is one somebody will tick.
	if tick := p.tickFor(t, "id"); !tick.Disabled() {
		t.Error("a column that is the same on both sides can be chosen")
	}
	if tick := p.tickFor(t, "orders"); tick.Disabled() {
		t.Error("the table that differs cannot be chosen")
	}
}

// tickFor draws a node's row and answers the tick box in it.
func (p *comparePanel) tickFor(t *testing.T, name string) *widget.Check {
	t.Helper()
	row := container.NewHBox(widget.NewCheck("", nil), widget.NewIcon(theme.DocumentIcon()),
		widget.NewLabel(""))
	p.draw(p.findID(t, name), row)
	tick, ok := row.Objects[0].(*widget.Check)
	if !ok {
		t.Fatalf("the row holds %T", row.Objects[0])
	}
	return tick
}

// What is the same is never chosen, whatever is ticked above it.
func TestWhatIsTheSameIsNeverChosen(t *testing.T) {
	_, _, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables[0].Columns[1].Type.Native = "varchar(40)"
	})
	p.choose(p.findID(t, "main"), true) // the schema, and everything under it
	if p.chosen[p.findID(t, "id")] {
		t.Error("a column that is the same on both sides was chosen")
	}
	if !p.chosen[p.findID(t, "name")] {
		t.Error("the column that differs was not chosen")
	}
}

// Choosing an object chooses what is under it, because a table is one
// decision and its columns are not separate ones when it is being made.
func TestChoosingAnObjectChoosesWhatIsUnderIt(t *testing.T) {
	_, _, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables[0].Columns[1].Type.Native = "varchar(40)"
		db.Schemas[0].Tables[0].Columns = append(db.Schemas[0].Tables[0].Columns,
			model.Column{Name: "note", Position: 3,
				Type: model.DataType{Class: model.TypeString, Native: "text", Length: -1}})
	})
	p.choose(p.findID(t, "items"), true)
	for _, name := range []string{"items", "name", "note"} {
		if !p.chosen[p.findID(t, name)] {
			t.Errorf("%q was not chosen", name)
		}
	}
}

// The script is written from what was chosen, and nothing runs.
func TestWritingTheScriptForWhatWasChosen(t *testing.T) {
	forgetStatements()
	fx, tb, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables[0].Columns = append(db.Schemas[0].Tables[0].Columns,
			model.Column{Name: "note", Position: 3,
				Type: model.DataType{Class: model.TypeString, Native: "text", Length: -1}})
	})
	p.choose(p.findID(t, "note"), true)
	p.writeScript()

	got := newestQuery(t, fx, 1)
	if !strings.Contains(got, "note") {
		t.Errorf("the script is %q", got)
	}
	if !strings.HasPrefix(got, "-- 1 difference chosen") {
		t.Errorf("it does not say what it is: %q", got)
	}
	if !strings.Contains(got, "Nothing here has run") {
		t.Errorf("it does not say that nothing ran: %q", got)
	}
	if n := len(ranStatements()); n != 0 {
		t.Errorf("%d statements ran; a script is written, never run", n)
	}
	_ = tb
}

// A selection that renders nothing says so rather than opening an empty tab.
func TestChoosingSomethingThatNeedsNoStatements(t *testing.T) {
	fx, tb, p := comparing(t, nil)
	before := len(fx.s.open)
	p.chosen[p.findID(t, "items")] = true
	p.writeScript()
	if got := len(fx.s.open); got != before {
		t.Errorf("%d tabs are open, was %d", got, before)
	}
	if !strings.Contains(tb.footer.Text, "needs no statements") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// Applying a script, or keeping it (FR-7.4).

// Applying goes through the preview, like every other structural change:
// what is read is what runs.
func TestApplyingWhatWasChosenIsReadFirst(t *testing.T) {
	forgetStatements()
	fx, tb, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables[0].Columns = append(db.Schemas[0].Tables[0].Columns,
			model.Column{Name: "note", Position: 3,
				Type: model.DataType{Class: model.TypeString, Native: "text", Length: -1}})
	})
	p.choose(p.findID(t, "note"), true)
	p.applyScript()

	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	shown := previewText(fx.s.win.Canvas().Overlays().Top())
	if !strings.Contains(shown, "note") {
		t.Errorf("the preview shows %q", shown)
	}
	if n := len(ranStatements()); n != 0 {
		t.Errorf("%d statements ran before the preview was answered", n)
	}

	tapOnTop(t, fx, "Run")
	pump(t, fx.q, func() bool { return len(ranStatements()) > 0 })
	if got := ranStatements()[0]; !strings.Contains(got, "note") {
		t.Errorf("it ran %q, which is not what was read", got)
	}
	// And both sides are read again, because what ran has moved one of them.
	pump(t, fx.q, func() bool { return tb.compare != nil && tb.compare != p })
}

// The script is kept as a file, named after what was compared.
func TestSavingTheScriptToAFile(t *testing.T) {
	forgetStatements()
	fx, tb, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables = append(db.Schemas[0].Tables,
			model.Table{Name: "orders", RowsEstimate: -1})
	})
	p.choose(p.findID(t, "orders"), true)
	p.saveScript()

	if len(fx.files.saves) != 1 {
		t.Fatalf("it asked to save %d times", len(fx.files.saves))
	}
	if got := fx.files.saves[0].Name; got != "main-sync.sql" {
		t.Errorf("it suggests %q", got)
	}
	if got := fx.files.saves[0].Extensions; !slices.Contains(got, "sql") {
		t.Errorf("it offers %q", got)
	}

	path := filepath.Join(t.TempDir(), "sync.sql")
	fx.files.answer(path, nil)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "orders") {
		t.Errorf("the file holds %q", data)
	}
	if !strings.HasPrefix(string(data), "-- 1 difference chosen") {
		t.Errorf("it does not say what it is: %q", data)
	}
	if !strings.Contains(tb.footer.Text, "Saved to sync.sql") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	if n := len(ranStatements()); n != 0 {
		t.Errorf("%d statements ran; saving runs nothing", n)
	}

	// Cancelling writes nothing and says nothing: it is an answer, not a
	// failure.
	tb.footer.SetText("nothing said yet")
	p.saveScript()
	fx.files.answer("", nil)
	if got := tb.footer.Text; got != "nothing said yet" {
		t.Errorf("cancelling said %q", got)
	}
	if got := fx.s.errors.text; got != "" {
		t.Errorf("cancelling reported a failure: %q", got)
	}
}

// A name a filesystem will not take does not become one.
func TestWhatASavedScriptIsCalled(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"main", "main-sync.sql"},
		{"a/b", "a-b-sync.sql"},
		{`C:\thing`, "C--thing-sync.sql"},
	} {
		if got := scriptFileName(c.in); got != c.want {
			t.Errorf("%q became %q, want %q", c.in, got, c.want)
		}
	}
}

// Nothing is offered until something is chosen, and all three come back
// together.
func TestTheThreeThingsToDoWithAScript(t *testing.T) {
	_, _, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables = append(db.Schemas[0].Tables,
			model.Table{Name: "orders", RowsEstimate: -1})
	})
	for _, b := range p.buttons() {
		if !b.Disabled() {
			t.Errorf("%q is offered with nothing chosen", b.Text)
		}
	}
	p.choose(p.findID(t, "orders"), true)
	for _, b := range p.buttons() {
		if b.Disabled() {
			t.Errorf("%q is not offered with something chosen", b.Text)
		}
	}
	// And all three go again when the choice is taken back.
	p.choose(p.findID(t, "orders"), false)
	for _, b := range p.buttons() {
		if !b.Disabled() {
			t.Errorf("%q is still offered with nothing chosen", b.Text)
		}
	}
}

// A change that runs part-way has still moved the database, so both sides
// are read again rather than the screen being left showing a comparison of
// something that is no longer there.
func TestAChangeThatRunsPartWayIsStillReadAgain(t *testing.T) {
	forgetStatements()
	fx, tb, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables = append(db.Schemas[0].Tables,
			model.Table{Name: "orders", RowsEstimate: -1},
			model.Table{Name: "refused", RowsEstimate: -1})
	})
	t.Cleanup(func() { willNotRun("") })
	willNotRun("refused")

	p.choose(p.findID(t, "orders"), true)
	p.choose(p.findID(t, "refused"), true)
	p.applyScript()
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	tapOnTop(t, fx, "Run")

	pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, "failed") })
	if !strings.Contains(tb.footer.Text, "1 statement ran") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	// One of the two landed, so the comparison on screen is of a database
	// that has moved: it is made again.
	pump(t, fx.q, func() bool { return tb.compare != nil && tb.compare != p })
}

// Nothing running means nothing to read again: a connection that refused
// before the first statement leaves the comparison exactly as it was.
func TestAChangeThatRanNothingLeavesTheComparisonAlone(t *testing.T) {
	forgetStatements()
	fx, tb, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables = append(db.Schemas[0].Tables,
			model.Table{Name: "refused", RowsEstimate: -1})
	})
	t.Cleanup(func() { willNotRun("") })
	willNotRun("refused")

	p.choose(p.findID(t, "refused"), true)
	p.applyScript()
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	tapOnTop(t, fx, "Run")
	pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, "failed") })

	if !strings.Contains(tb.footer.Text, "0 statements ran") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	// Reading again replaces the body with its own line before it starts,
	// so this is asked of something that has already happened or has not —
	// never of something still on its way.
	if said := labelText(tb.body); strings.Contains(said, "Reading both sides again") {
		t.Errorf("it started reading both sides again although nothing had run: %q", said)
	}
	if tb.compare != p {
		t.Error("it read both sides again although nothing had run")
	}
}

// What a comparison leaves out, said and changed (FR-7.5).

// A rule can hide a dropped column, so what is left out is said whether or
// not anything differs.
func TestWhatIsLeftOutIsAlwaysSaid(t *testing.T) {
	fx, _, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables[0].Comment = "a comment only the model has"
	})
	if !p.root.Differs() {
		t.Fatal("a changed comment compared as no change")
	}
	if strings.Contains(p.summary.Text, "Leaving out") {
		t.Errorf("with no rules it says %q", p.summary.Text)
	}

	// Left out, and the comparison says so even though it now finds nothing.
	p.leaveOut(diff.Options{Comments: true}, false)
	pump(t, fx.q, func() bool {
		return fx.s.open[0].compare != nil && fx.s.open[0].compare != p
	})
	now := fx.s.open[0].compare
	if now.root.Differs() {
		t.Errorf("it still compared as %s", now.root.Status)
	}
	if !strings.Contains(now.summary.Text, "Nothing differs") {
		t.Errorf("it says %q", now.summary.Text)
	}
	if !strings.Contains(now.summary.Text, "Leaving out comments") {
		t.Errorf("it does not say what it left out: %q", now.summary.Text)
	}
}

// Rules kept with the model are read back the next time anybody compares
// against it, which is the whole point of them being an agreement.
func TestRulesKeptWithTheModelAreUsedNextTime(t *testing.T) {
	fx, tb, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables[0].Comment = "a comment only the model has"
	})
	p.leaveOut(diff.Options{Comments: true}, true)
	pump(t, fx.q, func() bool { return tb.compare != nil && tb.compare != p })

	// A comparison made afresh against the same model reads them.
	again := fx.s.OpenComparison(tb.connID, model.NewRef(model.KindDatabase, "main"), p.dir)
	pump(t, fx.q, func() bool { return again.compare != nil })
	if !again.compare.ignoring.Comments {
		t.Errorf("it compared with %+v", again.compare.ignoring)
	}
	if again.compare.root.Differs() {
		t.Error("the rule kept with the model was not applied")
	}
}

// Rules not kept are for this comparison alone, and the model is untouched.
func TestRulesNotKeptAreForThisComparisonAlone(t *testing.T) {
	fx, tb, p := comparing(t, func(db *model.Database) {
		db.Schemas[0].Tables[0].Comment = "a comment only the model has"
	})
	dir := p.dir
	p.leaveOut(diff.Options{Comments: true}, false)
	pump(t, fx.q, func() bool { return tb.compare != nil && tb.compare != p })

	if got, err := schemafile.Ignore(dir); err != nil || got.Any() {
		t.Errorf("the model was changed: %+v, %v", got, err)
	}
	if !tb.compare.ignoring.Comments {
		t.Error("the rule was not applied to this comparison")
	}
}

// A pattern that is not one is refused rather than comparing with it.
func TestARuleThatIsNotOneIsRefused(t *testing.T) {
	fx, tb, p := comparing(t, nil)
	p.leaveOut(diff.Options{Names: []string{"[unclosed"}}, false)
	if fx.s.errors.text == "" {
		t.Error("a pattern that will not parse was accepted")
	}
	if tb.compare != p {
		t.Error("it compared again with a rule it had refused")
	}
}

// A comma-separated list of rules is read as the rules in it, and a trailing
// comma is not a rule that matches everything.
func TestReadingAListOfRules(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"audit", []string{"audit"}},
		{"audit, staging", []string{"audit", "staging"}},
		{" audit ,, staging , ", []string{"audit", "staging"}},
		{"", nil},
		{"  ", nil},
	} {
		if got := splitRules(c.in); !slices.Equal(got, c.want) {
			t.Errorf("%q read as %q, want %q", c.in, got, c.want)
		}
	}
}
