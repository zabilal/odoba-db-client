package shell

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

func copied(t *testing.T, fx *fixture, tb *tab) string {
	t.Helper()
	fx.s.copyCells(tb.ctx, tb.grid)
	// Wait for the copy's own word: selecting cells also rewrites the status
	// line, with the connection's state.
	pump(t, fx.q, func() bool {
		st := fx.s.status.Text
		return strings.HasPrefix(st, "Copied") || strings.HasPrefix(st, "Could not copy") ||
			strings.Contains(st, "Export it instead")
	})
	return fx.s.app.Clipboard().Content()
}

func TestCopyingABlockPutsTabSeparatedRowsOnTheClipboard(t *testing.T) {
	fx, tb := openItems(t)
	tb.grid.Select(grid.CellID{Row: 1, Col: 0}, grid.CellID{Row: 2, Col: 1})
	if got := copied(t, fx, tb); got != "1\titem 1\n2\titem 2" {
		t.Errorf("clipboard %q", got)
	}
	if got := fx.s.status.Text; got != "Copied 2 rows × 2 columns" {
		t.Errorf("status %q", got)
	}
}

func TestCopyingOneCellCopiesItsTextAlone(t *testing.T) {
	fx, tb := openItems(t)
	tb.grid.Select(grid.CellID{Row: 3, Col: 1}, grid.CellID{Row: 3, Col: 1})
	if got := copied(t, fx, tb); got != "item 3" {
		t.Errorf("clipboard %q", got)
	}
}

func TestCellsOutsideTheSelectionCopyEmpty(t *testing.T) {
	fx, tb := openItems(t)
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	tb.grid.ToggleCell(grid.CellID{Row: 2, Col: 1})
	if got := copied(t, fx, tb); got != "0\t\n\titem 2" {
		t.Errorf("clipboard %q; rows 0 and 2, each with only its selected cell", got)
	}
}

func TestCopyReachesRowsNeverDrawnAndStopsAtItsLimit(t *testing.T) {
	fx, tb := openItems(t)
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	tb.grid.SelectColumn()
	if got := copied(t, fx, tb); strings.Count(got, "\n") != fakeRows-1 || !strings.HasSuffix(got, "249") {
		t.Errorf("a whole column should copy all %d rows, to 249; got %d lines", fakeRows, strings.Count(got, "\n")+1)
	}
	copyLimit = 10
	t.Cleanup(func() { copyLimit = 100_000 })
	fx.s.app.Clipboard().SetContent("")
	tb.grid.SelectAll()
	if got := copied(t, fx, tb); got != "" {
		t.Errorf("more rows than Copy takes, yet the clipboard holds %d bytes", len(got))
	}
	if !strings.Contains(fx.s.status.Text, "Export it instead") {
		t.Errorf("status %q", fx.s.status.Text)
	}
}

func TestTheSelectionCommandsFollowTheGrid(t *testing.T) {
	fx, tb := openItems(t)
	c, ok := fx.s.reg.Get(cmdCopyCells)
	if !ok || c.Enabled() {
		t.Fatal("with nothing selected there is nothing to copy")
	}
	tb.grid.Select(grid.CellID{Row: 4, Col: 1}, grid.CellID{Row: 4, Col: 1})
	if !c.Enabled() {
		t.Error("a selection can be copied")
	}
	row, _ := fx.s.reg.Get(cmdSelectRow)
	row.Run()
	if s := tb.grid.Selection(); !s.Contains(4, 0) || s.Contains(3, 0) {
		t.Error("Select Row should select the active cell's row")
	}
	if tb.grid.OnCopy == nil {
		t.Error("⌘C on the grid should copy")
	}
}

func copiedAs(t *testing.T, fx *fixture, tb *tab, f export.Format) string {
	t.Helper()
	fx.s.copyAs(tb.ctx, tb.grid, f)
	pump(t, fx.q, func() bool {
		st := fx.s.status.Text
		return strings.HasPrefix(st, "Copied") || strings.HasPrefix(st, "Could not copy")
	})
	return fx.s.app.Clipboard().Content()
}

func TestCopyAsWritesWholeRowsWithTheirHeader(t *testing.T) {
	fx, tb := openItems(t)
	tb.grid.Select(grid.CellID{Row: 1, Col: 0}, grid.CellID{Row: 2, Col: 1})
	if got := copiedAs(t, fx, tb, export.CSV); got != "id,name\n1,item 1\n2,item 2" {
		t.Errorf("CSV %q", got)
	}
	if got := copiedAs(t, fx, tb, export.JSON); got != "[\n{\"id\":1,\"name\":\"item 1\"},\n{\"id\":2,\"name\":\"item 2\"}\n]" {
		t.Errorf("JSON %q", got)
	}
	if got := copiedAs(t, fx, tb, export.Markdown); got != "| id | name |\n| ---: | --- |\n| 1 | item 1 |\n| 2 | item 2 |" {
		t.Errorf("Markdown %q", got)
	}
	if got := fx.s.status.Text; got != "Copied 2 rows × 2 columns as Markdown" {
		t.Errorf("status %q", got)
	}
}

func TestCopyAsFillsTheCellsCopyLeavesBlank(t *testing.T) {
	fx, tb := openItems(t)
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	tb.grid.ToggleCell(grid.CellID{Row: 2, Col: 1})
	if got := copiedAs(t, fx, tb, export.CSV); got != "id,name\n0,item 0\n2,item 2" {
		t.Errorf("CSV %q; rows are written whole", got)
	}
}
