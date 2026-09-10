package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/export"
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
	if tb.footer.Text != "Exported 250 rows to items.csv" {
		t.Errorf("footer %q", tb.footer.Text)
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
