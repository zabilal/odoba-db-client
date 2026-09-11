package shell

import (
	"reflect"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// openFK opens the items table on a source whose items refer to parts by
// name, and waits for its rows and its description.
func openFK(t *testing.T) (*fixture, *tab) {
	t.Helper()
	fx := newFixture(t)
	c := fx.create(t, "fkeys", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool {
		if tb.model == nil {
			return false
		}
		_, ok := tb.model.Row(tb.ctx, 3)
		return ok && tb.table != nil
	})
	return fx, tb
}

// tabOn is the open tab on an object named name.
func tabOn(fx *fixture, name string) *tab {
	for _, t := range fx.s.open {
		if t.ref.Name() == name {
			return t
		}
	}
	return nil
}

// lastFilters are the filters of the last browse asked for.
func lastFilters() []source.Filter {
	browses.Lock()
	defer browses.Unlock()
	return browses.opts[len(browses.opts)-1].Filters
}

func TestGoToReferencedRowOpensTheTableFilteredToIt(t *testing.T) {
	fx, tb := openFK(t)
	tb.grid.Select(grid.CellID{Row: 2, Col: 0}, grid.CellID{Row: 2, Col: 0})
	if fx.s.canGoToReferenced() {
		t.Error("id is in no foreign key, so it refers to nothing")
	}
	tb.grid.Select(grid.CellID{Row: 2, Col: 1}, grid.CellID{Row: 2, Col: 1})
	if !fx.s.canGoToReferenced() {
		t.Fatal("name is in items_part, so it refers to a part")
	}
	fx.s.run(cmdGoToReferenced)
	parts := tabOn(fx, "parts")
	if parts == nil || fx.s.activeTab() != parts || !parts.ref.Equal(model.NewRef(model.KindTable, "main", "parts")) {
		t.Fatalf("the table referred to opens in front: %v", parts)
	}
	want := []source.Filter{{Column: "name", Op: source.OpEqual, Values: []any{"item 2"}}}
	pump(t, fx.q, func() bool { return parts.grid != nil && reflect.DeepEqual(parts.browse.Options().Filters, want) })
	if texts := parts.grid.FilterTexts(); texts[0] != "" || texts[1] == "" {
		t.Errorf("the filter row says what is applied: %q", texts)
	}
}

func TestGoingToAnOpenTableFiltersItAfresh(t *testing.T) {
	fx, tb := openFK(t)
	tb.grid.Select(grid.CellID{Row: 2, Col: 1}, grid.CellID{Row: 2, Col: 1})
	fx.s.run(cmdGoToReferenced)
	parts := tabOn(fx, "parts")
	pump(t, fx.q, func() bool { return parts.grid != nil && len(parts.browse.Options().Filters) == 1 })
	parts.grid.SetFilterText(0, ">100") // a filter the row would not pass
	fx.s.selectTab(tb)
	tb.grid.Select(grid.CellID{Row: 3, Col: 1}, grid.CellID{Row: 3, Col: 1})
	fx.s.run(cmdGoToReferenced)
	if tabOn(fx, "parts") != parts || len(fx.s.open) != 2 || fx.s.activeTab() != parts {
		t.Fatal("the tab open on the table is brought forward, not another opened")
	}
	want := []source.Filter{{Column: "name", Op: source.OpEqual, Values: []any{"item 3"}}}
	pump(t, fx.q, func() bool { return reflect.DeepEqual(parts.browse.Options().Filters, want) })
	if parts.grid.FilterTexts()[0] != "" || !reflect.DeepEqual(lastFilters(), want) {
		t.Error("every other filter is cleared, so none hides the row")
	}
}

func TestNothingIsReferredToByNULLOrOutsideATable(t *testing.T) {
	fx, tb := openFK(t)
	row, _ := tb.model.Row(tb.ctx, 2)
	if err := tb.grid.OnEdit(row, 1, nil); err != nil {
		t.Fatal(err)
	}
	tb.grid.Select(grid.CellID{Row: 2, Col: 1}, grid.CellID{Row: 2, Col: 1})
	if fx.s.canGoToReferenced() {
		t.Error("a NULL refers to nothing")
	}
	fx.s.run(cmdInsertRow)
	tb.grid.Select(grid.CellID{Row: 0, Col: 1}, grid.CellID{Row: 0, Col: 1})
	if fx.s.canGoToReferenced() {
		t.Error("a new row's column given nothing refers to nothing")
	}
	fx2 := newFixture(t)
	_, q := openQuery(t, fx2, "")
	ranItems(t, fx2, q)
	if fx2.s.canGoToReferenced() {
		t.Error("a query's result is not a table with foreign keys")
	}
}

func TestAReferencedTableIsInTheKeysSchema(t *testing.T) {
	fk := model.ForeignKey{RefSchema: "sales", RefTable: "parts"}
	for _, c := range []struct{ from, want model.ObjectRef }{
		{model.NewRef(model.KindTable, "db", "public", "items"), model.NewRef(model.KindTable, "db", "sales", "parts")}, // PostgreSQL
		{model.NewRef(model.KindTable, "shop", "items"), model.NewRef(model.KindTable, "sales", "parts")},               // MySQL
	} {
		if got := referenced(c.from, fk); !got.Equal(c.want) {
			t.Errorf("%v: %v, want %v", c.from, got, c.want)
		}
	}
	if got := referenced(model.NewRef(model.KindTable, "db", "public", "items"), model.ForeignKey{RefTable: "parts"}); !got.Equal(model.NewRef(model.KindTable, "db", "public", "parts")) {
		t.Errorf("a key naming no schema is in its table's: %v", got)
	}
}

func TestATableNotDescribedRefersToNothing(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "fkeys", nil)
	fx.s.OpenObject(c.ID, model.Node{Ref: model.NewRef(model.KindTable, "main", "boom"), Label: "boom"}) // describing it fails
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool {
		if tb.model == nil {
			return false
		}
		_, ok := tb.model.Row(tb.ctx, 3)
		return ok
	})
	tb.grid.Select(grid.CellID{Row: 2, Col: 1}, grid.CellID{Row: 2, Col: 1})
	if fx.s.canGoToReferenced() || tb.table != nil {
		t.Error("a table without a description has no foreign keys to follow")
	}
}
