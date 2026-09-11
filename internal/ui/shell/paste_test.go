package shell

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// pasteText puts text on the clipboard, pastes it with paste, and waits for
// the paste's own word in the footer.
func pasteText(t *testing.T, fx *fixture, tb *tab, text string, paste func()) {
	t.Helper()
	fx.s.app.Clipboard().SetContent(text)
	paste()
	pump(t, fx.q, func() bool {
		return strings.Contains(strings.ToLower(tb.footer.Text), "paste") && !strings.Contains(tb.footer.Text, "Pasting…")
	})
}

// valueAt is row r's pending value in column col, if it has one.
func valueAt(tb *tab, r, col int) (any, bool) {
	row, _ := tb.model.Row(tb.ctx, int64(r))
	return tb.ed.pending.Value(row, col)
}

func TestABlockIsPastedFromTheActiveCell(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 1, Col: 0}, grid.CellID{Row: 1, Col: 0})
	pasteText(t, fx, tb, "7\tx\nnope\ty\tw\n", tb.grid.OnPaste) // ⌘V on the grid
	if v, _ := valueAt(tb, 1, 0); v != int64(7) {
		t.Errorf("row 1's id: %v", v)
	}
	if v, _ := valueAt(tb, 1, 1); v != "x" {
		t.Errorf("row 1's name: %v", v)
	}
	if v, _ := valueAt(tb, 2, 1); v != "y" {
		t.Errorf("row 2's name: %v", v)
	}
	if _, ok := valueAt(tb, 2, 0); ok {
		t.Error("a value its column cannot hold is not pasted")
	}
	if !strings.Contains(tb.footer.Text, "Pasted 3 of 5 cells; the first not pasted: id: ") {
		t.Errorf("footer %q", tb.footer.Text)
	}
	if sel := tb.grid.Selection(); !sel.Contains(1, 0) || !sel.Contains(2, 1) || sel.Contains(3, 0) || sel.Contains(2, 2) {
		t.Error("the block pasted is selected, as far as the columns reach")
	}
}

func TestCSVIsPastedAndACommaInOneValueIsKept(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 3, Col: 0}, grid.CellID{Row: 3, Col: 0})
	pasteText(t, fx, tb, "10,p\n11,q", func() { fx.s.run(cmdPasteCells) })
	if v, _ := valueAt(tb, 4, 0); v != int64(11) {
		t.Errorf("row 4's id: %v", v)
	}
	if v, _ := valueAt(tb, 4, 1); v != "q" || !strings.Contains(tb.footer.Text, "Pasted 4 cells") {
		t.Errorf("row 4's name: %v; footer %q", v, tb.footer.Text)
	}
	tb.grid.Select(grid.CellID{Row: 5, Col: 1}, grid.CellID{Row: 5, Col: 1})
	pasteText(t, fx, tb, "Smith, John", func() { fx.s.run(cmdPasteCells) })
	if v, _ := valueAt(tb, 5, 1); v != "Smith, John" || !strings.Contains(tb.footer.Text, "Pasted 1 cell") {
		t.Errorf("one line is one value: %v; footer %q", v, tb.footer.Text)
	}
}

func TestAPasteSaysWhatReachedPastTheGrid(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 1, Col: 1}, grid.CellID{Row: 1, Col: 1})
	pasteText(t, fx, tb, "a\tb", func() { fx.s.run(cmdPasteCells) })
	if v, _ := valueAt(tb, 1, 1); v != "a" || !strings.Contains(tb.footer.Text, "Pasted 1 of 2 cells; the first not pasted: past the last column") {
		t.Errorf("%v; footer %q", v, tb.footer.Text)
	}
	tb.grid.Select(grid.CellID{Row: 248, Col: 1}, grid.CellID{Row: 248, Col: 1}) // rows never drawn
	pasteText(t, fx, tb, "a\nb\nc\nd", func() { fx.s.run(cmdPasteCells) })
	if !strings.Contains(tb.footer.Text, "Pasted 2 of 4 cells; the first not pasted: past the last row") {
		t.Errorf("footer %q", tb.footer.Text)
	}
	if v, _ := valueAt(tb, 249, 1); v != "b" || tb.model.Added() != 0 {
		t.Errorf("the last row is pasted into, and no row is added among the rows read: %v", v)
	}
}

func TestAPasteOnANewRowFillsNewRows(t *testing.T) {
	fx, tb := loadedItems(t)
	fx.s.run(cmdInsertRow) // new row 0, its first cell selected
	pasteText(t, fx, tb, "5\tn1\n\tn2\n", func() { fx.s.run(cmdPasteCells) })
	added := tb.ed.pending.Added()
	if len(added) != 2 || added[0][0] != int64(5) || added[0][1] != "n1" || added[1][0] != (model.Default{}) || added[1][1] != "n2" {
		t.Fatalf("a new row is added for the block, an empty cell left not given: %v", added)
	}
	if tb.model.Added() != 2 || tb.ed.pending.Len() != 2 || !strings.Contains(tb.footer.Text, "Pasted 4 cells") {
		t.Errorf("the rows read are not reached: %d changes; footer %q", tb.ed.pending.Len(), tb.footer.Text)
	}
}

func TestOneValuePastedOverASelectionFillsIt(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 1, Col: 1}, grid.CellID{Row: 3, Col: 1})
	tb.grid.ToggleCell(grid.CellID{Row: 5, Col: 1})
	pasteText(t, fx, tb, "z\n", func() { fx.s.run(cmdPasteCells) })
	for _, r := range []int{1, 2, 3, 5} {
		if v, _ := valueAt(tb, r, 1); v != "z" {
			t.Errorf("row %d's name: %v", r, v)
		}
	}
	if _, ok := valueAt(tb, 4, 1); ok || !strings.Contains(tb.footer.Text, "Pasted 4 cells") {
		t.Errorf("only the cells selected; footer %q", tb.footer.Text)
	}
	tb.grid.Select(grid.CellID{Row: 7, Col: 0}, grid.CellID{Row: 7, Col: 1}) // one row, two columns
	pasteText(t, fx, tb, "9", func() { fx.s.run(cmdPasteCells) })
	if id, _ := valueAt(tb, 7, 0); id != int64(9) {
		t.Errorf("each cell of a row selected: id %v", id)
	}
	if name, _ := valueAt(tb, 7, 1); name != "9" {
		t.Errorf("each cell of a row selected: name %v", name)
	}
}

func TestAPasteRefusesTooMuchAndNothing(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 1, Col: 1}, grid.CellID{Row: 1, Col: 1})
	pasteText(t, fx, tb, "", func() { fx.s.run(cmdPasteCells) })
	if !strings.Contains(tb.footer.Text, "Nothing to paste") {
		t.Errorf("footer %q", tb.footer.Text)
	}
	copyLimit = 2
	t.Cleanup(func() { copyLimit = 100_000 })
	pasteText(t, fx, tb, "a\nb\nc", func() { fx.s.run(cmdPasteCells) })
	if !strings.Contains(tb.footer.Text, "Paste takes up to 2 rows") || tb.ed.pending.Len() != 0 {
		t.Errorf("more rows than a paste takes writes none: footer %q", tb.footer.Text)
	}
}

func TestPasteIsOnlyWhereRowsAreEdited(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "nokey", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	tb.grid.Select(grid.CellID{Row: 1, Col: 1}, grid.CellID{Row: 1, Col: 1})
	fx.s.app.Clipboard().SetContent("x")
	if fx.s.canPaste() {
		t.Error("rows that are not edited take no paste")
	}
	tb.grid.OnPaste()
	if tb.ed.pending != nil || strings.Contains(tb.footer.Text, "Past") {
		t.Errorf("⌘V starts no paste on them: %q", tb.footer.Text)
	}
}

func TestAResultTakesAPaste(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	r := ranItems(t, fx, q)
	r.ed.grid.Select(grid.CellID{Row: 1, Col: 1}, grid.CellID{Row: 1, Col: 1})
	fx.s.app.Clipboard().SetContent("pasted")
	r.ed.grid.OnPaste()
	pump(t, fx.q, func() bool { return strings.Contains(r.count.Text, "Pasted 1 cell") })
	row, _ := r.ed.model.Row(r.ed.ctx, 1)
	if v, _ := r.ed.pending.Value(row, 1); v != "pasted" || !strings.Contains(r.count.Text, "1 pending change") {
		t.Errorf("the result's own changes take it: %v; %q", v, r.count.Text)
	}
}
