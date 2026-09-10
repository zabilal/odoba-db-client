package grid

import (
	"reflect"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

func filterableGrid(t *testing.T) *TableGrid {
	t.Helper()
	g := sortableGrid(t)
	g.SetFilterable(true)
	return g
}

// header builds a header cell and binds it to a column, as the table does.
func header(t *testing.T, g *TableGrid, col int) *columnHeader {
	t.Helper()
	h, ok := g.createHeader().(*columnHeader)
	if !ok {
		t.Fatalf("a filterable grid's header is %T", g.createHeader())
	}
	g.updateHeader(widget.TableCellID{Row: -1, Col: col}, h)
	return h
}

func TestOnlyAFilterableGridHasFilterFields(t *testing.T) {
	g := sortableGrid(t)
	if _, ok := g.createHeader().(*headerCell); !ok {
		t.Errorf("an unfilterable grid's header is %T", g.createHeader())
	}
	g.SetFilterable(true)
	if _, ok := g.createHeader().(*columnHeader); !ok {
		t.Errorf("a filterable grid's header is %T", g.createHeader())
	}
}

// The first screenshot of the filter row had every data row as high as the
// header: Fyne sizes rows by the taller of the cell and header templates.
func TestAFilterRowDoesNotMakeEveryRowTaller(t *testing.T) {
	g := filterableGrid(t)
	w := test.NewTempWindow(t, g.View())
	w.Resize(fyne.NewSize(800, 400))
	cells, headers := 0, 0
	for _, o := range test.LaidOutObjects(w.Canvas().Content()) {
		switch c := o.(type) {
		case *cellWidget:
			cells++
			if h := c.Size().Height; h != theme.RowHeight {
				t.Fatalf("a data row is %v high, want %v", h, theme.RowHeight)
			}
		case *columnHeader:
			headers++
			if got, need := c.filter.Size().Height, c.filter.MinSize().Height; got < need {
				t.Fatalf("the filter field is %v high and needs %v", got, need)
			}
		}
	}
	if cells == 0 || headers == 0 {
		t.Fatalf("laid out %d cells and %d headers", cells, headers)
	}
}

func TestReturnAppliesEveryColumnsFilter(t *testing.T) {
	g := filterableGrid(t)
	var got []string
	g.OnFilter = func(texts []string) { got = texts }
	a, b := header(t, g, 0), header(t, g, 2)
	test.Type(a.filter, "x")
	test.Type(b.filter, ">5")
	b.filter.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	want := make([]string, len(g.model.Columns()))
	want[0], want[2] = "x", ">5"
	if !reflect.DeepEqual(got, want) {
		t.Errorf("applied %q, want %q", got, want)
	}
}

func TestEscapeClearsTheColumnAndApplies(t *testing.T) {
	g := filterableGrid(t)
	calls := 0
	var got []string
	g.OnFilter = func(texts []string) { got, calls = texts, calls+1 }
	h := header(t, g, 1)
	test.Type(h.filter, "abc")
	h.filter.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if calls != 1 || got[1] != "" || h.filter.Text != "" {
		t.Errorf("%d applies, texts %q, field %q; Escape should clear and apply", calls, got, h.filter.Text)
	}
}

func TestARecycledFieldShowsItsNewColumn(t *testing.T) {
	g := filterableGrid(t)
	h := header(t, g, 1)
	test.Type(h.filter, "abc")
	g.updateHeader(widget.TableCellID{Row: -1, Col: 2}, h)
	if h.filter.Text != "" {
		t.Errorf("moved to column 2, the field shows %q", h.filter.Text)
	}
	test.Type(h.filter, "z")
	g.updateHeader(widget.TableCellID{Row: -1, Col: 1}, h)
	if h.filter.Text != "abc" {
		t.Errorf("back on column 1, the field shows %q", h.filter.Text)
	}
	if got := g.FilterTexts(); got[1] != "abc" || got[2] != "z" {
		t.Errorf("texts %q: each column keeps its own", got)
	}
}

func TestAFocusedFieldLetsGoWhenItMovesColumn(t *testing.T) {
	g := filterableGrid(t)
	h := header(t, g, 1)
	w := test.NewTempWindow(t, h)
	w.Resize(fyne.NewSize(200, 80)) // a header's height: alone, it asks for one row
	w.Canvas().Focus(h.filter)
	g.updateHeader(widget.TableCellID{Row: -1, Col: 1}, h)
	if w.Canvas().Focused() == nil {
		t.Fatal("refreshing the same column should keep the focus")
	}
	g.updateHeader(widget.TableCellID{Row: -1, Col: 2}, h)
	if w.Canvas().Focused() != nil {
		t.Error("a field recycled to another column kept the focus")
	}
}

func TestAFailedFilterMarksItsTitle(t *testing.T) {
	g := filterableGrid(t)
	h := header(t, g, 1)
	g.SetFilterErrors(1)
	g.updateHeader(widget.TableCellID{Row: -1, Col: 1}, h)
	if !g.FilterError(1) || h.title.fg != g.palette.Danger {
		t.Errorf("marked %v, title colour %v", g.FilterError(1), h.title.fg)
	}
	g.SetFilterErrors()
	g.updateHeader(widget.TableCellID{Row: -1, Col: 1}, h)
	if g.FilterError(1) || h.title.fg == g.palette.Danger {
		t.Error("clearing the errors should clear the mark")
	}
}
