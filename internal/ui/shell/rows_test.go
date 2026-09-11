package shell

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

func TestInsertRowAddsANewRowAtTheTop(t *testing.T) {
	fx, tb := loadedItems(t)
	if !fx.s.canInsert() || fx.s.canChangeRows() {
		t.Fatal("a keyed table takes new rows; with nothing selected, nothing else")
	}
	fx.s.run(cmdInsertRow)
	if tb.ed.pending.Len() != 1 || tb.model.Added() != 1 {
		t.Fatalf("%d changes, %d new rows", tb.ed.pending.Len(), tb.model.Added())
	}
	if row, _ := tb.model.Row(tb.ctx, 0); row[0] != (model.Default{}) {
		t.Errorf("the new row is first, given nothing: %v", row)
	}
	if a, ok := tb.grid.Selection().Active(); !ok || a != (grid.CellID{Row: 0, Col: 0}) {
		t.Errorf("its first cell is selected, to type into: %v %v", a, ok)
	}
	if !strings.HasPrefix(tb.footer.Text, "250 rows") || !strings.HasSuffix(tb.footer.Text, " · 1 pending change") {
		t.Errorf("the rows are counted apart from the new one: %q", tb.footer.Text)
	}
	if err := tb.grid.OnEditAdded(0, 1, "new"); err != nil {
		t.Fatal(err)
	}
	if row, _ := tb.model.Row(tb.ctx, 0); row[1] != "new" {
		t.Errorf("the grid shows what the new row was given: %v", row)
	}
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 1})
	fx.s.run(cmdSetNull)
	pump(t, fx.q, func() bool { return !strings.Contains(tb.footer.Text, "Setting…") })
	if got := tb.ed.pending.Added()[0]; got[0] != (model.Default{}) || got[1] != nil {
		t.Errorf("a new row's cell set to NULL; its id cannot be: %v", got)
	}
}

func TestDuplicateRowsCopiesAllButTheKey(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 2, Col: 0}, grid.CellID{Row: 3, Col: 1})
	tb.grid.ToggleCell(grid.CellID{Row: 5, Col: 0})
	fx.s.run(cmdDuplicateRows)
	added := tb.ed.pending.Added()
	if len(added) != 3 || added[0][1] != "item 2" || added[1][1] != "item 3" || added[2][1] != "item 5" || added[0][0] != (model.Default{}) {
		t.Fatalf("a copy of each row selected, but for its key: %v", added)
	}
	if tb.model.Added() != 3 {
		t.Error("the copies are shown")
	}
	if a, _ := tb.grid.Selection().Active(); a != (grid.CellID{Row: 0, Col: 0}) {
		t.Errorf("the first copy is selected: %v", a)
	}
}

func TestDeleteRowsMarksRowsReadAndDropsNewOnes(t *testing.T) {
	fx, tb := loadedItems(t)
	for range 3 {
		fx.s.run(cmdInsertRow)
	}
	if err := tb.grid.OnEditAdded(0, 1, "kept"); err != nil {
		t.Fatal(err)
	}
	tb.grid.Select(grid.CellID{Row: 1, Col: 0}, grid.CellID{Row: 4, Col: 0}) // two new rows, and ids 0 and 1
	fx.s.run(cmdDeleteRows)
	if added := tb.ed.pending.Added(); tb.model.Added() != 1 || len(added) != 1 || added[0][1] != "kept" {
		t.Errorf("the new rows selected are gone, the other kept: %v", added)
	}
	for _, id := range []int64{0, 1} {
		if tb.ed.pending.State(model.Row{id, fmt.Sprintf("item %d", id)}) != model.RowDeleted {
			t.Errorf("row %d is to be deleted", id)
		}
	}
	if row, _ := tb.model.Row(tb.ctx, 3); tb.ed.pending.State(row) != model.RowUnchanged { // id 2
		t.Error("the next row is not")
	}
	if !strings.HasSuffix(tb.footer.Text, " · 3 pending changes") {
		t.Errorf("footer %q", tb.footer.Text)
	}
}
