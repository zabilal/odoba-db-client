package shell

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

var otherNode = model.Node{Ref: model.NewRef(model.KindTable, "main", "other"), Label: "other", Browsable: true}

func tabNamed(s *Shell, label string) *tab {
	for _, t := range s.open {
		if t.item.Text == label {
			return t
		}
	}
	return nil
}

func tabLabels(s *Shell) []string {
	var out []string
	for _, it := range s.tabs.Items {
		out = append(out, it.Text)
	}
	return out
}

func TestTheWindowComesBackAsItWasLeft(t *testing.T) {
	fx := newFixture(t)
	fx.s.autosave = time.Hour // so that only quitting saves it
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	fx.s.OpenObject(c.ID, otherNode)
	fx.s.OpenQuery(c.ID)
	pump(t, fx.q, func() bool { return tabNamed(fx.s, "other") != nil && tabNamed(fx.s, "other").browse != nil })
	fx.s.tabs.Select(tabNamed(fx.s, "other").item)
	fx.s.togglePin()
	fx.s.tabs.Select(tabNamed(fx.s, "items").item)
	fx.s.split.SetOffset(0.31)
	fx.s.win.Resize(fyne.NewSize(1000, 700))
	if got := tabLabels(fx.s); !slices.Equal(got, []string{"other", "items", "Query 1"}) {
		t.Fatalf("before quitting the tabs are %v", got)
	}
	fx.s.shutdown()

	s := fx.relaunch(t)
	if got := tabLabels(s); !slices.Equal(got, []string{"other", "items", "Query 1"}) {
		t.Errorf("tabs came back as %v", got)
	}
	if o := tabNamed(s, "other"); o == nil || !o.pinned {
		t.Error("the pinned tab should come back pinned")
	}
	if a := s.activeTab(); a == nil || a.item.Text != "items" {
		got := "none"
		if a != nil {
			got = a.item.Text
		}
		t.Errorf("the selected tab should come back selected, got %s", got)
	}
	if s.split.Offset != 0.31 {
		t.Errorf("sidebar offset %v", s.split.Offset)
	}
	if sz := s.win.Canvas().Size(); sz.Width != 1000 || sz.Height != 700 {
		t.Errorf("window %v", sz)
	}
	pump(t, fx.q, func() bool { return tabNamed(s, "items").browse != nil })
}

// viewedItems opens the items table filtered by name, sorted by id
// descending and narrowed by a WHERE clause.
func viewedItems(t *testing.T) (*fixture, *tab) {
	t.Helper()
	fx, tb := openItems(t)
	tb.grid.SetFilterText(1, "item 1")
	tb.grid.ApplyFilters()
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Filters) == 1 })
	fx.s.resort(tb, []grid.SortKey{{Column: 0, Descending: true}})
	pump(t, fx.q, func() bool { return len(tb.browse.Options().Sorts) == 1 })
	fx.s.toggleWhere()
	tb.where.entry.SetText("id > 3")
	tb.where.apply()
	pump(t, fx.q, func() bool { return tb.browse.Options().Where == "id > 3" })
	return fx, tb
}

func checkViewBack(t *testing.T, fx *fixture, s *Shell) {
	t.Helper()
	pump(t, fx.q, func() bool {
		r := tabNamed(s, "items")
		if r == nil || r.browse == nil {
			return false
		}
		o := r.browse.Options()
		return o.Where == "id > 3" && len(o.Sorts) == 1 && len(o.Filters) == 1
	})
	r := tabNamed(s, "items")
	if o := r.browse.Options(); o.Sorts[0].Column != "id" || !o.Sorts[0].Descending || o.Filters[0].Column != "name" {
		t.Errorf("rows are in %+v", o)
	}
	if got := r.grid.FilterTexts(); len(got) < 2 || got[1] != "item 1" {
		t.Errorf("filter row shows %q", got)
	}
	if got := r.grid.Sorts(); !slices.Equal(got, []grid.SortKey{{Column: 0, Descending: true}}) {
		t.Errorf("header shows sort %v", got)
	}
	if r.where == nil || !r.where.shown() || r.where.entry.Text != "id > 3" {
		t.Error("an active WHERE clause should come back shown, not quietly applied")
	}
}

func TestFiltersSortAndWhereComeBackAfterQuitting(t *testing.T) {
	fx, _ := viewedItems(t)
	fx.s.autosave = time.Hour
	fx.s.shutdown()
	checkViewBack(t, fx, fx.relaunch(t))
}

func TestTheSessionIsKeptWithoutQuitting(t *testing.T) {
	fx, _ := viewedItems(t)
	pump(t, fx.q, func() bool {
		ss, ok, _ := fx.hist.Session(context.Background())
		return ok && len(ss.Tabs) == 1 && ss.Tabs[0].Where == "id > 3" && len(ss.Tabs[0].Filters) == 1
	})
	checkViewBack(t, fx, fx.relaunch(t)) // no shutdown first: the app died
}

func TestWhatNoLongerFitsIsSaidNotDropped(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.hist.PutSession(context.Background(), localdb.Session{Active: 0, Tabs: []localdb.SessionTab{{
		Kind: localdb.SessionObject, ConnectionID: c.ID, RefKind: string(model.KindTable),
		RefPath: []string{"main", "items"}, Label: "items",
		Filters: map[string]string{"name": "item", "nick": "x", "id": ">abc"},
		Sorts:   []localdb.SessionSort{{Column: "gone"}},
	}}})
	s := fx.relaunch(t)
	pump(t, fx.q, func() bool {
		r := tabNamed(s, "items")
		return r != nil && r.browse != nil && len(r.browse.Options().Filters) == 1
	})
	for _, want := range []string{"nick, a column it no longer has", "sort on gone", "filter on id"} {
		if !strings.Contains(s.errors.text, want) {
			t.Errorf("error band %q should mention %q", s.errors.text, want)
		}
	}
}

func TestAnObjectOnADeletedConnectionStaysClosed(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.hist.PutSession(context.Background(), localdb.Session{Active: 1, Tabs: []localdb.SessionTab{
		{Kind: localdb.SessionObject, ConnectionID: "gone", RefKind: string(model.KindTable), RefPath: []string{"main", "x"}, Label: "x"},
		{Kind: localdb.SessionObject, ConnectionID: c.ID, RefKind: string(model.KindTable), RefPath: []string{"main", "items"}, Label: "items"},
	}})
	s := fx.relaunch(t)
	if got := tabLabels(s); !slices.Equal(got, []string{"items"}) {
		t.Errorf("tabs %v", got)
	}
	if s.errors.text != "" {
		t.Errorf("nothing unsaved was lost, so nothing to say: %q", s.errors.text)
	}
}

func TestACleanSavedQueryReopensClean(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	test.Type(q.editor.Focusable(), "rows 1;")
	p := fx.s.promptSave(tb, false)
	p.name.SetText("Tidy")
	p.dlg.Submit()
	pump(t, fx.q, func() bool { return q.saved.ID != "" && !q.dirty })
	fx.s.shutdown()

	s := fx.relaunch(t)
	r := tabNamed(s, "Tidy")
	if r == nil || r.query.dirty || r.query.editor.Document().Text() != "rows 1;" || r.query.saved.ID != q.saved.ID {
		t.Fatalf("tabs %v", tabLabels(s))
	}
	pump(t, fx.q, func() bool { return r.query.session != nil })
}

func TestQuittingBeforeATabShowsKeepsItsView(t *testing.T) {
	fx, _ := viewedItems(t)
	fx.s.shutdown()
	s := fx.relaunch(t)
	s.shutdown() // before the reopened tab's rows arrive, so its view is still to be shown
	ss, ok, _ := fx.hist.Session(context.Background())
	if !ok || len(ss.Tabs) != 1 || ss.Tabs[0].Where != "id > 3" || ss.Tabs[0].Filters["name"] != "item 1" ||
		len(ss.Tabs[0].Sorts) != 1 {
		t.Errorf("session after a quick quit: %+v", ss)
	}
}
