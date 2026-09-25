package shell

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// Saved views (FR-3.16): a table's rows as somebody arranged them, under a
// name, so that the arrangement can be had again.

// arranged filters and sorts the items table, as somebody would before
// saving a view of it.
func arranged(t *testing.T, fx *fixture, tb *tab) {
	t.Helper()
	tb.grid.SetFilterText(1, "item 1")
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 1 })
	// Hidden after the filter has landed: a rebrowse builds the grid's
	// columns again, and a column hidden before it would come back.
	pump(t, fx.q, func() bool {
		fx.q.Run(func() { tb.grid.HideColumn(1) })
		return tb.grid.Hidden()
	})
	fx.s.sync()
}

// A view keeps what the rows are, not what they were: its filters, its
// sort and the columns it shows.
func TestAViewKeepsHowTheRowsAreArranged(t *testing.T) {
	fx, tb := openItems(t)
	arranged(t, fx, tb)

	fx.q.Run(func() { fx.s.keepView(tb, "Item ones") })
	ctx := context.Background()
	var saved []localdb.View
	pump(t, fx.q, func() bool {
		saved, _ = fx.hist.Views(ctx, tb.connID, string(tb.ref.Kind), tb.ref.Path)
		return len(saved) == 1
	})
	v := saved[0]
	if v.Name != "Item ones" {
		t.Errorf("the view is called %q", v.Name)
	}
	if v.Tab.Filters["name"] != "item 1" {
		t.Errorf("it kept the filters %v", v.Tab.Filters)
	}
	if len(v.Tab.Hidden) != 1 || v.Tab.Hidden[0] != "name" {
		t.Errorf("it kept the hidden columns %v", v.Tab.Hidden)
	}
	// And it is a view of this table, so that another table's list does
	// not hold it.
	if v.Tab.ConnectionID != tb.connID || len(v.Tab.RefPath) == 0 {
		t.Errorf("the view is of %+v", v.Tab)
	}
}

// Picking a view arranges the rows the way it says.
func TestPickingAViewArrangesTheRows(t *testing.T) {
	fx, tb := openItems(t)
	arranged(t, fx, tb)
	fx.q.Run(func() { fx.s.keepView(tb, "Item ones") })
	ctx := context.Background()
	var saved []localdb.View
	pump(t, fx.q, func() bool {
		saved, _ = fx.hist.Views(ctx, tb.connID, string(tb.ref.Kind), tb.ref.Path)
		return len(saved) == 1
	})

	// Put the rows back as they opened, so that applying the view has
	// something to do.
	fx.q.Run(func() {
		tb.grid.SetFilterText(1, "")
		tb.grid.ApplyFilters()
		tb.grid.ShowAllColumns()
	})
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 0 })

	fx.q.Run(func() { fx.s.applyView(tb, saved[0]) })
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 1 })
	if got := tb.grid.FilterTexts(); len(got) < 2 || got[1] != "item 1" {
		t.Errorf("the filters read %q", got)
	}
	if !tb.grid.Hidden() {
		t.Error("the view hid a column and the grid shows them all")
	}
}

// Forgetting a view takes it off the list. The rows are left as they are:
// forgetting how to get back somewhere is not going anywhere.
func TestForgettingAViewLeavesTheRows(t *testing.T) {
	fx, tb := openItems(t)
	arranged(t, fx, tb)
	fx.q.Run(func() { fx.s.keepView(tb, "Item ones") })
	ctx := context.Background()
	var saved []localdb.View
	pump(t, fx.q, func() bool {
		saved, _ = fx.hist.Views(ctx, tb.connID, string(tb.ref.Kind), tb.ref.Path)
		return len(saved) == 1
	})

	fx.q.Run(func() { fx.s.forgetView(saved[0]) })
	pump(t, fx.q, func() bool {
		left, _ := fx.hist.Views(ctx, tb.connID, string(tb.ref.Kind), tb.ref.Path)
		return len(left) == 0
	})
	if len(tb.browse.Options().Filters) != 1 {
		t.Error("forgetting the view changed the rows")
	}
}

// What a view would keep is said before it is saved, because a view of
// rows nobody has arranged is the one somebody would not have meant.
func TestAViewSaysWhatItWouldKeep(t *testing.T) {
	fx, tb := openItems(t)
	// A grid opens with its key column frozen, which is an arrangement
	// like any other and is kept like one. Rows arranged in no way at all
	// keep nothing, and say so.
	if got := describeArrangement(sessionTab(tb)); !strings.Contains(got, "1 frozen column") {
		t.Errorf("rows as they opened read %q", got)
	}
	pump(t, fx.q, func() bool {
		fx.q.Run(tb.grid.Unfreeze)
		return tb.grid.Frozen() == 0
	})
	fx.s.sync()
	if got := describeArrangement(sessionTab(tb)); !strings.Contains(got, "keep nothing") {
		t.Errorf("rows arranged in no way read %q", got)
	}
	arranged(t, fx, tb)
	got := describeArrangement(sessionTab(tb))
	if !strings.Contains(got, "1 filter") || !strings.Contains(got, "1 hidden column") {
		t.Errorf("arranged rows read %q", got)
	}
}

// The commands are offered on rows somebody could arrange, and not on a
// query's result, which is arranged by its query.
func TestWhatCanBeSavedAsAView(t *testing.T) {
	fx, tb := openItems(t)
	if !fx.s.canSaveView() || !fx.s.canShowViews() {
		t.Error("a table's rows were not offered a view")
	}
	fx.s.newQueryTab(tb.connID)
	fx.s.sync()
	if fx.s.canSaveView() {
		t.Error("a query was offered a view of itself")
	}
}
