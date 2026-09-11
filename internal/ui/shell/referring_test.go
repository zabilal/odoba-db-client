package shell

import (
	"reflect"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// radioIn is the first radio group in o.
func radioIn(o fyne.CanvasObject) *widget.RadioGroup {
	switch v := o.(type) {
	case *widget.RadioGroup:
		return v
	case *widget.PopUp:
		return radioIn(v.Content)
	case *fyne.Container:
		for _, c := range v.Objects {
			if r := radioIn(c); r != nil {
				return r
			}
		}
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(v).Objects() {
			if r := radioIn(c); r != nil {
				return r
			}
		}
	}
	return nil
}

func TestShowReferringRowsOpensTheOneTableThatRefers(t *testing.T) {
	fx, tb := openFK(t)
	tb.grid.Select(grid.CellID{Row: 2, Col: 1}, grid.CellID{Row: 2, Col: 1})
	fx.s.run(cmdGoToReferenced)
	parts := tabOn(fx, "parts")
	pump(t, fx.q, func() bool { return parts.grid != nil && len(parts.referrers) == 1 })
	parts.grid.Select(grid.CellID{Row: 4, Col: 1}, grid.CellID{Row: 4, Col: 1})
	if !fx.s.canShowReferring() {
		t.Fatal("items refer to parts by name")
	}
	fx.s.run(cmdShowReferring)
	if fx.s.activeTab() != tb || len(fx.s.open) != 2 {
		t.Fatal("the items tab, open, is brought forward")
	}
	want := []source.Filter{{Column: "name", Op: source.OpEqual, Values: []any{"item 4"}}}
	pump(t, fx.q, func() bool { return reflect.DeepEqual(tb.browse.Options().Filters, want) })
}

func TestShowReferringRowsAsksWhichWhenSeveralRefer(t *testing.T) {
	fx, tb := openFK(t)
	pump(t, fx.q, func() bool { return len(tb.referrers) == 3 })
	row1, _ := tb.model.Row(tb.ctx, 1)
	if err := tb.grid.OnEdit(row1, 1, "renamed"); err != nil { // other rows refer to what is written
		t.Fatal(err)
	}
	tb.grid.Select(grid.CellID{Row: 1, Col: 1}, grid.CellID{Row: 1, Col: 1})
	fx.s.run(cmdShowReferring)
	tapOnTop(t, fx, "Cancel")
	if len(fx.s.open) != 1 {
		t.Fatal("Cancel opens nothing")
	}
	fx.s.run(cmdShowReferring)
	top := fx.s.win.Canvas().Overlays().Top()
	if text := labelText(top); !strings.Contains(text, "Show Referring Rows") {
		t.Fatalf("it asks which: %q", text)
	}
	pick := radioIn(top)
	if pick == nil || !reflect.DeepEqual(pick.Options, []string{"notes, by name", "tags, by id"}) {
		t.Fatalf("each table, by its key's columns: %v", pick)
	}
	tapOnTop(t, fx, "Show Rows") // the first, notes, as it opens
	notes := tabOn(fx, "notes")
	byName := []source.Filter{{Column: "name", Op: source.OpEqual, Values: []any{"item 1"}}}
	pump(t, fx.q, func() bool {
		return notes != nil && notes.grid != nil && reflect.DeepEqual(notes.browse.Options().Filters, byName)
	})
	fx.s.selectTab(tb)
	fx.s.run(cmdShowReferring)
	radioIn(fx.s.win.Canvas().Overlays().Top()).SetSelected("tags, by id")
	tapOnTop(t, fx, "Show Rows")
	tags := tabOn(fx, "tags")
	if tags == nil || fx.s.activeTab() != tags {
		t.Fatal("the table picked opens in front")
	}
	want := []source.Filter{{Column: "id", Op: source.OpEqual, Values: []any{int64(1)}}}
	pump(t, fx.q, func() bool { return tags.grid != nil && reflect.DeepEqual(tags.browse.Options().Filters, want) })
}

func TestNothingRefersToANewRowOrAPlainTable(t *testing.T) {
	fx, tb := openFK(t)
	pump(t, fx.q, func() bool { return len(tb.referrers) == 3 })
	fx.s.run(cmdInsertRow)
	if err := tb.grid.OnEditAdded(0, 1, "fresh"); err != nil {
		t.Fatal(err)
	}
	if fx.s.canShowReferring() {
		t.Error("nothing refers to a row not yet written, whatever it is given")
	}
	fx2, tb2 := loadedItems(t) // a source that lists no referrers
	tb2.grid.Select(grid.CellID{Row: 1, Col: 1}, grid.CellID{Row: 1, Col: 1})
	if fx2.s.canShowReferring() || tb2.referrers != nil {
		t.Error("a table nothing refers to offers nothing")
	}
}
