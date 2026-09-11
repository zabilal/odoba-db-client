package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

func exportCSV(t *testing.T, fx *fixture, tb *tab, src *exportSrc) string {
	t.Helper()
	out := &sink{}
	j := fx.s.runExport(tb, src, export.Options{Format: export.CSV, Header: true}, out, "sel.csv", nil)
	pump(t, fx.q, func() bool { return j.done })
	if j.err != nil {
		t.Fatalf("export: %v", j.err)
	}
	return out.String()
}

func TestExportTheSelectionAlone(t *testing.T) {
	fx, tb := openItems(t)
	if fx.s.selectionSource(tb.grid, &exportSrc{name: "items"}) != nil {
		t.Error("with nothing selected there is no selection to export")
	}
	tb.grid.Select(grid.CellID{Row: 2, Col: 1}, grid.CellID{Row: 4, Col: 1}) // three rows of the name column
	got := exportCSV(t, fx, tb, fx.s.selectionSource(tb.grid, &exportSrc{name: "items"}))
	if got != "name\nitem 2\nitem 3\nitem 4\n" {
		t.Errorf("exported %q, want the three selected cells of name", got)
	}
}

func TestAWholeColumnSelectionStreamsToTheEnd(t *testing.T) {
	was := selectionPage
	selectionPage = 100 // the table's 250 rows then take three reads
	t.Cleanup(func() { selectionPage = was })
	fx, tb := openItems(t)
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: grid.End, Col: 0})
	src := fx.s.selectionSource(tb.grid, &exportSrc{name: "items"})
	if src.total != -1 {
		t.Errorf("a selection to the end has no known total, got %d", src.total)
	}
	got := exportCSV(t, fx, tb, src)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != fakeRows+1 || lines[0] != "id" || lines[fakeRows] != "249" {
		t.Errorf("%d lines, first %q, last %q; want id and all %d rows, across reads of %d",
			len(lines), lines[0], lines[len(lines)-1], fakeRows, selectionPage)
	}
}

// radios finds the radio groups in a canvas object.
func radios(o fyne.CanvasObject) []*widget.RadioGroup {
	var out []*widget.RadioGroup
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case nil:
			return
		case *widget.RadioGroup:
			out = append(out, v)
			return
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
			return
		}
		if w, ok := o.(fyne.Widget); ok {
			for _, c := range test.WidgetRenderer(w).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	return out
}

func TestTheSelectionIsOfferedNotAssumed(t *testing.T) {
	fx, tb := openItems(t)
	fx.s.showExport()
	if r := radios(fx.s.win.Canvas().Overlays().Top()); len(r) != 0 {
		t.Error("with nothing selected, the form should not ask which rows")
	}
	fx.s.win.Canvas().Overlays().Remove(fx.s.win.Canvas().Overlays().Top())
	tb.grid.Select(grid.CellID{Row: 2, Col: 1}, grid.CellID{Row: 4, Col: 1})
	fx.s.showExport()
	r := radios(fx.s.win.Canvas().Overlays().Top())
	if len(r) != 1 || r[0].Selected != allRows || len(r[0].Options) != 2 || r[0].Options[1] != "The selection: 3 rows, 1 column" {
		t.Fatalf("rows choice %+v; want All rows chosen, and the selection offered by its size", r)
	}
}

func TestChoosingTheSelectionExportsIt(t *testing.T) {
	all, sel := &exportSrc{name: "all"}, &exportSrc{name: "sel"}
	rows := widget.NewRadioGroup([]string{allRows, "The selection: 3 rows, 1 column"}, nil)
	rows.SetSelected(allRows)
	if pickSource(rows, all, sel) != all {
		t.Error("All rows chosen should export all rows")
	}
	rows.SetSelected("The selection: 3 rows, 1 column")
	if pickSource(rows, all, sel) != sel {
		t.Error("the selection chosen should export the selection")
	}
	if pickSource(nil, all, nil) != all {
		t.Error("with no selection there is only all")
	}
}
