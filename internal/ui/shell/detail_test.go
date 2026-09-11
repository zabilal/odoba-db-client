package shell

import (
	"reflect"
	"strings"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// detailOn opens the detail panel of an items tab whose rows are referred
// to, on a row, and waits for its rows.
func detailOn(t *testing.T, row int) (*fixture, *tab, *detailPanel) {
	t.Helper()
	fx, tb := openFK(t)
	pump(t, fx.q, func() bool { return len(tb.referrers) == 3 })
	tb.grid.Select(grid.CellID{Row: row, Col: 1}, grid.CellID{Row: row, Col: 1})
	if !fx.s.canShowDetail() {
		t.Fatal("items are referred to, so they have detail rows")
	}
	fx.s.run(cmdDetail)
	d := tb.detail
	if d == nil || !d.isShown() || tb.center.Objects[0] != fyne.CanvasObject(d.split) {
		t.Fatal("the panel is under the grid")
	}
	pump(t, fx.q, func() bool { return d.grid != nil })
	return fx, tb, d
}

func TestTheRowsThatReferToARowAreShownUnderIt(t *testing.T) {
	fx, tb, d := detailOn(t, 2)
	byName := func(v string) []source.Filter {
		return []source.Filter{{Column: "name", Op: source.OpEqual, Values: []any{v}}}
	}
	pump(t, fx.q, func() bool {
		return reflect.DeepEqual(lastFilters(), byName("item 2")) && strings.HasSuffix(d.status.Text, " of notes refer to this row")
	})
	tb.grid.Select(grid.CellID{Row: 2, Col: 1}, grid.CellID{Row: 2, Col: 1}) // the same row again
	if strings.HasPrefix(d.status.Text, "Reading") {
		t.Error("the same row is not read again")
	}
	g := d.grid
	row3, _ := tb.model.Row(tb.ctx, 3)
	if err := tb.grid.OnEdit(row3, 1, "renamed"); err != nil { // other rows refer to what is written
		t.Fatal(err)
	}
	tb.grid.Select(grid.CellID{Row: 3, Col: 1}, grid.CellID{Row: 3, Col: 1})
	pump(t, fx.q, func() bool { return reflect.DeepEqual(lastFilters(), byName("item 3")) })
	if d.grid != g {
		t.Error("another row of the same table is read into the same grid")
	}
	d.pick.SetSelected("tags, by id")
	byID := []source.Filter{{Column: "id", Op: source.OpEqual, Values: []any{int64(3)}}}
	pump(t, fx.q, func() bool { return reflect.DeepEqual(lastFilters(), byID) && d.grid != g })
	fx.s.run(cmdFormView) // the form takes the grid's place, and the panel stays
	if !d.isShown() || tb.forms[tb.grid] == nil || !tb.forms[tb.grid].shown() {
		t.Error("the form view and the panel are shown together")
	}
	fx.s.run(cmdDetail)
	if d.isShown() || tb.center.Objects[0] != fyne.CanvasObject(tb.body) {
		t.Error("toggled again, the panel goes and the grid's place has the room")
	}
}

func TestNothingRefersToANewRowInTheDetail(t *testing.T) {
	fx, tb, d := detailOn(t, 2)
	fx.s.run(cmdInsertRow) // the new row is selected
	if !strings.HasPrefix(d.status.Text, "Nothing refers to this row") || len(d.place.Objects) != 0 || !d.open.Disabled() {
		t.Errorf("a row not yet written has nothing referring to it: %q", d.status.Text)
	}
	if err := tb.grid.OnEditAdded(0, 1, "fresh"); err != nil {
		t.Fatal(err)
	}
	tb.grid.Select(grid.CellID{Row: 0, Col: 1}, grid.CellID{Row: 0, Col: 1})
	if !strings.HasPrefix(d.status.Text, "Nothing refers to this row") {
		t.Errorf("whatever a new row is given: %q", d.status.Text)
	}
	fx.s.run(cmdDeleteRows) // the new row goes; row 0 is selected again
	d.pick.SetSelected("widgets, by ghost")
	if !strings.HasPrefix(d.status.Text, "Nothing refers to this row") {
		t.Errorf("a key the row has no column for refers to nothing: %q", d.status.Text)
	}
}

func TestOpenInTabOpensTheRowsShownInTheDetail(t *testing.T) {
	fx, _, d := detailOn(t, 2)
	pump(t, fx.q, func() bool { return !d.open.Disabled() })
	d.open.OnTapped()
	notes := tabOn(fx, "notes")
	if notes == nil || fx.s.activeTab() != notes {
		t.Fatal("the rows open in a tab of their own, in front")
	}
	want := []source.Filter{{Column: "name", Op: source.OpEqual, Values: []any{"item 2"}}}
	pump(t, fx.q, func() bool { return notes.grid != nil && reflect.DeepEqual(notes.browse.Options().Filters, want) })
}

func TestATableNothingRefersToHasNoDetail(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 1, Col: 1}, grid.CellID{Row: 1, Col: 1})
	if fx.s.canShowDetail() {
		t.Error("a table nothing refers to has no detail rows to show")
	}
}
