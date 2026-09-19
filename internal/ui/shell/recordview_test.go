package shell

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/ui/cellview"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// Reading one record whole (T2.66, FR-13.8, ADR-0098).

// theRecord shows the record at a row, and returns the view showing it.
func theRecord(t *testing.T, fx *fixture, tb *tab, row int) *recordView {
	t.Helper()
	tb.grid.Select(grid.CellID{Row: row, Col: 0}, grid.CellID{Row: row, Col: 0})
	fx.s.toggleRecord()
	r := tb.records[tb.grid]
	if r == nil {
		t.Fatal("showing a record made no record view")
	}
	pump(t, fx.q, func() bool { return !strings.HasSuffix(r.where.Text, "Reading…") })
	return r
}

func TestARecordIsNamedByWhereItSits(t *testing.T) {
	fx, tb := openRecords(t)
	r := theRecord(t, fx, tb, 0)

	// A record has no name of its own: a log and a place in it is the whole
	// of its identity.
	if got := r.where.Text; got != "Partition 0, offset 10" {
		t.Errorf("the record is named %q", got)
	}
	if !r.shown() {
		t.Error("the record was not put in the grid's place")
	}

	// And going back gives the grid its place again.
	fx.s.toggleRecord()
	if r.shown() {
		t.Error("the grid never got its place back")
	}
}

func TestARecordIsOfferedInTheFormsItsBytesAdmit(t *testing.T) {
	fx, tb := openRecords(t)
	r := theRecord(t, fx, tb, 0)

	// A value that parses is offered as JSON first: somebody opening a record
	// wants to see what it says.
	if got := r.value.pick.Options; len(got) != 3 || got[0] != cellview.FormJSON {
		t.Errorf("a JSON value is offered as %v", got)
	}
	if got := r.value.pick.Selected; got != cellview.FormJSON {
		t.Errorf("a JSON value opens as %q", got)
	}
	// Its key is text and not JSON, so JSON is not among its forms.
	if got := r.key.pick.Options; len(got) != 2 || got[0] != cellview.FormText {
		t.Errorf("a text key is offered as %v", got)
	}
}

func TestTheFormChosenIsKeptWhileItStillApplies(t *testing.T) {
	fx, tb := openRecords(t)
	r := theRecord(t, fx, tb, 0)

	// Reading down a topic in hex should stay in hex — starting, as every
	// record does, at the form nearest to what it says.
	if got := r.value.pick.Selected; got != cellview.FormJSON {
		t.Fatalf("a JSON value opened as %q, so what follows would prove nothing", got)
	}
	r.value.pick.SetSelected(cellview.FormHex)
	r.move(1)
	pump(t, fx.q, func() bool { return strings.HasSuffix(r.where.Text, "offset 11") })
	if got := r.value.pick.Selected; got != cellview.FormHex {
		t.Errorf("after moving on, the value is shown as %q", got)
	}

	// The next value is not text at all, so hex is all it admits — and that
	// is what it falls back to rather than showing nothing.
	r.move(1)
	pump(t, fx.q, func() bool { return strings.HasSuffix(r.where.Text, "offset 12") })
	if got := r.value.pick.Options; len(got) != 1 || got[0] != cellview.FormHex {
		t.Errorf("bytes that are not text are offered as %v", got)
	}

	// And a form the next record cannot be read in falls back rather than
	// showing nothing: text chosen here, then a record that is not text at
	// all, where keeping the choice would point past the forms there are.
	r.move(-2)
	pump(t, fx.q, func() bool { return strings.HasSuffix(r.where.Text, "offset 10") })
	r.value.pick.SetSelected(cellview.FormText)
	r.move(2)
	pump(t, fx.q, func() bool { return strings.HasSuffix(r.where.Text, "offset 12") })
	if got := r.value.pick.Selected; got != cellview.FormHex {
		t.Errorf("a value that cannot be read as text is shown as %q", got)
	}
}

func TestARecordWithNoKeySaysSo(t *testing.T) {
	fx, tb := openRecords(t)
	r := theRecord(t, fx, tb, 2)

	// Sending no key and sending an empty one are different things, and a
	// person reading the record is owed the difference.
	if got := r.key.text.Text; got != "No key" {
		t.Errorf("a record with no key shows %q", got)
	}
	if got := r.key.pick.Options; len(got) != 0 {
		t.Errorf("a record with no key offers the forms %v", got)
	}
	if got := r.key.meta.Text; got != "none" {
		t.Errorf("a record with no key says %q", got)
	}
}

func TestRecordViewIsOfferedForRecordsAndNothingElse(t *testing.T) {
	fx, _ := openRecords(t)
	if !fx.s.canShowRecord() {
		t.Error("a topic's records should be readable one at a time")
	}
	// A table's rows are rows: the form view already reads one of those down.
	fx2, _ := openItems(t)
	if fx2.s.canShowRecord() {
		t.Error("a table offered the record view")
	}
}

func TestARecordNamedWithoutItsPlaceIsStillNamed(t *testing.T) {
	// A source that gave something other than numbers for these would
	// otherwise be shown as "Partition <nil>".
	if got := recordWhere("x", int64(1)); got != "Record" {
		t.Errorf("a record with no place is named %q", got)
	}
}

// Filtering records where the broker will not (T2.67, FR-13.9, ADR-0099).

// filterTo types text into a column's filter cell and applies it.
func filterTo(t *testing.T, fx *fixture, tb *tab, col int, text string) {
	t.Helper()
	tb.grid.SetFilterText(col, text)
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { return !strings.HasSuffix(tb.footer.Text, "Filtering…") })
}

func TestATopicsRecordsAreFilteredHere(t *testing.T) {
	fx, tb := openRecords(t)

	// A broker filters nothing, so before this there was no filter row at all.
	if !tb.grid.Filterable() {
		t.Fatal("a topic's records have no filter row")
	}

	// The key column is column 3. One record carries order-1.
	filterTo(t, fx, tb, 3, "order-1")
	pump(t, fx.q, func() bool { n, _ := tb.model.Extent(); return n == 1 })
	row, ok := tb.model.Row(tb.ctx, 0)
	if !ok {
		t.Fatal("the matching record never loaded")
	}
	if got, _ := row[3].([]byte); string(got) != "order-1" {
		t.Errorf("the record shown has key %q", got)
	}

	// Nothing was asked of the source: the rows it had are the rows filtered.
	if got := len(tb.browse.Options().Filters); got != 0 {
		t.Errorf("%d filters were sent to a broker that refuses them", got)
	}

	// And the footer says what it filtered, which is not the whole topic.
	if got := tb.footer.Text; !strings.Contains(got, "filtering what has been read") {
		t.Errorf("the footer says %q", got)
	}

	// Clearing it gives every record back.
	filterTo(t, fx, tb, 3, "")
	pump(t, fx.q, func() bool { n, _ := tb.model.Extent(); return n == int64(len(fakeRecords)) })
	if tb.local != nil {
		t.Error("clearing the filter left one behind")
	}
}

func TestARecordIsFoundByAHeaderItCarries(t *testing.T) {
	fx, tb := openRecords(t)

	// Headers are column 5, and one record carries trace-id twice.
	filterTo(t, fx, tb, 5, "trace-id")
	pump(t, fx.q, func() bool { n, _ := tb.model.Extent(); return n == 1 })
	row, ok := tb.model.Row(tb.ctx, 0)
	if !ok {
		t.Fatal("the record with that header never loaded")
	}
	if got, _ := row[1].(int64); got != 10 {
		t.Errorf("the record found is at offset %v", got)
	}

	// A header that was never sent finds nothing, rather than everything.
	filterTo(t, fx, tb, 5, "no-such-header")
	pump(t, fx.q, func() bool { n, _ := tb.model.Extent(); return n == 0 })
}
