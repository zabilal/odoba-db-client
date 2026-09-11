package shell

import (
	"slices"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// openLabelled opens the items table on a source whose items' ids refer to
// owners, labelled by name, and whose names refer to parts, which have no
// other text to label a row with.
func openLabelled(t *testing.T) (*fixture, *tab) {
	t.Helper()
	fx := newFixture(t)
	c := fx.create(t, "labels", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool {
		if tb.model == nil || tb.table == nil {
			return false
		}
		_, ok := tb.model.Row(tb.ctx, 3)
		return ok
	})
	return fx, tb
}

// labelBrowses counts the browses that read labels: of two columns, one
// filter IN a list of values.
func labelBrowses() int {
	browses.Lock()
	defer browses.Unlock()
	n := 0
	for _, o := range browses.opts {
		if len(o.Filters) == 1 && o.Filters[0].Op == source.OpIn && len(o.Columns) == 2 {
			n++
		}
	}
	return n
}

func TestAKeysValueIsShownWithTheLabelOfItsRow(t *testing.T) {
	fx, tb := openLabelled(t)
	pump(t, fx.q, func() bool {
		if tb.grid.Labels == nil {
			return false
		}
		l, ok := tb.grid.Labels(0, int64(3))
		return ok && l == "item 3"
	})
	var read *source.BrowseOptions
	browses.Lock()
	for _, o := range browses.opts {
		if slices.Equal(o.Columns, []string{"id", "name"}) {
			read = &o
		}
	}
	browses.Unlock()
	if read == nil || len(read.Filters) != 1 || read.Filters[0].Column != "id" || read.Filters[0].Op != source.OpIn ||
		!slices.Contains(read.Filters[0].Values, any(int64(3))) {
		t.Errorf("labels are read with the key and label columns alone, for the values shown: %+v", read)
	}
	if _, ok := tb.grid.Labels(1, "item 3"); ok {
		t.Error("parts have no text but their key, so a name is shown alone")
	}
	if _, ok := tb.grid.Labels(0, nil); ok {
		t.Error("NULL refers to nothing")
	}
	asked := labelBrowses()
	fx.s.reload(tb) // the same pages again
	pump(t, fx.q, func() bool { _, ok := tb.model.Row(tb.ctx, 3); return ok })
	if labelBrowses() != asked {
		t.Error("a value's label is read once")
	}
}

func TestATableWithoutKeysHasNoLabels(t *testing.T) {
	_, tb := loadedItems(t)
	if tb.grid.Labels != nil || !tb.labels.empty() {
		t.Error("a table with no foreign keys asks nothing for its cells")
	}
}
