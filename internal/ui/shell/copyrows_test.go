package shell

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Copying a table's rows into another table (FR-10.9).

// The copy is offered on rows, and not on what has none.
func TestWhatIsOfferedACopy(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	if !fx.s.canCopyRows() {
		t.Error("a table's rows were not offered a copy")
	}
	if fx.s.menuItems[cmdCopyRows].Disabled {
		t.Error("the menu item is disabled for a table")
	}
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	if fx.s.canCopyRows() {
		t.Error("a database, which has no rows of its own, was offered a copy")
	}
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, itemsNode.Ref))
	fx.s.sync()
	if err := fx.ws.Disconnect(c.ID); err != nil {
		t.Fatal(err)
	}
	fx.s.sync()
	if fx.s.canCopyRows() {
		t.Error("a connection that is not open was offered a copy")
	}
}

// copyFormOn re-selects the items table of a connection and opens the copy
// form over it. Loading more of the tree can take the selection away, so
// it is made again here rather than assumed.
func copyFormOn(t *testing.T, fx *fixture, c store.SavedConnection) *copyForm {
	t.Helper()
	// The tree down to the table again: refreshing the root for another
	// connection drops what was loaded under this one, and a node that is
	// not there cannot be selected.
	loaded(t, fx, view.ConnectionID(c.ID))
	loaded(t, fx, view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, itemsNode.Ref))
	fx.s.sync()
	f := fx.s.copyRowsFrom()
	if f == nil {
		t.Fatal("the copy form did not open")
	}
	return f
}

// wideItems selects the items table on a connection that also holds the
// memos table, whose columns are not the same.
func wideItems(t *testing.T, fx *fixture) store.SavedConnection {
	t.Helper()
	c := fx.create(t, "wide", nil)
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	loaded(t, fx, view.ConnectionID(c.ID))
	loaded(t, fx, view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, itemsNode.Ref))
	fx.s.sync()
	return c
}

// What the destination has nowhere to put is said before anything is
// copied, and a destination with nothing in common says that.
func TestTheFormSaysWhatIsLeftBehind(t *testing.T) {
	fx := newFixture(t)
	wideItems(t, fx)
	f := copyFormOn(t, fx, fx.conns.List()[0])
	pump(t, fx.q, func() bool { return len(f.tables.Options) == 3 })
	f.tables.SetSelectedIndex(1) // memos
	pump(t, fx.q, func() bool { return f.to != nil })
	fx.q.Flush()
	if got := f.note.Text; !strings.Contains(got, "1 column matched") || !strings.Contains(got, "Left behind: name") {
		t.Errorf("the form says %q", got)
	}
}

// A value copied into a column of another type is made that type, because
// the destination's columns are what the rows are made into.
func TestACopiedValueIsMadeTheDestinationsType(t *testing.T) {
	fx := newFixture(t)
	wideItems(t, fx)
	f := copyFormOn(t, fx, fx.conns.List()[0])
	pump(t, fx.q, func() bool { return len(f.tables.Options) == 3 })
	f.tables.SetSelectedIndex(1) // memos, whose id is text
	pump(t, fx.q, func() bool { return f.to != nil && f.from != nil })

	before := len(loadsSoFar())
	fx.q.Run(f.start)
	pump(t, fx.q, func() bool { return len(loadsSoFar()) > before })
	got := loadsSoFar()[len(loadsSoFar())-1]
	if len(got.columns) != 1 || got.columns[0] != "id" {
		t.Fatalf("it wrote the columns %v", got.columns)
	}
	if len(got.rows) == 0 {
		t.Fatal("it wrote no rows")
	}
	if _, ok := got.rows[0][0].(string); !ok {
		t.Errorf("the id arrived as %T, and the column it went into holds text", got.rows[0][0])
	}
}

// The form offers the open connections, lists the tables of the one
// chosen, and says what would be copied and what left behind.
func TestTheCopyFormSaysWhatWouldBeCopied(t *testing.T) {
	fx := newFixture(t)
	f := copyFormOn(t, fx, selectItems(t, fx))
	if len(f.connIDs) != 1 || f.conns.Selected != "db1" {
		t.Errorf("it offers the connections %v, selected %q", f.connIDs, f.conns.Selected)
	}
	pump(t, fx.q, func() bool { return len(f.tables.Options) > 0 })
	if got := f.tables.Options; len(got) != 1 || !strings.Contains(got[0], "items") {
		t.Errorf("it lists the tables %v", got)
	}
	f.tables.SetSelectedIndex(0)
	pump(t, fx.q, func() bool { return f.to != nil })
	fx.q.Flush()
	// Into itself, so every column matches and nothing is left behind.
	if got := f.note.Text; !strings.Contains(got, "2 columns matched") || strings.Contains(got, "Left behind") {
		t.Errorf("the form says %q", got)
	}
}

// Copying writes the rows, says how many, and reads the destination again
// where it is open.
func TestCopyingWritesTheRows(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	f := copyFormOn(t, fx, c)
	pump(t, fx.q, func() bool { return len(f.tables.Options) > 0 })
	f.tables.SetSelectedIndex(0)
	pump(t, fx.q, func() bool { return f.to != nil && f.from != nil })

	before := len(loadsSoFar())
	fx.q.Run(f.start)
	pump(t, fx.q, func() bool { return strings.Contains(fx.s.status.Text, "Copied") })
	loads := loadsSoFar()
	if len(loads) != before+1 {
		t.Fatalf("it wrote %d loads", len(loads)-before)
	}
	got := loads[len(loads)-1]
	if len(got.rows) == 0 {
		t.Error("it wrote no rows")
	}
	// The destination's columns, by name, and its own order.
	if len(got.columns) != 2 || got.columns[0] != "id" || got.columns[1] != "name" {
		t.Errorf("it wrote the columns %v", got.columns)
	}
	if got.opt.Truncate {
		t.Error("it replaced the rows without being asked to")
	}
	if got.target.Name() != "items" {
		t.Errorf("it wrote into %q", got.target.Name())
	}
	if want := "Copied " + group(int64(len(got.rows))) + " rows into items"; !strings.Contains(fx.s.status.Text, want) {
		t.Errorf("the window says %q, want %q in it", fx.s.status.Text, want)
	}
}

// Replacing every row asks first, and nothing is written until it is
// answered.
func TestReplacingEveryRowAsksFirst(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	f := copyFormOn(t, fx, c)
	pump(t, fx.q, func() bool { return len(f.tables.Options) > 0 })
	f.tables.SetSelectedIndex(0)
	pump(t, fx.q, func() bool { return f.to != nil && f.from != nil })
	f.replace.SetChecked(true)

	fx.q.Run(f.start)
	fx.q.Flush()
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("replacing every row did not ask")
	}
	if b := findButton(top, "Replace"); b == nil {
		t.Errorf("the question's buttons are not what it does: %v", labelsIn(top))
	}
	if strings.Contains(fx.s.status.Text, "Copied") {
		t.Error("it wrote the rows before the question was answered")
	}
	before := len(loadsSoFar())
	test.Tap(findButton(top, "Cancel"))
	settle(fx)
	if len(loadsSoFar()) != before {
		t.Error("it wrote the rows after the question was declined")
	}
}

// A destination sharing no column is said to be one, rather than copied
// into and found empty.
func TestTheFormSaysWhenNothingMatches(t *testing.T) {
	fx := newFixture(t)
	wideItems(t, fx)
	f := copyFormOn(t, fx, fx.conns.List()[0])
	pump(t, fx.q, func() bool { return len(f.tables.Options) == 3 })
	f.tables.SetSelectedIndex(2) // alone, whose one column is nobody else's
	pump(t, fx.q, func() bool { return f.to != nil })
	fx.q.Flush()
	if got := f.note.Text; !strings.Contains(got, "nothing to copy") {
		t.Errorf("the form says %q", got)
	}
	// And with nowhere chosen at all it says to choose.
	f.to = nil
	f.describe()
	if got := f.note.Text; !strings.Contains(got, "Choose where") {
		t.Errorf("with nowhere chosen the form says %q", got)
	}
}

// A list of tables that arrives after a newer one is dropped: it is the
// last question's answer, and the form is showing this one's.
func TestALateListOfTablesIsDropped(t *testing.T) {
	fx := newFixture(t)
	wideItems(t, fx)
	slow, err := fx.conns.Create(store.SavedConnection{Name: "slow", Driver: "postgres", Host: "slowlist"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	loaded(t, fx, view.ConnectionID(slow.ID))
	f := copyFormOn(t, fx, fx.conns.List()[0])
	pump(t, fx.q, func() bool { return len(f.connIDs) == 2 })

	// The slow one first, then the wide one before its answer arrives.
	f.conns.SetSelectedIndex(indexOf(f.connIDs, slow.ID))
	f.conns.SetSelectedIndex(indexOf(f.connIDs, fx.conns.List()[0].ID))
	pump(t, fx.q, func() bool { return len(f.tables.Options) == 3 })
	settle(fx)
	if got := len(f.tables.Options); got != 3 {
		t.Errorf("the form lists %d tables; the slow answer took the list over", got)
	}
}

// A form with nowhere chosen to copy into says so rather than copying.
func TestACopyNeedsADestination(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	f := copyFormOn(t, fx, c)
	pump(t, fx.q, func() bool { return f.from != nil })
	fx.q.Run(f.start)
	fx.q.Flush()
	if strings.Contains(fx.s.status.Text, "Copied") {
		t.Error("it copied with nowhere to copy into")
	}
}

// The form opens on rows and on nothing else.
func TestTheFormOpensOnRows(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	if f := fx.s.copyRowsFrom(); f != nil {
		t.Error("the form opened on a database, which has no rows of its own")
	}
}

// Copying into a connection marked Production asks first.
func TestCopyingIntoProductionAsksFirst(t *testing.T) {
	fx := newFixture(t)
	selectItems(t, fx)
	prod, err := fx.conns.Create(store.SavedConnection{Name: "prod", Driver: "postgres",
		Host: "db2", Environment: "production"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	loaded(t, fx, view.ConnectionID(prod.ID))
	f := copyFormOn(t, fx, fx.conns.List()[0])
	pump(t, fx.q, func() bool { return len(f.connIDs) == 2 })
	f.conns.SetSelectedIndex(indexOf(f.connIDs, prod.ID))
	pump(t, fx.q, func() bool { return len(f.tables.Options) > 0 })
	f.tables.SetSelectedIndex(0)
	pump(t, fx.q, func() bool { return f.to != nil && f.from != nil })

	before := len(loadsSoFar())
	fx.q.Run(f.start)
	fx.q.Flush()
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil || findButton(top, "Copy") == nil {
		t.Fatalf("copying into production did not ask: %v", labelsIn(top))
	}
	settle(fx)
	if len(loadsSoFar()) != before {
		t.Error("it wrote the rows before the question was answered")
	}
	test.Tap(findButton(top, "Copy"))
	pump(t, fx.q, func() bool { return len(loadsSoFar()) > before })
	if got := loadsSoFar()[len(loadsSoFar())-1]; !got.opt.Confirmed {
		t.Error("the loader was not told the write was confirmed")
	}
}

// settle runs whatever is queued, and whatever that queues, for long
// enough that work started off the UI goroutine would have landed.
func settle(fx *fixture) {
	for i := 0; i < 50; i++ {
		fx.q.Flush()
		time.Sleep(2 * time.Millisecond)
	}
}

// The tables of another connection are listed when it is chosen.
func TestChoosingAnotherConnectionListsItsTables(t *testing.T) {
	fx := newFixture(t)
	selectItems(t, fx)
	other := fx.create(t, "db2", nil)
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	loaded(t, fx, view.ConnectionID(other.ID)) // opens it
	f := copyFormOn(t, fx, fx.conns.List()[0])
	pump(t, fx.q, func() bool { return len(f.connIDs) == 2 })
	i := indexOf(f.connIDs, other.ID)
	if i < 0 {
		t.Fatalf("the other connection is not offered: %v", f.connIDs)
	}
	f.conns.SetSelectedIndex(i)
	pump(t, fx.q, func() bool { return f.toConn == other.ID && len(f.tables.Options) > 0 })
	if got := f.tables.Options; len(got) != 1 || !strings.Contains(got[0], "items") {
		t.Errorf("it lists %v", got)
	}
}
