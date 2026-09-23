package shell

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/chart"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// What a column holds (FR-3.14).

// measured opens a table and measures one of its columns.
func measured(t *testing.T, host string, col int) (*fixture, *tab, *statsPanel) {
	t.Helper()
	fx := newFixture(t)
	c := fx.create(t, host, nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })

	tb.grid.Select(grid.CellID{Row: 0, Col: col}, grid.CellID{Row: 0, Col: col})
	fx.s.sync()
	p := fx.s.showColumnStats(tb, col, fyne.NewPos(10, 10))
	if p == nil {
		t.Fatal("nothing was measured")
	}
	pump(t, fx.q, func() bool { return !strings.Contains(p.status.Text, "Measuring") || len(p.body.Objects) > 1 })
	return fx, tb, p
}

// said is every line of the panel.
func (p *statsPanel) said() string {
	var out []string
	for _, o := range p.body.Objects {
		out = append(out, labelsIn(o)...)
	}
	return strings.Join(out, " | ")
}

// A column of numbers has every figure there is, and a picture of how its
// values are spread.
func TestAColumnOfNumbersIsMeasured(t *testing.T) {
	_, _, p := measured(t, "db1", 0)
	said := p.said()
	for _, want := range []string{"Rows", "250", "Empty", "Different values", "Smallest", "Largest", "Average"} {
		if !strings.Contains(said, want) {
			t.Errorf("the panel says %q, without %q", said, want)
		}
	}
	if !strings.Contains(said, "The server took") {
		t.Errorf("the panel says %q, without how long it took", said)
	}
	if spreads(p.body) == 0 {
		t.Error("a column of numbers has no picture of how its values are spread")
	}
}

// A column that is not numbers is measured and not drawn: a histogram of
// text is a histogram of nothing.
func TestAColumnThatIsNotNumbersIsNotDrawn(t *testing.T) {
	_, _, p := measured(t, "db1", 1)
	if !strings.Contains(p.said(), "Rows") {
		t.Errorf("the panel says %q", p.said())
	}
	if strings.Contains(p.said(), "Average") {
		t.Errorf("a column of text was averaged: %q", p.said())
	}
	if spreads(p.body) != 0 {
		t.Error("a column of text was drawn")
	}
}

// A column that is not numbers is not sampled either: reading ten thousand
// values to draw nothing is a query nobody asked for.
func TestAColumnThatIsNotNumbersIsNotSampled(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })

	before := browsesSoFar()
	p := fx.s.showColumnStats(tb, 1, fyne.NewPos(10, 10))
	pump(t, fx.q, func() bool { return strings.Contains(p.said(), "Rows") })
	if got := browsesSoFar(); got != before {
		t.Errorf("%d browses to measure a column of text, want the %d there were", got, before)
	}
}

// A picture of no values is no picture.
func TestAPictureOfNoValues(t *testing.T) {
	_, _, p := measured(t, "db1", 0)
	if _, ok := p.spread(nil); ok {
		t.Error("a picture was drawn of no values at all")
	}
	if _, ok := p.spread([]model.Row{{nil}, {nil}}); ok {
		t.Error("a picture was drawn of a sample with no numbers in it")
	}
	if _, ok := p.spread([]model.Row{{int64(1)}, {int64(2)}}); !ok {
		t.Error("a sample of numbers was not drawn")
	}
}

// What is written on each line of the panel, and what is left off it.
func TestWhatEachLineOfThePanelSays(t *testing.T) {
	def := model.ColumnDef{Name: "c", Type: model.DataType{Class: model.TypeInteger}}
	said := func(st *source.ColumnStats) string {
		var out []string
		for _, f := range figuresOfColumn(st, def) {
			out = append(out, f.name+"="+f.value)
		}
		return strings.Join(out, " ")
	}
	// A figure nobody measured is not shown.
	if got := said(&source.ColumnStats{Rows: 10, Distinct: -1}); strings.Contains(got, "Different") {
		t.Errorf("it says %q about a column whose values were not counted", got)
	}
	if got := said(&source.ColumnStats{Rows: 10, Distinct: 4}); !strings.Contains(got, "Different values=4") {
		t.Errorf("it says %q", got)
	}
	// A column with nothing missing says so in words rather than in noughts.
	if got := said(&source.ColumnStats{Rows: 10}); !strings.Contains(got, "Empty=none") {
		t.Errorf("it says %q", got)
	}
	// A column of no rows is neither full nor empty.
	if got := said(&source.ColumnStats{}); strings.Contains(got, "Empty") {
		t.Errorf("it says %q about a column of no rows", got)
	}
}

// A proportion of no rows at all is none, not a number nobody can read.
func TestAProportionOfNothing(t *testing.T) {
	for _, tc := range []struct {
		part, whole int64
		want        string
	}{{0, 0, "0%"}, {5, 0, "0%"}, {1, 4, "25.0%"}, {1, 3, "33.3%"}} {
		if got := percent(tc.part, tc.whole); got != tc.want {
			t.Errorf("%d of %d is %q, want %q", tc.part, tc.whole, got, tc.want)
		}
	}
}

// How many rows hold nothing is said as a count and as a proportion, which
// is the figure somebody is actually after.
func TestHowMuchOfAColumnIsEmpty(t *testing.T) {
	_, _, p := measured(t, "db1", 0)
	if !strings.Contains(p.said(), "50 (20.0%)") {
		t.Errorf("the panel says %q", p.said())
	}
}

// The figures are about the rows the grid's filters select.
func TestTheFiguresFollowTheGridsFilters(t *testing.T) {
	fx, tb, _ := measured(t, "db1", 0)
	tb.grid.SetFilterText(0, "=1")
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) > 0 })

	p := fx.s.showColumnStats(tb, 0, fyne.NewPos(10, 10))
	pump(t, fx.q, func() bool { return strings.Contains(p.said(), "Rows") })
	if !strings.Contains(p.said(), "| 10 |") {
		t.Errorf("the panel says %q, want the figures for the rows that are left", p.said())
	}
}

// A reading that arrives after a later one is dropped: somebody clicking
// down a row of headers asks several times over, and the panel should say
// what they asked for last.
func TestAnOlderReadingDoesNotLandOnTopOfANewerOne(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })

	statsGate = make(chan struct{})
	t.Cleanup(func() { statsGate = nil })
	statsAsked.Store(0)

	// The first reading is of the whole table, and waits.
	p := fx.s.showColumnStats(tb, 0, fyne.NewPos(10, 10))
	pump(t, fx.q, func() bool { return statsAsked.Load() == 1 })

	// The second is of a narrowed grid, and answers at once.
	tb.grid.SetFilterText(0, "=1")
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) > 0 })
	p.measure()
	pump(t, fx.q, func() bool { return strings.Contains(p.said(), "| 10 |") })

	// Now the first one arrives. It is about a grid nobody is looking at,
	// and the window is given every chance to show it.
	close(statsGate)
	for i := 0; i < 40; i++ {
		fx.q.Flush()
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(p.said(), "| 10 |") {
		t.Errorf("the panel says %q, want what was asked for last", p.said())
	}
}

// A connection that will not measure offers nothing.
func TestAConnectionThatWillNotMeasureAColumn(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "nostats", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 0, Col: 0})
	fx.s.sync()

	if fx.s.canMeasureColumn() {
		t.Error("a connection that will not measure offered to")
	}
	if !fx.s.menuItems[cmdColumnStats].Disabled {
		t.Error("Measure This Column is offered on a connection that will not")
	}
	if fx.s.showColumnStats(tb, 0, fyne.NewPos(10, 10)) != nil {
		t.Error("a panel was opened on a connection that will not measure")
	}
}

// Nothing is offered with no column selected.
func TestMeasuringNeedsAColumn(t *testing.T) {
	fx := newFixture(t)
	if fx.s.canMeasureColumn() {
		t.Error("a column was offered with nothing open")
	}
	if !fx.s.menuItems[cmdColumnStats].Disabled {
		t.Error("Measure This Column should be disabled with nothing open")
	}

	// A table open with nothing chosen in it has no column to measure.
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	fx.s.sync()
	if fx.s.canMeasureColumn() {
		t.Error("a column was offered with none chosen")
	}
	if !fx.s.menuItems[cmdColumnStats].Disabled {
		t.Error("Measure This Column is offered with no column chosen")
	}
}

// A measuring that fails says so rather than showing an empty panel.
func TestAMeasuringThatFails(t *testing.T) {
	statsFails.Store(true)
	t.Cleanup(func() { statsFails.Store(false) })
	_, _, p := measured(t, "db1", 0)
	if !strings.Contains(p.status.Text, "Could not measure") {
		t.Errorf("it said %q", p.status.Text)
	}
}

// What proportion of a column holds something, for anybody who needs the
// number rather than the words.
func TestWhatProportionHoldsSomething(t *testing.T) {
	for _, tc := range []struct {
		rows, nulls int64
		want        float64
	}{{100, 0, 1}, {100, 100, 0}, {100, 25, 0.75}, {0, 0, 0}} {
		got := app.FilledOf(&source.ColumnStats{Rows: tc.rows, Nulls: tc.nulls})
		if got != tc.want {
			t.Errorf("%d rows with %d empty is %v, want %v", tc.rows, tc.nulls, got, tc.want)
		}
	}
	if got := app.FilledOf(nil); got != 0 {
		t.Errorf("nothing measured is %v", got)
	}
}

// spreads counts the pictures of how a column's values are spread.
func spreads(o fyne.CanvasObject) int {
	switch v := o.(type) {
	case *chart.Widget:
		return 1
	case *fyne.Container:
		n := 0
		for _, c := range v.Objects {
			n += spreads(c)
		}
		return n
	}
	return 0
}
