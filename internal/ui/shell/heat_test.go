package shell

import (
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// heating is a table open with a column in front.
func heating(t *testing.T, col int) (*fixture, *tab) {
	t.Helper()
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil && tb.grid != nil })
	tb.grid.Select(grid.CellID{Row: 0, Col: col}, grid.CellID{Row: 0, Col: col})
	fx.s.sync()
	return fx, tb
}

func TestAColumnOfNumbersIsShadedBetweenItsSmallestAndLargest(t *testing.T) {
	fx, tb := heating(t, 0)
	c, ok := fx.s.reg.Get(cmdHeatMap)
	if !ok || !c.Enabled() || !fx.s.canHeat() {
		t.Fatal("a column of numbers, on a source that measures, offers Heat Map")
	}
	if tb.grid.Heated(0) {
		t.Fatal("nothing is shaded until somebody asks")
	}
	fx.s.buildMenu()
	if fx.s.menuItems[cmdHeatMap] == nil {
		t.Error("Heat Map is not in the menus")
	}
	asked := statsAsked.Load()
	fx.s.run(cmdHeatMap)
	if !strings.Contains(tb.footer.Text, "Measuring id to shade it…") {
		t.Errorf("while it is measured, that is said: %q", tb.footer.Text)
	}
	pump(t, fx.q, func() bool { return tb.grid.Heated(0) })
	// The range is the server's, over every row, and not the page in hand.
	if !strings.Contains(tb.footer.Text, "id is shaded from 0 to 249") || statsAsked.Load() != asked+1 {
		t.Errorf("it says what it shaded between: %q, after %d measurings", tb.footer.Text, statsAsked.Load()-asked)
	}
	if lo, hi, _ := tb.grid.HeatRange(0); lo != 0 || hi != 249 {
		t.Errorf("shaded between %v and %v", lo, hi)
	}

	// Narrowed, it is measured again: the shading is of what is looked at.
	tb.grid.SetFilterText(1, "item 1")
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, "id is shaded from 0 to 9") })
	if lo, hi, ok := tb.grid.HeatRange(0); statsAsked.Load() != asked+2 || !ok || lo != 0 || hi != 9 {
		t.Errorf("%d measurings after narrowing, shaded between %v and %v", statsAsked.Load()-asked, lo, hi)
	}

	// Asked again, it is unshaded, and stays so when the rows change.
	fx.s.run(cmdHeatMap)
	if tb.grid.Heated(0) || len(tb.heated) != 0 || !strings.Contains(tb.footer.Text, "id is no longer shaded") {
		t.Errorf("unshaded, and not to be measured again: %q, %v", tb.footer.Text, tb.heated)
	}
	tb.grid.SetFilterText(1, "")
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { return !strings.Contains(tb.footer.Text, "filtered") })
	if tb.grid.Heated(0) || statsAsked.Load() != asked+2 {
		t.Error("a column unshaded was measured and shaded again")
	}
}

func TestWhatCannotBeShadedIsNot(t *testing.T) {
	// Text has no size.
	fx, tb := heating(t, 1)
	if c, _ := fx.s.reg.Get(cmdHeatMap); c.Enabled() || fx.s.canHeat() {
		t.Error("a column of text offers Heat Map")
	}
	fx.s.run(cmdHeatMap)
	fx.s.toggleHeat()
	if tb.grid.Heated(1) || len(tb.heated) != 0 {
		t.Error("a column of text was shaded")
	}

	// A column of one value has none larger than another, and is said so
	// rather than tinted one colour.
	fx, tb = heating(t, 0)
	statsOneValue.Store(true)
	t.Cleanup(func() { statsOneValue.Store(false) })
	fx.s.run(cmdHeatMap)
	pump(t, fx.q, func() bool {
		return strings.Contains(tb.footer.Text, "id holds no two different numbers here, so there is nothing to shade")
	})
	if tb.grid.Heated(0) {
		t.Error("a column of one value is shaded")
	}
	statsOneValue.Store(false)

	// A column that could not be measured is said, and is not left marked
	// to be tried again at every change.
	fx, tb = heating(t, 0)
	statsFails.Store(true)
	t.Cleanup(func() { statsFails.Store(false) })
	fx.s.run(cmdHeatMap)
	pump(t, fx.q, func() bool {
		return strings.Contains(tb.footer.Text, "Could not measure id: fakesql: the column could not be measured")
	})
	if tb.grid.Heated(0) || len(tb.heated) != 0 {
		t.Error("a column that could not be measured is shaded, or will be tried again")
	}
}

// An answer to a question nobody is asking any more does not land: the rows
// were narrowed while the whole table was being measured.
func TestAMeasuringThatWasOvertakenIsDropped(t *testing.T) {
	fx, tb := heating(t, 0)
	statsGate = make(chan struct{})
	t.Cleanup(func() { statsGate = nil })
	fx.s.run(cmdHeatMap) // of every row, which waits
	tb.grid.SetFilterText(1, "item 1")
	tb.grid.ApplyFilters() // of ten, which does not
	pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, "id is shaded from 0 to 9") })
	close(statsGate)
	// Given every chance to land, the first answer changes nothing.
	for range 50 {
		fx.q.Flush()
		time.Sleep(time.Millisecond)
	}
	if lo, hi, ok := tb.grid.HeatRange(0); !ok || lo != 0 || hi != 9 || !strings.Contains(tb.footer.Text, "from 0 to 9") {
		t.Errorf("shaded between %v and %v: %q", lo, hi, tb.footer.Text)
	}
}
