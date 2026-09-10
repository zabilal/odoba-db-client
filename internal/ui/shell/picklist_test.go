package shell

import (
	"reflect"
	"testing"
	"time"

	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app/filterexpr"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func openPicklist(t *testing.T, fx *fixture, tb *tab, col int) *picklist {
	t.Helper()
	p := fx.s.showPicklist(tb, col)
	if p == nil {
		t.Fatal("no picklist")
	}
	pump(t, fx.q, func() bool { return p.loaded })
	return p
}

func TestPickingValuesFiltersByExactlyThem(t *testing.T) {
	fx, tb := openItems(t)
	p := openPicklist(t, fx, tb, 1)
	if !reflect.DeepEqual(p.on, []bool{true, true, true, true}) {
		t.Fatalf("ticked %v; with no filter every value starts ticked", p.on)
	}
	p.setShown(false)
	p.set(0, true)
	p.set(3, true)
	p.confirm()
	if got := tb.grid.FilterTexts()[1]; got != `item 1,"a,b"` {
		t.Errorf("the filter row shows %q", got)
	}
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 1 })
	want := source.Filter{Column: "name", Op: source.OpIn, Values: []any{"item 1", "a,b"}}
	if got := tb.browse.Options().Filters[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("filter %+v, want %+v", got, want)
	}
	again := openPicklist(t, fx, tb, 1)
	if !reflect.DeepEqual(again.on, []bool{true, false, false, true}) {
		t.Errorf("reopened, ticked %v; it should show what is picked", again.on)
	}
}

func TestUntickingAFewExcludesThem(t *testing.T) {
	fx, tb := openItems(t)
	p := openPicklist(t, fx, tb, 1)
	p.set(2, false) // NULL
	p.confirm()
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 1 })
	want := source.Filter{Column: "name", Op: source.OpNotIn, Values: []any{nil}}
	if got := tb.browse.Options().Filters[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("filter %+v, want %+v", got, want)
	}
	if got := tb.grid.FilterTexts()[1]; got != "!NULL" {
		t.Errorf("the filter row shows %q", got)
	}
}

func TestEditingAPickedFilterMakesTheTextTheFilter(t *testing.T) {
	fx, tb := openItems(t)
	p := openPicklist(t, fx, tb, 1)
	p.setShown(false)
	p.set(1, true)
	p.confirm()
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 1 })
	tb.grid.SetFilterText(1, "item")
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool {
		f := tb.browse.Options().Filters
		return len(f) == 1 && f[0].Op == source.OpContains
	})
	if _, ok := tb.picked[1]; ok {
		t.Error("an edited filter should stop being a pick")
	}
}

func TestTickingEverythingRemovesTheColumnsFilter(t *testing.T) {
	fx, tb := openItems(t)
	p := openPicklist(t, fx, tb, 1)
	p.set(0, false)
	p.confirm()
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 1 })
	again := openPicklist(t, fx, tb, 1)
	again.setShown(true)
	again.confirm()
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 0 })
	if got := tb.grid.FilterTexts()[1]; got != "" {
		t.Errorf("the filter row still shows %q", got)
	}
}

func TestNothingTickedCannotBeApplied(t *testing.T) {
	fx, tb := openItems(t)
	p := openPicklist(t, fx, tb, 1)
	p.setShown(false)
	if !p.apply.Disabled() {
		t.Error("Filter should be unavailable with nothing ticked")
	}
	p.confirm()
	fx.q.Flush()
	if tb.browseSeq != 0 {
		t.Error("nothing ticked must not filter")
	}
}

func TestSearchNarrowsWhatSelectAllTouches(t *testing.T) {
	fx, tb := openItems(t)
	p := openPicklist(t, fx, tb, 1)
	p.search.SetText("item")
	if len(p.shown) != 2 {
		t.Fatalf("%d values shown for “item”, want 2", len(p.shown))
	}
	p.setShown(false)
	if !reflect.DeepEqual(p.on, []bool{false, false, true, true}) {
		t.Errorf("ticked %v; Deselect All should touch only what is shown", p.on)
	}
}

func TestFilterByValuesFollowsTheSelectedCell(t *testing.T) {
	fx, tb := openItems(t)
	c, ok := fx.s.reg.Get(cmdFilterValues)
	if !ok {
		t.Fatal("no Filter by Values command")
	}
	if c.Enabled() {
		t.Error("with no cell selected there is no column to list")
	}
	tb.grid.Table.Select(widget.TableCellID{Row: 0, Col: 1})
	if !c.Enabled() {
		t.Error("a selected cell's column can be listed")
	}
}

// A pick's text reads back as text; the values sent must be the ones listed.
func TestAPickFiltersByTheValuesAsTyped(t *testing.T) {
	fx, tb := openItems(t)
	day := time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
	text := filterexpr.Pick([]any{day}, false)
	tb.picked = map[int]pick{1: {values: []any{day}, text: text}}
	tb.grid.SetFilterText(1, text)
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 1 })
	if got := tb.browse.Options().Filters[0].Values[0]; got != day {
		t.Errorf("sent %#v; the text reads back as %q, not the time that was listed", got, text)
	}
}
