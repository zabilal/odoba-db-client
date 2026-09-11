package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func showWhere(t *testing.T, fx *fixture, tb *tab) *whereBar {
	t.Helper()
	c, _ := fx.s.reg.Get(cmdWhere)
	if !c.Enabled() {
		t.Fatal("a table from a source with a query language can take a WHERE clause")
	}
	fx.s.toggleWhere()
	if tb.where == nil || !tb.where.shown() {
		t.Fatal("the WHERE bar did not show")
	}
	return tb.where
}

func TestTheWhereBarIsHiddenUntilAskedFor(t *testing.T) {
	fx, tb := openItems(t)
	if len(tb.top.Objects) != 0 {
		t.Error("the WHERE bar should be hidden until asked for")
	}
	b := showWhere(t, fx, tb)
	if !strings.Contains(b.sql.Text, `SELECT * FROM "items"`) {
		t.Errorf("the statement shown is %q", b.sql.Text)
	}
	if fx.s.win.Canvas().Focused() != b.entry {
		t.Error("the WHERE field should take the focus")
	}
	b.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if b.shown() {
		t.Error("Escape should hide the bar")
	}
}

func TestAWhereClauseBrowsesAgainAndShowsTheStatement(t *testing.T) {
	fx, tb := openItems(t)
	b := showWhere(t, fx, tb)
	test.Type(b.entry, "id > 5")
	b.entry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	pump(t, fx.q, func() bool { return tb.browse.Options().Where == "id > 5" })
	if !strings.Contains(b.sql.Text, "WHERE (id > 5)") {
		t.Errorf("the statement shown is %q", b.sql.Text)
	}
	b.copySQL()
	if got := fx.s.app.Clipboard().Content(); got != b.sql.Text {
		t.Errorf("Copy put %q on the clipboard, not the statement", got)
	}
	pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, "filtered") })
	tb.grid.ToggleSort(0, false)
	tb.grid.SetFilterText(1, "item")
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool {
		o := tb.browse.Options()
		return o.Where == "id > 5" && len(o.Filters) == 1 && len(o.Sorts) == 1
	})
	b.clear()
	pump(t, fx.q, func() bool { return tb.browse.Options().Where == "" })
}

func TestAWhereTheServerRefusesIsExplainedAndKept(t *testing.T) {
	fx, tb := openItems(t)
	b := showWhere(t, fx, tb)
	b.entry.SetText("boom")
	b.apply()
	pump(t, fx.q, func() bool { return b.notes.Visible() })
	if !strings.Contains(b.note.Text, "syntax error") {
		t.Errorf("the note says %q", b.note.Text)
	}
	if tb.browse.Options().Where != "" {
		t.Error("a refused WHERE must leave the rows as they were")
	}
	if b.entry.Text != "boom" {
		t.Error("the typed text should stay, to be corrected")
	}
	b.entry.SetText("id > 1")
	b.apply()
	pump(t, fx.q, func() bool { return tb.browse.Options().Where == "id > 1" })
	if b.notes.Visible() {
		t.Error("a WHERE that works should clear the note")
	}
}

// The first screenshot of the bar had its statement drawn over the grid.
func TestTheWhereBarGetsItsRoomAboveTheGrid(t *testing.T) {
	fx, tb := openItems(t)
	b := showWhere(t, fx, tb)
	b.entry.SetText("id > 5\nAND id < 9")
	b.apply()
	pump(t, fx.q, func() bool { return tb.browse.Options().Where != "" })
	if got, need := b.box.Size().Height, b.box.MinSize().Height; got < need {
		t.Fatalf("the bar has %v of height and needs %v", got, need)
	}
	d := fyne.CurrentApp().Driver()
	bottom := d.AbsolutePositionForObject(b.box).Y + b.box.Size().Height
	if top := d.AbsolutePositionForObject(tb.grid.View()).Y; bottom > top+0.5 {
		t.Errorf("the bar ends at %v, below the top of the grid at %v", bottom, top)
	}
}
