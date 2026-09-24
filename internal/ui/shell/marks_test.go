package shell

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/source"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// Batch operations on marked objects (FR-2.8).

// markItems marks the items table and returns its connection.
func markItems(t *testing.T, fx *fixture) string {
	t.Helper()
	c := selectItems(t, fx)
	fx.s.run(cmdMark)
	if len(fx.s.Explorer.Marks()) != 1 {
		t.Fatalf("the marks are %v", fx.s.Explorer.Marks())
	}
	return c.ID
}

// Marking is a thing somebody does on purpose, and the window says how
// many are marked.
func TestMarkingAndClearing(t *testing.T) {
	fx := newFixture(t)
	if !fx.s.menuItems[cmdMark].Disabled {
		t.Error("with nothing selected there is nothing to mark")
	}
	if !fx.s.menuItems[cmdMarksClear].Disabled {
		t.Error("with nothing marked there is nothing to clear")
	}
	markItems(t, fx)
	fx.s.sync()
	// What is marked, and still which connection the selection is on: a
	// batch being built does not stop the window saying where it is.
	if got := fx.s.status.Text; !strings.Contains(got, "1 object marked") || !strings.Contains(got, "db1") {
		t.Errorf("the window says %q", got)
	}
	if fx.s.menuItems[cmdMarksClear].Disabled {
		t.Error("with something marked, clearing is offered")
	}
	fx.s.run(cmdMark) // again: it comes off
	if len(fx.s.Explorer.Marks()) != 0 {
		t.Errorf("the marks are %v", fx.s.Explorer.Marks())
	}
	fx.s.run(cmdMark)
	fx.s.run(cmdMarksClear)
	if len(fx.s.Explorer.Marks()) != 0 {
		t.Errorf("clearing left %v", fx.s.Explorer.Marks())
	}
}

// A script for the batch names every marked object, says it has not run,
// and opens as unsaved text in a query tab.
func TestScriptingTheMarkedObjects(t *testing.T) {
	fx := newFixture(t)
	markItems(t, fx)
	fx.s.sync()
	if fx.s.menuItems[cmdMarksSelect].Disabled {
		t.Fatal("a marked object is not offered a script")
	}
	fx.s.run(cmdMarksSelect)
	pump(t, fx.q, func() bool { return len(fx.s.open) == 1 && fx.s.open[0].query != nil })
	text := fx.s.open[0].query.editor.Document().Text()
	for _, want := range []string{"SELECT", "1 marked object", "has run"} {
		if !strings.Contains(text, want) {
			t.Errorf("the script does not mention %q:\n%s", want, text)
		}
	}
	if !fx.s.open[0].query.dirty {
		t.Error("the script should be unsaved text, kept like anything typed there")
	}
}

// The DROP script is a script: it is written, not run.
func TestScriptingTheMarkedObjectsAsDrop(t *testing.T) {
	fx := newFixture(t)
	markItems(t, fx)
	fx.s.sync()
	fx.s.run(cmdMarksDrop)
	pump(t, fx.q, func() bool { return len(fx.s.open) == 1 && fx.s.open[0].query != nil })
	text := fx.s.open[0].query.editor.Document().Text()
	if !strings.Contains(text, "DROP") || !strings.Contains(text, "items") {
		t.Errorf("the DROP script reads:\n%s", text)
	}
	if !strings.Contains(text, "Nothing here has run") {
		t.Errorf("the script does not say it has not run:\n%s", text)
	}
}

// An object the connection cannot write that statement for is named in
// the script rather than left out of it: a script quietly short of what
// was marked is the one somebody runs thinking it is all of it.
func TestWhatCannotBeScriptedIsSaidInTheScript(t *testing.T) {
	fx := newFixture(t)
	markItems(t, fx)
	fx.s.sync()
	fx.q.Run(func() {
		fx.s.scriptMarked("SELECT", func(context.Context, source.Source, view.MarkedNode) (string, error) {
			return "", errors.New("no SELECT for that")
		})
	})
	pump(t, fx.q, func() bool { return len(fx.s.open) == 1 && fx.s.open[0].query != nil })
	want := "-- SELECT for 1 marked object.\n" +
		"-- Nothing here has run. This is a script to read, keep or edit.\n\n" +
		"-- items: no SELECT for that\n"
	if got := fx.s.open[0].query.editor.Document().Text(); got != want {
		t.Errorf("the script reads:\n%s\nwant:\n%s", got, want)
	}
}

// An object that writes nothing at all is named too, for the same reason.
func TestWhatWritesNothingIsSaidInTheScript(t *testing.T) {
	fx := newFixture(t)
	markItems(t, fx)
	fx.s.sync()
	fx.q.Run(func() {
		fx.s.scriptMarked("CREATE", func(context.Context, source.Source, view.MarkedNode) (string, error) {
			return "  \n ", nil
		})
	})
	pump(t, fx.q, func() bool { return len(fx.s.open) == 1 && fx.s.open[0].query != nil })
	if got := fx.s.open[0].query.editor.Document().Text(); !strings.HasSuffix(got, "-- items: nothing to write\n") {
		t.Errorf("the script reads:\n%s", got)
	}
}

// Two objects are two statements, one blank line apart, with no more of
// the blank lines each of them ended with.
func TestABatchScriptIsOneStatementPerObject(t *testing.T) {
	fx := newFixture(t)
	markItems(t, fx)
	c := fx.conns.List()[0]
	fx.s.Explorer.ToggleMark(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	fx.q.Run(func() {
		fx.s.scriptMarked("SELECT", func(_ context.Context, _ source.Source, n view.MarkedNode) (string, error) {
			return "SELECT * FROM " + n.Node.Label + ";\n\n", nil
		})
	})
	pump(t, fx.q, func() bool { return len(fx.s.open) == 1 && fx.s.open[0].query != nil })
	want := "-- SELECT for 2 marked objects.\n" +
		"-- Nothing here has run. This is a script to read, keep or edit.\n\n" +
		"SELECT * FROM items;\n\nSELECT * FROM main;\n"
	if got := fx.s.open[0].query.editor.Document().Text(); got != want {
		t.Errorf("the script reads:\n%q\nwant:\n%q", got, want)
	}
}

// Only rows can be exported, so an object with none takes the batch's
// export off the menu.
func TestOnlyRowsAreExported(t *testing.T) {
	fx := newFixture(t)
	markItems(t, fx)
	c := fx.conns.List()[0]
	fx.s.Explorer.ToggleMark(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	if fx.s.canExportMarked() {
		t.Error("a database, which has no rows of its own, was offered an export")
	}
	if fx.s.menuItems[cmdMarksSelect].Disabled {
		t.Error("it can still be scripted: that is a different question")
	}
}

// A connection that is not open has nothing to write a script from.
func TestABatchNeedsItsConnectionOpen(t *testing.T) {
	fx := newFixture(t)
	c := markItems(t, fx)
	fx.s.sync()
	if fx.s.menuItems[cmdMarksSelect].Disabled {
		t.Fatal("an open connection is not offered a script")
	}
	if err := fx.ws.Disconnect(c); err != nil {
		t.Fatal(err)
	}
	fx.s.sync()
	if !fx.s.menuItems[cmdMarksSelect].Disabled || !fx.s.menuItems[cmdMarksExport].Disabled {
		t.Error("a connection that is not open was offered a batch")
	}
}

// Only an object is marked. A folder is not one, and neither is the
// connection: marking one would put a thing with no script in the batch.
func TestOnlyAnObjectIsMarked(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	fx.s.Explorer.Tree.Select(view.ConnectionID(c.ID))
	fx.s.sync()
	fx.s.markSelected()
	if len(fx.s.Explorer.Marks()) != 0 {
		t.Errorf("the connection itself was marked: %v", fx.s.Explorer.Marks())
	}
}

// Objects on two connections are two batches, and the window says so by
// offering nothing rather than by doing half of it.
func TestABatchIsOnOneConnection(t *testing.T) {
	fx := newFixture(t)
	markItems(t, fx)
	// A second connection, loaded far enough for its table to be a node in
	// the tree: a mark on a row that is not there is no mark at all.
	other := fx.create(t, "db2", nil)
	fx.s.Explorer.Refresh(explorer.RootID)
	loaded(t, fx, explorer.RootID)
	loaded(t, fx, view.ConnectionID(other.ID))
	loaded(t, fx, view.NodeID(other.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.Explorer.ToggleMark(view.NodeID(other.ID, itemsNode.Ref))
	if len(fx.s.Explorer.MarkedNodes()) != 2 {
		t.Fatalf("the marked objects are %+v", fx.s.Explorer.MarkedNodes())
	}
	fx.s.sync()
	if _, _, ok := fx.s.marked(); ok {
		t.Error("marks on two connections were taken for one batch")
	}
	if !fx.s.menuItems[cmdMarksSelect].Disabled || !fx.s.menuItems[cmdMarksExport].Disabled {
		t.Error("a batch over two connections should be offered neither a script nor an export")
	}
}

// Exporting the batch writes one file per object, named after it, in the
// directory chosen.
func TestExportingTheMarkedObjects(t *testing.T) {
	fx := newFixture(t)
	markItems(t, fx)
	fx.s.sync()
	if fx.s.menuItems[cmdMarksExport].Disabled {
		t.Fatal("a marked table is not offered an export")
	}
	dir := t.TempDir()
	fx.s.run(cmdMarksExport)
	choose := findButton(fx.s.win.Canvas().Overlays().Top(), "Choose Where…")
	if choose == nil {
		t.Fatal("the export form did not open")
	}
	test.Tap(choose)
	if len(fx.files.saves) != 1 {
		t.Fatalf("it asked for %d places to save", len(fx.files.saves))
	}
	if got := fx.files.saves[0].Name; got != "items.csv" {
		t.Errorf("it suggests the file name %q", got)
	}
	fx.files.answer(filepath.Join(dir, "items.csv"), nil)
	pump(t, fx.q, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "items.csv"))
		return err == nil && strings.Contains(string(b), "item 1")
	})
}

// Marking a row draws the tick on it there and then: the mark is the only
// lasting sign of what is in a batch, and it has to appear when it is made.
func TestMarkingDrawsTheTickOnTheRow(t *testing.T) {
	fx := newFixture(t)
	selectItems(t, fx)
	fx.s.win.Resize(fyne.NewSize(900, 600))
	fx.q.Flush()
	before := ticksDrawn(fx)
	fx.s.run(cmdMark)
	fx.q.Flush()
	if got := ticksDrawn(fx); got != before+1 {
		t.Errorf("%d rows are drawn with a tick, and %d were before", got, before)
	}
	fx.s.run(cmdMarksClear)
	fx.q.Flush()
	if got := ticksDrawn(fx); got != before {
		t.Errorf("%d rows still drawn with a tick after clearing", got)
	}
}

// ticksDrawn is how many rows the window draws with a mark on them.
func ticksDrawn(fx *fixture) int {
	n := 0
	for _, text := range drawnSkipping(fx.s.win.Canvas().Content(), nil) {
		if strings.HasPrefix(text, "✓") {
			n++
		}
	}
	return n
}
