package grid

import (
	"context"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestNewRowsComeBeforeTheRowsRead(t *testing.T) {
	ctx := context.Background()
	m := NewModel(NewSyntheticFetcher(600))
	m.SetAdded([]model.Row{{"a"}, {"b"}})
	if r, ok := m.Row(ctx, 1); !ok || r[0] != "b" || m.Added() != 2 || !m.Resident(1) {
		t.Fatalf("new rows are there at once: %v %v", r, ok)
	}
	waitFor(t, func() bool { _, ok := m.Row(ctx, 2); return ok }, "the first page")
	if r, _ := m.Row(ctx, 2); r[0] != int64(1) {
		t.Errorf("the first row read follows the new ones: %v", r)
	}
	if n, final := m.Extent(); n != PageSize+2 || final {
		t.Errorf("extent %d, final %v", n, final)
	}
	if err := m.LoadCount(ctx); err != nil {
		t.Fatal(err)
	}
	if total, _ := m.Total(); total != 600 {
		t.Errorf("the total is the rows read: %d", total)
	}
	if n, final := m.Extent(); n != 602 || !final {
		t.Errorf("the extent counts the new rows too: %d %v", n, final)
	}
	rows, err := m.Read(ctx, 1, 4)
	if err != nil || len(rows) != 3 || rows[0][0] != "b" || rows[1][0] != int64(1) || rows[2][0] != int64(2) {
		t.Errorf("a read crosses from the new rows to the rows read: %v %v", rows, err)
	}
	if rows, _ := m.Read(ctx, 0, 1); len(rows) != 1 || rows[0][0] != "a" {
		t.Errorf("a read of new rows alone: %v", rows)
	}
	if m.Resident(2+3*PageSize) || !m.Resident(PageSize+1) {
		t.Error("a row read is resident by its page, counted after the new rows")
	}
	before := m.Stats().Fetches
	m.Prefetch(ctx, 0, 1)
	if m.Stats().Fetches != before {
		t.Error("with only new rows in view there is nothing to fetch")
	}
	m.SetFetcher(NewSyntheticFetcher(10))
	if m.Added() != 2 {
		t.Error("a new sort or filter keeps the new rows")
	}
}

func TestANewRowIsDrawnAndEditedAsNew(t *testing.T) {
	g, p, got := editGrid(t)
	var added []any
	g.OnEditAdded = func(i, col int, v any) error {
		added = append(added, v)
		if err := p.SetAdded(i, col, v); err != nil {
			return err
		}
		g.model.SetAdded(p.Added())
		return nil
	}
	first := rowOf(t, g, 0)
	p.Add()
	g.model.SetAdded(p.Added())
	c := drawn(g, 0, 2)
	if c.text != DefaultText || !c.style.Italic || c.fg != g.palette.AddedFg || c.bg != g.palette.AddedBg {
		t.Errorf("a column not given: %q italic %v, fg %v bg %v", c.text, c.style.Italic, c.fg, c.bg)
	}
	if h := gutter(g, 0); h.title.text != "+" {
		t.Errorf("a new row is marked: %q", h.title.text)
	}
	if c := drawn(g, 1, 2); c.text != first[2] || gutter(g, 1).title.text != "" {
		t.Errorf("the rows read move down: %q", c.text)
	}
	click(g, 0, 2, 0)
	g.EditCell("Zed")
	g.editing.entry.TypedKey(key(fyne.KeyReturn))
	if len(added) != 1 || added[0] != "Zed" || len(*got) != 0 {
		t.Fatalf("a new row's cell is written to the new row: %v, rows read %v", added, *got)
	}
	if c := drawn(g, 0, 2); c.text != "Zed" {
		t.Errorf("drawn: %q", c.text)
	}
	click(g, 0, 3, 0)
	g.EditCell("")
	if g.editing.entry.Text != "" || g.editing.entry.PlaceHolder != DefaultText {
		t.Errorf("a column not given opens empty, saying DEFAULT: %q %q", g.editing.entry.Text, g.editing.entry.PlaceHolder)
	}
	g.editing.entry.TypedKey(key(fyne.KeyEscape))
	// A new row given the key of a row read with changes is still itself.
	if err := p.Set(first, 2, "Other"); err != nil {
		t.Fatal(err)
	}
	if err := g.OnEditAdded(0, 0, first[0]); err != nil {
		t.Fatal(err)
	}
	click(g, 0, 2, 0)
	g.EditCell("")
	if g.editing.entry.Text != "Zed" {
		t.Errorf("a new row opens with its own value: %q", g.editing.entry.Text)
	}
	g.editing.entry.TypedKey(key(fyne.KeyEscape))
	g.OnEditAdded = nil
	if err := g.SetValue(0, nil, 2, "x"); err == nil {
		t.Error("a grid that takes no new rows' values says so")
	}
}
