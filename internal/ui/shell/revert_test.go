package shell

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

func TestRevertCellsPutsBackWhatWasRead(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 1, Col: 1}, grid.CellID{Row: 1, Col: 1})
	if fx.s.canRevert() {
		t.Error("with no changes there is nothing to revert")
	}
	r1, _ := tb.model.Row(tb.ctx, 1)
	if err := tb.grid.OnEdit(r1, 1, "renamed"); err != nil {
		t.Fatal(err)
	}
	fx.s.run(cmdInsertRow) // the new row comes first, and id 1 moves to row 2
	if err := tb.grid.OnEditAdded(0, 1, "new"); err != nil {
		t.Fatal(err)
	}
	if err := tb.grid.OnEditAdded(0, 0, int64(9)); err != nil {
		t.Fatal(err)
	}
	tb.grid.Select(grid.CellID{Row: 0, Col: 1}, grid.CellID{Row: 2, Col: 1})
	tb.grid.ToggleCell(grid.CellID{Row: 4, Col: 0}) // column 0 of the new row is not selected
	if !fx.s.canRevert() {
		t.Fatal("changed cells selected can be reverted")
	}
	fx.s.run(cmdRevertCells)
	if _, ok := tb.ed.pending.Value(r1, 1); ok {
		t.Error("a row read's cell shows what was read again")
	}
	added := tb.ed.pending.Added()
	if len(added) != 1 || added[0][1] != (model.Default{}) || added[0][0] != int64(9) {
		t.Errorf("a new row's column selected goes back to not given, and it stays: %v", added)
	}
	if tb.ed.pending.Len() != 1 || !strings.HasSuffix(tb.footer.Text, " · 1 pending change") {
		t.Errorf("a row with no change left is unchanged: %d, %q", tb.ed.pending.Len(), tb.footer.Text)
	}
	if row, _ := tb.model.Row(tb.ctx, 0); row[1] != (model.Default{}) {
		t.Error("the grid shows the new row as it is now")
	}
}

func TestRevertRowsUndoesDeletionsAndTakesOutNewRows(t *testing.T) {
	fx, tb := loadedItems(t)
	r1, _ := tb.model.Row(tb.ctx, 1)
	r2, _ := tb.model.Row(tb.ctx, 2)
	if err := tb.grid.OnEdit(r1, 1, "renamed"); err != nil {
		t.Fatal(err)
	}
	tb.ed.pending.Delete(r2)
	if fx.s.canRevert() {
		t.Error("with changes but nothing selected there is nothing to revert")
	}
	for range 3 {
		fx.s.run(cmdInsertRow)
	}
	if err := tb.grid.OnEditAdded(0, 1, "kept"); err != nil {
		t.Fatal(err)
	}
	tb.grid.Select(grid.CellID{Row: 1, Col: 0}, grid.CellID{Row: 5, Col: 0}) // two new rows, and ids 0, 1 and 2
	fx.s.run(cmdRevertRows)
	if tb.ed.pending.State(r1) != model.RowUnchanged || tb.ed.pending.State(r2) != model.RowUnchanged {
		t.Error("rows read are as they were read, a deletion undone")
	}
	if added := tb.ed.pending.Added(); len(added) != 1 || added[0][1] != "kept" || tb.model.Added() != 1 {
		t.Errorf("the new rows selected go, the other stays: %v", added)
	}
	if tb.ed.pending.Len() != 1 {
		t.Errorf("%d changes left", tb.ed.pending.Len())
	}
}

func TestDiscardAllAsksThenForgetsEveryChange(t *testing.T) {
	fx, tb := loadedItems(t)
	editOne(t, tb)
	fx.s.run(cmdInsertRow)
	fx.s.run(cmdDiscardAll)
	if text := labelText(fx.s.win.Canvas().Overlays().Top()); !strings.Contains(text, "2 changes not committed will be lost.") {
		t.Fatalf("it asks first: %q", text)
	}
	tapOnTop(t, fx, "Cancel")
	if tb.ed.pending.Len() != 2 {
		t.Fatal("Cancel keeps them")
	}
	fx.s.run(cmdDiscardAll)
	tapOnTop(t, fx, "Discard")
	if tb.ed.pending.Len() != 0 || tb.model.Added() != 0 || tb.ed.review.Visible() || fx.s.canReview() {
		t.Errorf("every change forgotten: %d, %d new rows", tb.ed.pending.Len(), tb.model.Added())
	}
	if !strings.Contains(tb.footer.Text, "Discarded 2 changes") {
		t.Errorf("footer %q", tb.footer.Text)
	}
}

func TestClosingATabWithChangesAsks(t *testing.T) {
	fx, tb := loadedItems(t)
	editOne(t, tb)
	fx.s.requestClose(tb.item)
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil || !strings.Contains(labelText(top), "“items” has 1 change not committed, which closing discards.") {
		t.Fatalf("closing asks first; overlay %v", top)
	}
	tapOnTop(t, fx, "Cancel")
	if len(fx.s.open) != 1 || tb.ed.pending.Len() != 1 {
		t.Fatal("Cancel keeps the tab and its changes")
	}
	fx.s.requestClose(tb.item)
	tapOnTop(t, fx, "Close")
	if len(fx.s.open) != 0 {
		t.Error("Close closes it")
	}
}

func TestQuittingWithChangesAsks(t *testing.T) {
	fx, tb := loadedItems(t)
	editOne(t, tb)
	fx.s.requestQuit()
	top := fx.s.win.Canvas().Overlays().Top()
	if top == nil || !strings.Contains(labelText(top), "1 change not committed will be lost.") || fx.s.ctx.Err() != nil {
		t.Fatalf("quitting asks first; overlay %v", top)
	}
	tapOnTop(t, fx, "Cancel")
	if fx.s.ctx.Err() != nil {
		t.Fatal("Cancel keeps the app")
	}
	fx.s.requestQuit()
	tapOnTop(t, fx, "Quit")
	if fx.s.ctx.Err() == nil {
		t.Error("Quit quits")
	}
}
