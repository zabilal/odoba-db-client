package shell

import "testing"

func TestAKeyedTableKeepsItsChangesThroughASort(t *testing.T) {
	fx, tb := openItems(t)
	if tb.pending == nil || !tb.grid.Table.ShowHeaderColumn {
		t.Fatal("a table whose rows have a key should hold its edits and mark them in a gutter")
	}
	pending := tb.pending
	tb.grid.ToggleSort(1, false)
	pump(t, fx.q, func() bool { return len(tb.applied) == 1 })
	if tb.pending != pending || !tb.grid.Table.ShowHeaderColumn {
		t.Error("a sort reads the rows again; their edits should stay")
	}
}

func TestATableWithNoKeyHasNoChangesToShow(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "nokey", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	if tb.pending != nil || tb.grid.Table.ShowHeaderColumn {
		t.Error("rows that cannot be told apart are never edited, so nothing is marked")
	}
}
