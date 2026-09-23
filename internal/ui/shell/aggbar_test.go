package shell

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// What is selected, added up (FR-3.15).

// adding opens a table with the bar shown.
func adding(t *testing.T, host string) (*fixture, *tab, *aggBar) {
	t.Helper()
	fx := newFixture(t)
	c := fx.create(t, host, nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	pump(t, fx.q, func() bool { return tb.model.Resident(0) })

	fx.s.toggleAggregates()
	if tb.agg == nil {
		t.Fatal("the bar was not shown")
	}
	return fx, tb, tb.agg
}

// The figures follow the selection the moment it changes, without asking
// the server anything.
func TestTheFiguresFollowTheSelection(t *testing.T) {
	_, tb, a := adding(t, "db1")
	before := browsesSoFar()

	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 3, Col: 0})
	said := a.said.Text
	for _, want := range []string{"id:", "4 cells", "sum 6", "average 1.5", "smallest 0", "largest 3"} {
		if !strings.Contains(said, want) {
			t.Errorf("the bar says %q, without %q", said, want)
		}
	}
	if got := browsesSoFar(); got != before {
		t.Errorf("%d browses to add up what is on the screen, want the %d there were", got, before)
	}
}

// Nothing selected is nothing to add up.
func TestNothingSelectedAddsUpToNothing(t *testing.T) {
	_, _, a := adding(t, "db1")
	if !strings.Contains(a.said.Text, "Nothing selected") {
		t.Errorf("the bar says %q", a.said.Text)
	}
	if !a.whole.Disabled() {
		t.Error("the whole column was offered with no column chosen")
	}
}

// Only the cells that are selected are added up. A selection is not always
// a rectangle: a cell added with the keyboard is one cell, not a column.
func TestOnlyTheSelectedCellsAreAddedUp(t *testing.T) {
	_, tb, a := adding(t, "db1")
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 3, Col: 0})
	// One more cell, in the other column, four rows down.
	tb.grid.ToggleCell(grid.CellID{Row: 7, Col: 1})

	said := a.said.Text
	if !strings.Contains(said, "5 cells") {
		t.Errorf("the bar says %q, want the four and the one", said)
	}
	// The four ids are 0, 1, 2 and 3; the fifth cell is a word.
	if !strings.Contains(said, "sum 6") {
		t.Errorf("the bar says %q, want the numbers among them", said)
	}
}

// A column that is not numbers is counted and not totalled.
func TestAColumnThatIsNotNumbersIsOnlyCounted(t *testing.T) {
	_, tb, a := adding(t, "db1")
	tb.grid.Select(grid.CellID{Row: 0, Col: 1}, grid.CellID{Row: 2, Col: 1})
	said := a.said.Text
	if !strings.Contains(said, "3 cells") {
		t.Errorf("the bar says %q", said)
	}
	if strings.Contains(said, "sum") {
		t.Errorf("the bar says %q about a column of words", said)
	}
}

// A selection reaching rows nobody has read says how many it looked at: a
// total over half a column is not the total.
//
// And it looks at them without asking the server, and without walking to
// the end of a table two thousand million rows long: a bar that followed a
// selection at that cost would not follow anything.
func TestASelectionBeyondWhatHasBeenReadSaysSo(t *testing.T) {
	_, tb, a := adding(t, "db1")
	start := time.Now()
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: grid.End, Col: 0})
	took := time.Since(start)

	if !strings.Contains(a.said.Text, "read so far") {
		t.Errorf("the bar says %q", a.said.Text)
	}
	if !strings.Contains(a.said.Text, "rows") {
		t.Errorf("the bar says %q, without how many it looked at", a.said.Text)
	}
	if took > 2*time.Second {
		t.Errorf("following the selection took %v", took)
	}
}

// heldRows is a grid's rows, which says which of them it was asked for.
type heldRows struct {
	rows  map[int64]model.Row
	asked []int64
}

func (h *heldRows) Resident(i int64) bool { _, ok := h.rows[i]; return ok }

func (h *heldRows) Row(_ context.Context, i int64) (model.Row, bool) {
	h.asked = append(h.asked, i)
	r, ok := h.rows[i]
	return r, ok
}

// A bar asks for the rows a grid has and for no others: asking for one it
// does not have would queue a fetch for every row of a selection somebody
// dragged to the end of a large table.
func TestOnlyTheRowsAGridHasAreAskedFor(t *testing.T) {
	_, tb, _ := adding(t, "db1")
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 9, Col: 0})

	h := &heldRows{rows: map[int64]model.Row{0: {int64(1)}, 2: {int64(3)}}}
	values, read, all := selectedValues(h, tb.grid, tb.grid.Selection())
	if len(h.asked) != 2 {
		t.Errorf("it asked for rows %v, want only the two the grid has", h.asked)
	}
	if read != 2 || len(values) != 2 {
		t.Errorf("it read %d rows and %d values", read, len(values))
	}
	if all {
		t.Error("it called a selection whole when eight of its rows were not there")
	}
}

// The whole column is the server's own answer, and says it is.
func TestTheWholeColumnIsTheServersAnswer(t *testing.T) {
	fx, tb, a := adding(t, "db1")
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 3, Col: 0})
	if a.whole.Disabled() {
		t.Fatal("the whole column was not offered")
	}
	a.measureWhole()
	pump(t, fx.q, func() bool { return strings.Contains(a.said.Text, "whole column") })

	said := a.said.Text
	for _, want := range []string{"id, whole column", "250 rows", "sum", "average", "the server took"} {
		if !strings.Contains(said, want) {
			t.Errorf("the bar says %q, without %q", said, want)
		}
	}
}

// A connection that will not measure does not offer the whole column, and
// the selection's own figures are there as ever.
func TestAConnectionThatWillNotMeasureStillAddsUp(t *testing.T) {
	_, tb, a := adding(t, "nostats")
	tb.grid.Select(grid.CellID{Row: 0, Col: 0}, grid.CellID{Row: 3, Col: 0})
	if !a.whole.Disabled() {
		t.Error("a connection that will not measure offered the whole column")
	}
	if !strings.Contains(a.said.Text, "sum 6") {
		t.Errorf("the bar says %q", a.said.Text)
	}
}

// Showing the bar and taking it away leaves the grid where it was.
func TestTheBarIsShownAndTakenAway(t *testing.T) {
	fx, tb, _ := adding(t, "db1")
	if !fx.s.checked(cmdAggregates) {
		t.Error("the menu does not say the bar is shown")
	}
	fx.s.toggleAggregates()
	if tb.agg != nil {
		t.Error("the bar is still there")
	}
	if fx.s.checked(cmdAggregates) {
		t.Error("the menu says the bar is shown")
	}
	if len(tb.center.Objects) != 1 || tb.center.Objects[0] != tb.body {
		t.Error("the grid was not put back where it was")
	}
}

// Nothing is offered where there is no grid.
func TestAddingUpNeedsAGrid(t *testing.T) {
	fx := newFixture(t)
	if fx.s.canAggregate() {
		t.Error("it was offered with nothing open")
	}
	if !fx.s.menuItems[cmdAggregates].Disabled {
		t.Error("Add Up the Selection should be disabled with nothing open")
	}
	fx.s.toggleAggregates() // and does nothing

	// A query tab is not a grid either: its results are, and they have
	// their own.
	openQuery(t, fx, "")
	fx.s.sync()
	if fx.s.canAggregate() {
		t.Error("it was offered on a query tab")
	}
	if !fx.s.menuItems[cmdAggregates].Disabled {
		t.Error("Add Up the Selection is offered on a query tab")
	}
}

// What the bar says, for every shape of answer it can be given.
func TestWhatTheBarSays(t *testing.T) {
	def := model.ColumnDef{Name: "total", Type: model.DataType{Class: model.TypeInteger}}
	whole := describeWhole(&source.ColumnStats{Rows: 100, Nulls: 10, Min: int64(1), Max: int64(9),
		Mean: 5, HasMean: true, Sum: 450, HasSum: true}, def)
	for _, want := range []string{"total, whole column", "100 rows", "90 values", "sum 450",
		"average 5", "smallest 1", "largest 9"} {
		if !strings.Contains(whole, want) {
			t.Errorf("it says %q, without %q", whole, want)
		}
	}
	// A column with nothing missing does not say how many values there are:
	// that is the row count again.
	full := describeWhole(&source.ColumnStats{Rows: 100}, def)
	if strings.Contains(full, "values") {
		t.Errorf("it says %q about a column with nothing missing", full)
	}
	if strings.Contains(full, "sum") {
		t.Errorf("it says %q about a column with no total", full)
	}
}

// A figure is written for reading rather than for arithmetic.
func TestHowAFigureIsWritten(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{{0, "0"}, {1500, "1,500"}, {-3, "-3"}, {2.5, "2.5"}, {1.0 / 3, "0.3333"}} {
		if got := number(tc.in); got != tc.want {
			t.Errorf("%v is written %q, want %q", tc.in, got, tc.want)
		}
	}
}
