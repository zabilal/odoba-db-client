package grid

import (
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

var ordersKey = model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"},
	Target: model.NewRef(model.KindTable, "public", "orders")}

// pendingGrid is a grid showing the pending changes to its rows, with the
// first of them loaded.
func pendingGrid(t *testing.T, g *TableGrid) *app.Pending {
	t.Helper()
	p, err := app.NewPending(g.model.Columns(), ordersKey)
	if err != nil {
		t.Fatal(err)
	}
	g.SetChanges(p)
	waitFor(t, func() bool { _, ok := g.model.Row(g.ctx, 3); return ok }, "the first rows")
	return p
}

func rowOf(t *testing.T, g *TableGrid, i int) model.Row {
	t.Helper()
	row, ok := g.model.Row(g.ctx, int64(i))
	if !ok {
		t.Fatalf("row %d is not loaded", i)
	}
	return row
}

// drawn is a cell as the grid draws it.
func drawn(g *TableGrid, r, c int) *cellWidget {
	cell := g.createCell().(*cellWidget)
	g.UpdateCell(widget.TableCellID{Row: r, Col: c}, cell)
	return cell
}

// gutter is a row's mark as the grid draws it.
func gutter(g *TableGrid, r int) *columnHeader {
	h := g.createHeader().(*columnHeader)
	g.updateHeader(widget.TableCellID{Row: r, Col: -1}, h)
	return h
}

func TestAChangedCellShowsItsNewValueBoldOnItsTint(t *testing.T) {
	g := selectingGrid(t)
	p := pendingGrid(t, g)
	cols := g.model.Columns()
	row := rowOf(t, g, 1)
	was := shown(Format(row[2], cols[2], g.loc))
	if err := p.Set(row, 2, "Someone Else"); err != nil {
		t.Fatal(err)
	}
	c := drawn(g, 1, 2)
	if c.text != "Someone Else" || !c.style.Bold || c.fg != g.palette.ModifiedFg || c.bg != g.palette.ModifiedBg {
		t.Errorf("a changed cell: %q bold %v, fg %v bg %v", c.text, c.style.Bold, c.fg, c.bg)
	}
	if c.hint != "Changed from "+was {
		t.Errorf("a changed cell should say what it was: %q", c.hint)
	}
	if o := drawn(g, 1, 1); o.style.Bold || o.bg != g.palette.AlternateRow || o.text != shown(Format(row[1], cols[1], g.loc)) {
		t.Errorf("a cell not changed, in a changed row, is as it was: %q bold %v bg %v", o.text, o.style.Bold, o.bg)
	}
	if f := drawn(g, 1, len(g.order)); f.bg != g.palette.AlternateRow {
		t.Errorf("a changed row is not tinted to its edge, only its cells: %v", f.bg)
	}

	h := gutter(g, 1)
	if h.title.text != "•" || h.title.fg != g.palette.ModifiedFg || h.bg.FillColor != g.palette.ModifiedBg {
		t.Errorf("the gutter marks a changed row: %q, fg %v bg %v", h.title.text, h.title.fg, h.bg.FillColor)
	}
	if h.title.hint != "1 cell changed" || h.handle.Visible() {
		t.Errorf("the mark says %q; handle shown %v", h.title.hint, h.handle.Visible())
	}
	if err := p.Set(row, 3, nil); err != nil {
		t.Fatal(err)
	}
	if c := drawn(g, 1, 3); c.text != NullText || !c.style.Italic || !c.style.Bold {
		t.Errorf("a cell changed to NULL is NULL, and changed: %q italic %v bold %v", c.text, c.style.Italic, c.style.Bold)
	}
	if h := gutter(g, 1); h.title.hint != "2 cells changed" {
		t.Errorf("the mark counts the cells changed: %q", h.title.hint)
	}
	if h := gutter(g, 0); h.title.text != "" || h.title.hint != "" || h.bg.FillColor != g.palette.SidebarBackground {
		t.Errorf("an unchanged row has no mark: %q %q %v", h.title.text, h.title.hint, h.bg.FillColor)
	}
}

func TestADeletedRowIsStruckThroughToTheGridsEdge(t *testing.T) {
	g := selectingGrid(t)
	p := pendingGrid(t, g)
	p.Delete(rowOf(t, g, 2))
	for col := range g.order {
		c := drawn(g, 2, col)
		if !c.style.Strikethrough || c.fg != g.palette.DeletedFg || c.bg != g.palette.DeletedBg || c.hint != "To be deleted" {
			t.Errorf("column %d of a deleted row: struck %v, fg %v bg %v, %q", col, c.style.Strikethrough, c.fg, c.bg, c.hint)
		}
	}
	if f := drawn(g, 2, len(g.order)); f.bg != g.palette.DeletedBg {
		t.Errorf("the filler carries a deleted row's tint to the edge: %v", f.bg)
	}
	if c := drawn(g, 3, 1); c.style.Strikethrough || c.bg != g.palette.AlternateRow {
		t.Error("the next row is not deleted")
	}
	if h := gutter(g, 2); h.title.text != "−" || h.title.fg != g.palette.DeletedFg || h.title.hint != "To be deleted" {
		t.Errorf("the gutter marks a deleted row: %q %v %q", h.title.text, h.title.fg, h.title.hint)
	}
}

func TestASelectedChangeKeepsItsSignInTheSelectionsColours(t *testing.T) {
	g := selectingGrid(t)
	p := pendingGrid(t, g)
	if err := p.Set(rowOf(t, g, 1), 2, "Someone Else"); err != nil {
		t.Fatal(err)
	}
	p.Delete(rowOf(t, g, 2))
	click(g, 1, 2, 0)
	click(g, 2, 1, fyne.KeyModifierShortcutDefault)
	c := drawn(g, 1, 2)
	if c.bg != g.palette.SelectedUnemphasized || c.fg != g.palette.Label || !c.style.Bold {
		t.Errorf("a selected changed cell: bg %v fg %v bold %v", c.bg, c.fg, c.style.Bold)
	}
	d := drawn(g, 2, 1)
	if d.bg != g.palette.SelectedUnemphasized || d.fg != g.palette.Label || !d.style.Strikethrough {
		t.Errorf("a selected deleted cell: bg %v fg %v struck %v", d.bg, d.fg, d.style.Strikethrough)
	}
	if o := drawn(g, 2, 0); o.fg != g.palette.DeletedFg {
		t.Error("a cell not selected keeps the change's colour")
	}
}

// allAdded says every row is new, as rows added will be (T2.4).
type allAdded struct{}

func (allAdded) State(model.Row) model.RowState   { return model.RowAdded }
func (allAdded) Value(model.Row, int) (any, bool) { return nil, false }

func TestANewRowIsOnTheAddedTint(t *testing.T) {
	g := selectingGrid(t)
	g.SetChanges(allAdded{})
	waitFor(t, func() bool { _, ok := g.model.Row(g.ctx, 0); return ok }, "the first row")
	if c := drawn(g, 0, 1); c.fg != g.palette.AddedFg || c.bg != g.palette.AddedBg || c.hint != "New row" || c.style.Strikethrough {
		t.Errorf("a new row's cell: fg %v bg %v %q", c.fg, c.bg, c.hint)
	}
	if f := drawn(g, 0, len(g.order)); f.bg != g.palette.AddedBg {
		t.Errorf("the filler carries a new row's tint: %v", f.bg)
	}
	if h := gutter(g, 0); h.title.text != "+" || h.title.fg != g.palette.AddedFg || h.title.hint != "New row" {
		t.Errorf("the gutter marks a new row: %q %v %q", h.title.text, h.title.fg, h.title.hint)
	}
	if h := gutter(g, 150); h.title.text != "" {
		t.Error("a row past the end of the rows is no row, so it has no mark")
	}
	if h := gutter(g, 5000); h.title.text != "" {
		t.Error("a row still loading is not known yet, so it has no mark")
	}
}

// walk visits every object drawn under o.
func walk(o fyne.CanvasObject, visit func(fyne.CanvasObject)) {
	visit(o)
	var kids []fyne.CanvasObject
	switch v := o.(type) {
	case *fyne.Container:
		kids = v.Objects
	case fyne.Widget:
		kids = test.WidgetRenderer(v).Objects()
	}
	for _, k := range kids {
		walk(k, visit)
	}
}

func TestTheMarkIsDrawnInTheGutter(t *testing.T) {
	g := selectingGrid(t)
	p := pendingGrid(t, g)
	p.Delete(rowOf(t, g, 1))
	g.Table.Refresh()
	var mark *headerCell
	walk(g.View(), func(o fyne.CanvasObject) {
		if h, ok := o.(*headerCell); ok && h.text == "−" {
			mark = h
		}
	})
	if mark == nil {
		t.Fatal("no mark drawn for the deleted row")
	}
	if mark.Size().Width != gutterWidth {
		t.Errorf("the mark is %v wide; the gutter is %v", mark.Size().Width, gutterWidth)
	}
}

// rowsFetcher serves the rows it holds.
type rowsFetcher struct {
	cols []model.ColumnDef
	rows []model.Row
}

func (f rowsFetcher) Columns() []model.ColumnDef           { return f.cols }
func (f rowsFetcher) Count(context.Context) (int64, error) { return int64(len(f.rows)), nil }
func (f rowsFetcher) Fetch(_ context.Context, off, lim int64) ([]model.Row, error) {
	if off >= int64(len(f.rows)) {
		return nil, nil
	}
	return f.rows[off:min(off+lim, int64(len(f.rows)))], nil
}

func TestACellChangedFromNothingSaysSo(t *testing.T) {
	test.NewTempApp(t)
	f := rowsFetcher{cols: []model.ColumnDef{{Name: "id", Type: model.DataType{Class: model.TypeInteger}}, {Name: "name"}},
		rows: []model.Row{{int64(1), ""}}}
	g := NewTableGridWith(context.Background(), NewModel(f), theme.Light, (&uithread.Queue{}).Run, 0)
	p := pendingGrid(t, g)
	if err := p.Set(rowOf(t, g, 0), 1, "ann"); err != nil {
		t.Fatal(err)
	}
	if c := drawn(g, 0, 1); c.hint != "Changed from an empty value" {
		t.Errorf("%q", c.hint)
	}
}

func TestTheGutterIsThereOnlyWithChangesAndSortsNothing(t *testing.T) {
	g := selectingGrid(t)
	used := float32(0)
	for _, c := range g.Shown() {
		used += g.ColumnWidth(c) + theme.SeparatorWidth
	}
	g.viewWidth = used + 300
	g.fitFiller()
	if g.Table.ShowHeaderColumn {
		t.Error("a grid with no changes to show has no gutter")
	}
	pendingGrid(t, g)
	if !g.Table.ShowHeaderColumn || g.fillerWidth != 300-gutterWidth-theme.SeparatorWidth {
		t.Errorf("gutter %v; the filler should give it its width: %v", g.Table.ShowHeaderColumn, g.fillerWidth)
	}
	sorted := false
	g.OnSort = func([]SortKey) { sorted = true }
	h := gutter(g, 1)
	h.title.Tapped(&fyne.PointEvent{})
	filler := g.createHeader().(*columnHeader)
	g.updateHeader(widget.TableCellID{Row: -1, Col: len(g.order)}, filler)
	filler.title.Tapped(&fyne.PointEvent{})
	if sorted || len(g.Sorts()) != 0 {
		t.Errorf("tapping the gutter or the filler sorts nothing: %v", g.Sorts())
	}
	if h.title.Cursor() != desktop.DefaultCursor {
		t.Error("the gutter is not a button")
	}
	g.SetChanges(nil)
	if g.Table.ShowHeaderColumn || g.fillerWidth != 300 {
		t.Errorf("with no changes the gutter goes, and the filler takes its width back: %v", g.fillerWidth)
	}
}

// Fyne keeps the gutter's cells and the column headers in one pool, so a
// header can come back as a column's after marking a row.
func TestAMarkMovedToAColumnIsAColumnsTitleAgain(t *testing.T) {
	g := sortableGrid(t)
	g.SetFilterable(true)
	test.NewTempWindow(t, g.View()).Resize(fyne.NewSize(600, 400))
	p := pendingGrid(t, g)
	p.Delete(rowOf(t, g, 1))
	h := gutter(g, 1)
	if h.field.Visible() || h.title.hint == "" {
		t.Fatalf("in the gutter: field shown %v, hint %q", h.field.Visible(), h.title.hint)
	}
	g.updateHeader(widget.TableCellID{Row: -1, Col: 0}, h)
	if h.title.text != "id" || h.title.hint != "" || h.title.col != 0 || h.bg.FillColor != g.palette.SidebarBackground {
		t.Errorf("back as a column's: %q %q col %d bg %v", h.title.text, h.title.hint, h.title.col, h.bg.FillColor)
	}
	if !h.handle.Visible() || !h.field.Visible() || h.title.Cursor() != desktop.PointerCursor {
		t.Error("a column's header resizes, filters and sorts again")
	}
}

func TestRestingOnAMarkSaysItInWords(t *testing.T) {
	tipDelay = 0
	t.Cleanup(func() { tipDelay = 600 * time.Millisecond })
	g := selectingGrid(t)
	p := pendingGrid(t, g)
	if err := p.Set(rowOf(t, g, 1), 2, "Someone Else"); err != nil {
		t.Fatal(err)
	}
	h := gutter(g, 1)
	h.title.MouseIn(&desktop.MouseEvent{PointEvent: fyne.PointEvent{AbsolutePosition: fyne.NewPos(10, 60)}})
	if !g.tip.Visible() || g.tipText.Text != "1 cell changed" {
		t.Errorf("tip shown %v, saying %q", g.tip.Visible(), g.tipText.Text)
	}
}
