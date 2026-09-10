package shell

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

func openItems(t *testing.T) (*fixture, *tab) {
	t.Helper()
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	return fx, tb
}

func TestTheFilterRowBrowsesAgainOnTheServer(t *testing.T) {
	fx, tb := openItems(t)
	if !tb.grid.Filterable() {
		t.Fatal("a table the server can filter should have a filter row")
	}
	tb.grid.SetFilterText(1, "item 1")
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 1 })
	want := source.Filter{Column: "name", Op: source.OpContains, Values: []any{"item 1"}}
	if got := tb.browse.Options().Filters[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("filter %+v, want %+v", got, want)
	}
	pump(t, fx.q, func() bool { return strings.HasSuffix(tb.footer.Text, "· filtered") })
	tb.grid.SetFilterText(1, "")
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 0 })
}

func TestAFilterThatDoesNotParseIsExplainedNotSent(t *testing.T) {
	fx, tb := openItems(t)
	tb.grid.SetFilterText(0, ">abc")
	tb.grid.ApplyFilters()
	fx.q.Flush()
	if !tb.grid.FilterError(0) {
		t.Error("the id column's filter should be marked")
	}
	if !strings.Contains(tb.footer.Text, "Filter on id") || !strings.Contains(tb.footer.Text, "not a whole number") {
		t.Errorf("footer %q should say which filter is wrong, and why", tb.footer.Text)
	}
	if tb.browseSeq != 0 {
		t.Error("a filter that does not parse must not be sent")
	}
}

func TestASortMadeWhileFilteringKeepsTheFilter(t *testing.T) {
	fx, tb := openItems(t)
	tb.grid.SetFilterText(1, "item")
	tb.grid.ApplyFilters()
	tb.grid.ToggleSort(0, false) // before the filtered browse has landed
	pump(t, fx.q, func() bool {
		o := tb.browse.Options()
		return len(o.Sorts) == 1 && len(o.Filters) == 1
	})
	tb.grid.ToggleSort(0, false)
	pump(t, fx.q, func() bool {
		o := tb.browse.Options()
		return len(o.Sorts) == 1 && o.Sorts[0].Descending && len(o.Filters) == 1
	})
}

func TestAFilterTheServerRefusesIsMarkedAndExplained(t *testing.T) {
	fx, tb := openItems(t)
	tb.grid.SetFilterText(1, "~^item")
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { return tb.grid.FilterError(1) })
	if len(tb.browse.Options().Filters) != 0 {
		t.Error("a refused filter must leave the rows as they were")
	}
	if !strings.Contains(tb.footer.Text, "Could not filter: fakesql: no regular expressions") {
		t.Errorf("footer %q should say the filter was refused", tb.footer.Text)
	}
	tb.grid.SetFilterText(1, "item")
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 1 })
	if tb.grid.FilterError(1) || strings.Contains(tb.footer.Text, "Could not") {
		t.Errorf("a filter that works should clear the mark; footer %q", tb.footer.Text)
	}
}
