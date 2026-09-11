package shell

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/store"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// onEnv makes a connection on host in env, read-only or not.
func onEnv(t *testing.T, fx *fixture, host, env string, readOnly bool) store.SavedConnection {
	t.Helper()
	c, err := fx.conns.Create(store.SavedConnection{Name: host, Driver: "postgres", Host: host, Environment: env, ReadOnly: readOnly}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// openOn opens the fake table on c and waits for it.
func openOn(t *testing.T, fx *fixture, c store.SavedConnection) *tab {
	t.Helper()
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	return tb
}

// drawn is every canvas text shown under o, as the tab bar draws a mark.
func drawn(o fyne.CanvasObject) []string { return drawnSkipping(o, nil) }

// barDrawn is what the tab bar draws of its tabs. It leaves out the content
// of the tab on show, whose band says PROD too.
func barDrawn(fx *fixture) []string {
	var skip fyne.CanvasObject
	if sel := fx.s.tabs.Selected(); sel != nil {
		skip = sel.Content
	}
	return drawnSkipping(fx.s.tabs, skip)
}

func drawnSkipping(o, skip fyne.CanvasObject) []string {
	if !o.Visible() || (skip != nil && o == skip) {
		return nil
	}
	var kids []fyne.CanvasObject
	switch v := o.(type) {
	case *canvas.Text:
		return []string{v.Text}
	case *fyne.Container:
		kids = v.Objects
	case fyne.Widget:
		kids = test.WidgetRenderer(v).Objects()
	}
	var out []string
	for _, c := range kids {
		out = append(out, drawnSkipping(c, skip)...)
	}
	return out
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestEveryProductionTabSaysSoOnItsTabAndAcrossItsContent(t *testing.T) {
	fx := newFixture(t)
	c := onEnv(t, fx, "db1", "production", false)
	prod := uitheme.New().Environment("production", false)
	tabs := []*tab{openOn(t, fx, c)}
	tabs = append(tabs, fx.s.OpenQuery(c.ID))
	fx.s.OpenStructure(c.ID, itemsNode)
	pump(t, fx.q, func() bool { return len(fx.s.open) == 3 })
	tabs = append(tabs, fx.s.open[2])
	for _, tb := range tabs {
		m := fx.s.tabMark(tb.item)
		if m == nil || m.Label != "PROD" || m.Fill != prod.Accent || m.Text != prod.OnAccent {
			t.Errorf("%q: mark %+v, want PROD in production's colours", tb.item.Text, m)
		}
		if !tb.band.Visible() || tb.band.text() != "Production: statements run here change live data." {
			t.Errorf("%q: band shown %v, saying %q", tb.item.Text, tb.band.Visible(), tb.band.text())
		}
		if got := drawn(tb.item.Content); !has(got, "PROD") {
			t.Errorf("%q: the tab's content draws %q, without the band's word", tb.item.Text, got)
		}
		r := test.WidgetRenderer(tb.band).(*envBandRenderer)
		if r.bg.FillColor != prod.Subtle || r.pill.FillColor != prod.Accent || r.word.Color != prod.OnAccent {
			t.Errorf("%q: the band is %v, its word %v on %v; want production's colours", tb.item.Text,
				r.bg.FillColor, r.word.Color, r.pill.FillColor)
		}
	}
	if got := barDrawn(fx); !has(got, "PROD") {
		t.Errorf("the tab bar draws %q, without the mark", got)
	}
}

func TestOnlyProductionHasABand(t *testing.T) {
	fx := newFixture(t)
	tb := openOn(t, fx, onEnv(t, fx, "db1", "dev", false))
	if m := fx.s.tabMark(tb.item); m == nil || m.Label != "DEV" {
		t.Errorf("a dev tab is marked %+v", m)
	}
	if tb.band.Visible() {
		t.Errorf("a dev tab has a band saying %q; if every environment shouts, none does", tb.band.text())
	}
	fx.s.closeTab(tb.item)
	tb = openOn(t, fx, onEnv(t, fx, "db2", "", false))
	if m := fx.s.tabMark(tb.item); m != nil || tb.band.Visible() {
		t.Errorf("a connection with no environment marks its tab %+v, band %v", m, tb.band.Visible())
	}
}

func TestAReadOnlyProductionTabSaysItIsReadOnly(t *testing.T) {
	fx := newFixture(t)
	tb := openOn(t, fx, onEnv(t, fx, "db1", "production", true))
	if got := tb.band.text(); got != "Production, opened read-only: statements that change data are refused." {
		t.Errorf("band says %q", got)
	}
}

func TestAnEnvironmentSavedLaterReachesATabStillOpen(t *testing.T) {
	fx := newFixture(t)
	c := onEnv(t, fx, "down", "", false) // the fake cannot reach it: the tab stays, unconnected
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return fx.q.Len() == 0 && tb.browse == nil })
	if tb.band.Visible() || has(barDrawn(fx), "PROD") {
		t.Fatal("marked before it was production")
	}
	c.Environment = "production"
	if err := fx.conns.Update(c, app.SecretEdit{}); err != nil {
		t.Fatal(err)
	}
	fx.s.connectionSaved(c.ID, true) // as the form does
	if fx.s.tabOf(tb.item) == nil {
		t.Fatal("the tab closed; it was not connected, so it should stay")
	}
	if got := barDrawn(fx); !tb.band.Visible() || !has(got, "PROD") {
		t.Errorf("band shown %v, tab bar draws %q; both should say production now", tb.band.Visible(), got)
	}
}

func TestTheMarkIsInTheAppearanceOnShow(t *testing.T) {
	fx := newFixture(t)
	tb := openOn(t, fx, onEnv(t, fx, "db1", "production", false))
	fx.s.d.Theme.Appearance = uitheme.AppearanceDark
	if m := fx.s.tabMark(tb.item); m.Fill != uitheme.New().Environment("production", true).Accent {
		t.Errorf("in dark, the mark is filled %v, want dark production's", m.Fill)
	}
}
