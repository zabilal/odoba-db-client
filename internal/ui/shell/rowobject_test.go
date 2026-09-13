package shell

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

func TestARowThatNamesSomethingOpensIt(t *testing.T) {
	fx := newFixture(t)
	tb := openKeyspace(t, fx, "keys")
	// Nothing is selected yet, so there is nothing to open.
	if fx.s.canOpenRowObject() {
		t.Error("a row nobody is on names nothing")
	}
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	if !fx.s.canOpenRowObject() {
		t.Fatal("the first key names a key")
	}
	fx.s.run(cmdOpenRowObject)
	opened := tabOn(fx, "user:1")
	if opened == nil || fx.s.activeTab() != opened || !opened.ref.Equal(firstKey) {
		t.Fatalf("what the row names opens in front: %v", opened)
	}
	// What the key holds is rows of its own shape, read the same way.
	pump(t, fx.q, func() bool {
		if opened.model == nil {
			return false
		}
		_, ok := opened.model.Row(opened.ctx, 1)
		return ok
	})
	cols := opened.model.Columns()
	if len(cols) != 2 || cols[0].Name != "field" || cols[1].Name != "value" {
		t.Errorf("a hash shows %v", cols)
	}
	row, _ := opened.model.Row(opened.ctx, 0)
	if len(row) != 2 || row[0] != "city" {
		t.Errorf("the first row is %v", row)
	}
	// Asked again, the tab it is already in comes forward rather than a
	// second one opening on the same key.
	fx.s.selectTab(tb)
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	fx.s.run(cmdOpenRowObject)
	if len(fx.s.open) != 2 || fx.s.activeTab() != opened {
		t.Errorf("%d tabs open, and %v in front", len(fx.s.open), fx.s.activeTab().ref)
	}
}

func TestARowThatNamesNothingIsNotOffered(t *testing.T) {
	fx := newFixture(t)
	// A source that does not say its rows are objects offers nothing, and a
	// relational one never does.
	tb := openKeyspace(t, fx, "flat")
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	if fx.s.canOpenRowObject() {
		t.Error("a store whose rows name nothing offered to open one")
	}
	fx.s.run(cmdOpenRowObject) // does nothing, and does not panic
	if len(fx.s.open) != 1 {
		t.Errorf("%d tabs open", len(fx.s.open))
	}

	table := fx.create(t, "rows", nil)
	fx.s.OpenObject(table.ID, model.Node{Ref: model.NewRef(model.KindTable, "main", "people"), Label: "people", Browsable: true})
	people := tabOn(fx, "people")
	if people == nil {
		t.Fatal("the table did not open")
	}
	pump(t, fx.q, func() bool {
		if people.model == nil {
			return false
		}
		_, ok := people.model.Row(people.ctx, 0)
		return ok
	})
	people.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	if fx.s.canOpenRowObject() {
		t.Error("a table's row named something to open")
	}
}
