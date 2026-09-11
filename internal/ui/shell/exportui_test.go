package shell

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// sink is an in-memory export destination.
type sink struct {
	bytes.Buffer
	closed bool
}

func (s *sink) Close() error { s.closed = true; return nil }

func TestExportNeedsSomethingToExport(t *testing.T) {
	fx := newFixture(t)
	if !fx.s.menuItems[cmdExport].Disabled {
		t.Error("Export should be disabled with nothing open")
	}
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	if fx.s.menuItems[cmdExport].Disabled {
		t.Error("a table tab can be exported")
	}
}

func TestExportWritesTheWholeTable(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	out := &sink{}
	j := fx.s.runExport(tb, fx.s.exportSource(), export.Options{Format: export.CSV, Header: true}, out, "items.csv", nil)
	pump(t, fx.q, func() bool { return j.done })
	if j.err != nil || !out.closed {
		t.Fatalf("err %v, closed %v", j.err, out.closed)
	}
	if lines := strings.Count(out.String(), "\n"); lines != fakeRows+1 {
		t.Errorf("%d lines, want a header and %d rows", lines, fakeRows)
	}
	if !strings.HasPrefix(out.String(), "id,name\n0,item 0\n") {
		t.Errorf("starts %q", out.String()[:min(30, out.Len())])
	}
	fx.s.showCount(tb) // as a page loading, or the count landing, would
	if !strings.HasSuffix(tb.footer.Text, "Exported 250 rows to items.csv") {
		t.Errorf("footer %q; the count must not wipe what the export said", tb.footer.Text)
	}
}

func TestExportAQueryResult(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	q.editor.Document().SetText("rows 5;")
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return !q.executing && len(q.sets) == 1 })
	waitDone(t, q.sets[0].Done())
	src := fx.s.exportSource()
	if src == nil || src.total != 5 || src.name != "Query 1 result 1" {
		t.Fatalf("source %+v", src)
	}
	out := &sink{}
	j := fx.s.runExport(tb, src, export.Options{Format: export.JSON}, out, "r.json", nil)
	pump(t, fx.q, func() bool { return j.done })
	var got []map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got) != 5 {
		t.Errorf("%d objects, %v", len(got), err)
	}
	q.results.SelectIndex(0) // Messages: nothing to export
	if fx.s.exportSource() != nil {
		t.Error("the Messages tab has no rows to export")
	}
}

func TestCancellingAnExportDiscardsTheFile(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	q.editor.Document().SetText("slow 100000;")
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return len(q.sets) == 1 })
	discarded := false
	out := &sink{}
	j := fx.s.runExport(tb, fx.s.exportSource(), export.Options{Format: export.NDJSON}, out, "r.ndjson",
		func() { discarded = true })
	j.cancel()
	pump(t, fx.q, func() bool { return j.done })
	if !errors.Is(j.err, context.Canceled) || !discarded || !out.closed {
		t.Errorf("err %v, discarded %v, closed %v", j.err, discarded, out.closed)
	}
	if !strings.Contains(tb.footer.Text, "cancelled") {
		t.Errorf("footer %q", tb.footer.Text)
	}
}

func TestExportStatusWording(t *testing.T) {
	for _, c := range []struct {
		p     export.Progress
		total int64
		want  string
	}{
		{export.Progress{}, -1, "0 rows"},
		{export.Progress{Rows: 5000, Elapsed: time.Second}, -1, "5,000 rows · 5,000 rows/s"},
		{export.Progress{Rows: 5000, Elapsed: time.Second}, 20000, "5,000 of 20,000 rows · 5,000 rows/s · about 3 s left"},
		{export.Progress{Rows: 5000, Elapsed: time.Second}, 1000000, "5,000 of 1,000,000 rows · 5,000 rows/s · about 3 min left"},
	} {
		if got := exportStatus(c.p, c.total); got != c.want {
			t.Errorf("exportStatus(%+v, %d) = %q, want %q", c.p, c.total, got, c.want)
		}
	}
}

func TestFileNameIsSafe(t *testing.T) {
	if got := fileName(` a/b:c* `); got != "a_b_c_" {
		t.Errorf("%q", got)
	}
	if fileName("  ") != "export" {
		t.Error("an empty name needs a fallback")
	}
}

func TestAnExportCanBeAWorkbook(t *testing.T) {
	fx, tb := openItems(t)
	out := &sink{}
	j := fx.s.runExport(tb, fx.s.exportSource(), exportOptions(export.XLSX, true, &exportSrc{name: "items"}), out, "items.xlsx", nil)
	pump(t, fx.q, func() bool { return j.done })
	if j.err != nil || !out.closed || !bytes.HasPrefix(out.Bytes(), []byte("PK")) || j.task.status != "Exported 250 rows to items.xlsx" {
		t.Errorf("err %v, closed %v, starts %q, task %q", j.err, out.closed, out.Bytes()[:min(4, out.Len())], j.task.status)
	}
}

func TestTheHeaderIsOfferedWhereAFormatHasOne(t *testing.T) {
	for _, f := range export.Formats() {
		if want := f == export.CSV || f == export.TSV || f == export.XLSX; headerApplies(f) != want {
			t.Errorf("%v: header offered %v", f, !want)
		}
	}
	if got := exportOptions(export.XLSX, true, &exportSrc{name: "items"}); got.Format != export.XLSX || !got.Header || got.Name != "items" {
		t.Errorf("a workbook's sheet takes the name of what is exported: %+v", got)
	}
}

// findSelect is the first picker under o, as findButton looks.
func findSelect(o fyne.CanvasObject) *widget.Select {
	switch v := o.(type) {
	case *widget.Select:
		return v
	case *widget.PopUp:
		return findSelect(v.Content)
	case *fyne.Container:
		for _, c := range v.Objects {
			if s := findSelect(c); s != nil {
				return s
			}
		}
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(v).Objects() {
			if s := findSelect(c); s != nil {
				return s
			}
		}
	}
	return nil
}

// findCheck is the check box under o with the text, as findButton looks.
func findCheck(o fyne.CanvasObject, text string) *widget.Check {
	switch v := o.(type) {
	case *widget.Check:
		if v.Text == text {
			return v
		}
	case *widget.PopUp:
		return findCheck(v.Content, text)
	case *fyne.Container:
		for _, c := range v.Objects {
			if k := findCheck(c, text); k != nil {
				return k
			}
		}
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(v).Objects() {
			if k := findCheck(c, text); k != nil {
				return k
			}
		}
	}
	return nil
}

func TestAWorkbookIsOfferedWithItsHeader(t *testing.T) {
	fx, tb := openItems(t)
	fx.s.showExport()
	form := fx.s.win.Canvas().Overlays().Top()
	format, header := findSelect(form), findCheck(form, "Column names as the first line")
	if format == nil || header == nil {
		t.Fatal("the export form asks the format, and whether the column names come first")
	}
	format.SetSelected("JSON")
	if !header.Disabled() {
		t.Error("JSON names every value already")
	}
	format.SetSelected("Excel")
	if header.Disabled() {
		t.Error("a workbook's first row can be its column names")
	}
	test.Tap(findButton(form, "Choose File…"))
	if len(fx.files.saves) != 1 || fx.files.saves[0].Name != tb.item.Text+".xlsx" || fx.files.saves[0].Kind != "Excel" {
		t.Fatalf("the save dialog was asked %+v", fx.files.saves)
	}
	path := filepath.Join(t.TempDir(), "chosen.xlsx")
	fx.files.answer(path, nil)
	if len(fx.s.tasks) != 1 {
		t.Fatalf("%d tasks, want the export", len(fx.s.tasks))
	}
	k := fx.s.tasks[0]
	pump(t, fx.q, func() bool { return k.state != taskRunning })
	zr, err := zip.OpenReader(path)
	if err != nil || k.state != taskDone {
		t.Fatalf("task %v (%s); file: %v", k.state, k.status, err)
	}
	defer zr.Close()
	var workbook string
	for _, f := range zr.File {
		if f.Name == "xl/workbook.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(rc)
			rc.Close()
			workbook = string(b)
		}
	}
	if !strings.Contains(workbook, `<sheet name="`+tb.item.Text+`"`) {
		t.Errorf("the sheet takes the tab's name: %s", workbook)
	}
}

func TestATablesRowsExportAsItsInserts(t *testing.T) {
	fx, tb := openItems(t)
	src := fx.s.exportSource()
	if src.inserts == nil || !slices.Contains(exportFormats(src), export.SQLInsert) {
		t.Fatal("a table's rows can be written as its INSERT statements")
	}
	out := &sink{}
	j := fx.s.runExport(tb, src, exportOptions(export.SQLInsert, false, src), out, "items.sql", nil)
	pump(t, fx.q, func() bool { return j.done })
	if j.err != nil || strings.Count(out.String(), "INSERT 2 ") != fakeRows || !strings.HasPrefix(out.String(), "INSERT 2 [0 item 0];\n") {
		t.Errorf("err %v, starts %q", j.err, out.String()[:min(40, out.Len())])
	}
	if got := exportFormats(&exportSrc{name: "result"}); slices.Contains(got, export.SQLInsert) || len(got) != len(export.Formats())-1 || len(exportFormats(src)) != len(export.Formats()) {
		t.Error("rows with no one table to insert into are not offered as INSERTs; a table's are offered every format")
	}
	tb.grid.Select(grid.CellID{Row: 2, Col: 1}, grid.CellID{Row: 4, Col: 1})
	if sel := fx.s.selectionSource(tb.grid, src); sel == nil || sel.inserts == nil || sel.name != "items selection" {
		t.Error("a selection of a table's rows is written as its INSERTs too")
	}
}
