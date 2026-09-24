package view

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// Marks (FR-2.8): the nodes picked out for something to be done to all of
// them, which the tree cannot hold because it selects one row at a time.

// tree is an explorer over one connection, loaded to its tables.
func tree(t *testing.T) (*Explorer, []string) {
	t.Helper()
	newApp(t)
	l, _ := setup(t, "primary")
	e := New(l, (&uithread.Queue{}).Run, 0)
	e.Model.Children(explorer.RootID)
	root := waitReal(t, e.Model, explorer.RootID)
	dbs := waitReal(t, e.Model, root[0])
	schemas := waitReal(t, e.Model, dbs[0])
	return e, waitReal(t, e.Model, schemas[1]) // the tables of schema "a"
}

// A mark goes on and comes off, and they come back in the order they were
// made: a script for a batch reads in the order somebody built it.
func TestMarksGoOnAndComeOff(t *testing.T) {
	e, tables := tree(t)
	if e.Marked(tables[0]) {
		t.Error("a node is marked before anything marked it")
	}
	if !e.ToggleMark(tables[0]) {
		t.Error("marking a node did not mark it")
	}
	if !e.Marked(tables[0]) || len(e.Marks()) != 1 {
		t.Errorf("the marks are %v", e.Marks())
	}
	if e.ToggleMark(tables[0]) {
		t.Error("marking a marked node did not unmark it")
	}
	if len(e.Marks()) != 0 {
		t.Errorf("the marks are %v", e.Marks())
	}
}

// Clearing takes them all off, and saying so is worth doing once.
func TestClearingTakesEveryMarkOff(t *testing.T) {
	e, tables := tree(t)
	var changed int
	e.OnMark = func() { changed++ }
	e.ToggleMark(tables[0])
	e.ClearMarks()
	if len(e.Marks()) != 0 {
		t.Errorf("the marks are %v", e.Marks())
	}
	if changed != 2 {
		t.Errorf("the marks changed %d times, want 2", changed)
	}
	e.ClearMarks() // nothing to clear, and nothing to say about it
	if changed != 2 {
		t.Errorf("clearing nothing said the marks changed: %d", changed)
	}
}

// A marked row is drawn with a tick in front of it, and in bold: a colour
// alone is not a sign somebody can see or have read out.
func TestAMarkedRowIsDrawnAsOne(t *testing.T) {
	e, tables := tree(t)
	r := newNodeRow()
	e.update(tables[0], r)
	plain := r.label.Text
	if r.marked || r.label.TextStyle.Bold {
		t.Errorf("an unmarked row is drawn as marked: %q", r.label.Text)
	}
	e.ToggleMark(tables[0])
	e.update(tables[0], r)
	if r.label.Text != markGlyph+plain || !r.label.TextStyle.Bold {
		t.Errorf("a marked row reads %q (bold %v)", r.label.Text, r.label.TextStyle.Bold)
	}
	// The rows are reused, so an unmarked one drawn into the same row must
	// not keep the tick.
	e.update(e.Model.Children(explorer.RootID)[0], r)
	if r.marked || r.label.TextStyle.Bold {
		t.Errorf("the next row kept the mark: %q", r.label.Text)
	}
}

// A loading or failed row cannot be marked: there is no object there.
func TestAPlaceholderCannotBeMarked(t *testing.T) {
	e, tables := tree(t)
	// A branch nobody has opened lists a loading row, which is what a
	// placeholder is.
	loading := e.Model.Children(tables[0])
	if len(loading) != 1 || !explorer.IsPlaceholder(loading[0]) {
		t.Fatalf("expected a loading row, got %v", loading)
	}
	if e.ToggleMark(loading[0]) {
		t.Error("a loading row was marked")
	}
	if e.ToggleMark("") {
		t.Error("nothing at all was marked")
	}
	if len(e.Marks()) != 0 {
		t.Errorf("the marks are %v", e.Marks())
	}
}

// The marked objects come back with the connection they are on, and a
// mark on something that is not an object is left out of them.
func TestTheMarkedObjectsComeBackWithTheirConnection(t *testing.T) {
	e, tables := tree(t)
	root := e.Model.Children(explorer.RootID)
	e.ToggleMark(tables[0])
	e.ToggleMark(root[0]) // the connection itself, which is no object
	got := e.MarkedNodes()
	if len(got) != 1 {
		t.Fatalf("the marked objects are %+v", got)
	}
	if got[0].ID != tables[0] || got[0].ConnID == "" || got[0].Node.Ref.Kind != model.KindTable {
		t.Errorf("the marked object is %+v", got[0])
	}
	// And both marks are still there: what is marked and what a batch can
	// be done to are different questions.
	if len(e.Marks()) != 2 {
		t.Errorf("the marks are %v", e.Marks())
	}
}
