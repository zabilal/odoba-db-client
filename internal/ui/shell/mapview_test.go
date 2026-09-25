package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"github.com/ikigai-db/ikigai-db/internal/ui/geomap"
)

// The way onto a map (FR-11.5).
//
// The fake's places table holds a name, a pair of coordinates and a geometry, so
// that all three ways of holding a place are drawn from something a real browse
// produced rather than from rows a test handed the panel.

// mapped opens the places table, maps it, and waits for both.
func mapped(t *testing.T) (*fixture, *tab, *mapPanel) {
	t.Helper()
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, placesNode)
	rows := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return rows.browse != nil })

	tb := fx.s.OpenMap(rows)
	pump(t, fx.q, func() bool { return tb.geomap != nil })
	return fx, tb, tb.geomap
}

// A result with places in it maps itself, drawn rather than asking which of four
// columns was meant.
func TestAResultOpensAsAMap(t *testing.T) {
	_, tb, p := mapped(t)
	if tb.item.Text != "Map: places" {
		t.Errorf("the tab is called %q", tb.item.Text)
	}
	if len(p.rows) != fakeRows {
		t.Errorf("%d rows read, want %d", len(p.rows), fakeRows)
	}
	if p.w == nil {
		t.Fatal("nothing was drawn")
	}
	// The geometry column wins over the pair of numbers, because a type is not
	// a guess.
	if p.cols[p.src.Geometry].Name != "shape" {
		t.Errorf("it drew from %+v", p.src)
	}
	if len(p.built.Places) != fakeRows {
		t.Errorf("it read %d places for %d rows", len(p.built.Places), fakeRows)
	}
	said := tb.footer.Text
	for _, want := range []string{"places", "shape"} {
		if !strings.Contains(said, want) {
			t.Errorf("the footer says %q, which does not mention %q", said, want)
		}
	}
}

// Mapping is offered only where there is something to map: an affordance that
// opens an empty picture is worse than one that is not there (REQ-DB-2).
func TestMappingNeedsPlaces(t *testing.T) {
	fx := newFixture(t)
	if fx.s.canMap() {
		t.Error("it is offered with no tab open")
	}
	c := fx.create(t, "db1", nil)
	// The items table holds an id and a name, and nowhere at all.
	fx.s.OpenObject(c.ID, itemsNode)
	rows := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return rows.browse != nil })
	fx.s.sync()
	if fx.s.canMap() {
		t.Error("a table with no place in it was offered a map")
	}
	if !fx.s.menuItems[cmdMap].Disabled {
		t.Error("the menu item is enabled where the command is not")
	}
	// And the places table is.
	fx.s.OpenObject(c.ID, placesNode)
	places := fx.s.open[len(fx.s.open)-1]
	pump(t, fx.q, func() bool { return places.browse != nil })
	fx.s.sync()
	if !fx.s.canMap() {
		t.Error("a table of places was not offered a map")
	}
	if fx.s.menuItems[cmdMap].Disabled {
		t.Error("the menu item is disabled where the command is offered")
	}
}

// A place that is only in the values is found: a JSON column says nothing about
// what it holds, so the rows already read are looked at.
func TestAPlaceOnlyTheValuesKnowAboutIsFound(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, docsNode)
	docs := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return docs.browse != nil })
	// The rows have to have arrived: the question is about the values, and a
	// grid that has not read any cannot answer it.
	pump(t, fx.q, func() bool { _, ok := docs.model.Row(docs.ctx, 0); return ok })
	fx.s.sync()
	if !fx.s.canMap() {
		t.Fatal("a document with a place in it was not offered a map")
	}
	tb := fx.s.OpenMap(docs)
	pump(t, fx.q, func() bool { return tb.geomap != nil })
	p := tb.geomap
	if p.cols[p.src.Geometry].Name != "doc" {
		t.Errorf("it drew from %+v", p.src)
	}
	if len(p.built.Places) != fakeRows {
		t.Errorf("it read %d places for %d rows", len(p.built.Places), fakeRows)
	}
}

// Mapping the same rows twice is one tab, as charting them twice is.
func TestMappingTwiceIsOneTab(t *testing.T) {
	fx, tb, _ := mapped(t)
	before := len(fx.s.open)
	again := fx.s.OpenMap(fx.s.tabFor(strings.TrimPrefix(tb.key, "map:")))
	if again != tb || len(fx.s.open) != before {
		t.Errorf("it opened %d tabs", len(fx.s.open))
	}
}

// Which columns hold the places can be changed, and changing them redraws from
// the rows already read rather than asking the server again.
func TestTheColumnsAMapIsDrawnFromCanBeChanged(t *testing.T) {
	_, tb, p := mapped(t)
	browsed := browsesSoFar()
	p.lats.SetSelected("lat")
	// Straight away: choosing a latitude puts the geometry down, rather than
	// waiting for the longitude to do it.
	if p.src.Geometry != geomap.None {
		t.Errorf("the geometry column stayed chosen: %+v", p.src)
	}
	p.lons.SetSelected("lon")
	if p.src.Geometry != geomap.None {
		t.Errorf("the geometry column came back: %+v", p.src)
	}
	if p.cols[p.src.Lat].Name != "lat" || p.cols[p.src.Lon].Name != "lon" {
		t.Errorf("it drew from %+v", p.src)
	}
	if len(p.built.Places) != fakeRows {
		t.Errorf("it read %d places from the pair", len(p.built.Places))
	}
	if got := browsesSoFar(); got != browsed {
		t.Errorf("%d browses after changing the columns, want the %d before it", got, browsed)
	}
	said := tb.footer.Text
	if !strings.Contains(said, "lat") || !strings.Contains(said, "lon") {
		t.Errorf("the footer says %q, which does not name both columns", said)
	}
	// And choosing a geometry again puts the pair down: they are two answers to
	// the same question.
	p.geoms.SetSelected("shape")
	if p.src.Lat != geomap.None || p.src.Lon != geomap.None {
		t.Errorf("both are chosen: %+v", p.src)
	}
}

// Nothing to draw is said rather than drawn as an empty box.
func TestAMapOfNothingSaysSo(t *testing.T) {
	_, tb, p := mapped(t)
	p.geoms.SetSelected(nothingName)
	if p.src.Found() {
		t.Fatalf("it still has a place to draw from: %+v", p.src)
	}
	if !strings.Contains(tb.footer.Text, "nothing to draw") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// The pointer's position is read as a bearing, because a coordinate is read
// aloud as one: 51.5000°N, not 51.5.
func TestThePointerSaysWhereItIs(t *testing.T) {
	_, tb, p := mapped(t)
	// Laid out, because the frame a position is read against is worked out when
	// the widget is given a size: an unlaid map has no places on the screen.
	p.w.Resize(fyne.NewSize(600, 400))
	f := p.w.Frame()
	x, y := f.At(p.built.Places[0].Points[0].X, p.built.Places[0].Points[0].Y)
	// Through the widget, which is the path a pointer takes.
	p.w.MouseMoved(&desktop.MouseEvent{
		PointEvent: fyne.PointEvent{Position: fyne.NewPos(float32(x), float32(y))}})
	said := tb.footer.Text
	for _, want := range []string{"°N", "· row 1"} {
		if !strings.Contains(said, want) {
			t.Errorf("the footer says %q, which does not mention %q", said, want)
		}
	}
	// Away from everything there is still a coordinate and no row.
	p.hover(geomap.Reading{Row: -1, X: 10, Y: -20})
	said = tb.footer.Text
	if !strings.Contains(said, "°S") || !strings.Contains(said, "°E") {
		t.Errorf("the footer says %q", said)
	}
	if strings.Contains(said, "· row") {
		t.Errorf("the footer says %q away from every place", said)
	}
}

// A click goes to the row: the point of a map over a result is getting back to
// the rows it was drawn from (FR-11.4).
func TestClickingAPlaceGoesToItsRow(t *testing.T) {
	fx, tb, p := mapped(t)
	rows := fx.s.tabFor(strings.TrimPrefix(tb.key, "map:"))
	p.pick(geomap.Reading{Row: 2})
	if fx.s.activeTab() != rows {
		t.Error("it did not go to the rows")
	}
	// Which cell the grid then shows is the grid's own doing (GoTo); what this
	// asserts is that a click on the map lands in the rows.
	// Clicking nowhere says so rather than going somewhere.
	fx.s.selectTab(tb)
	p.pick(geomap.Reading{Row: -1})
	if fx.s.activeTab() != tb {
		t.Error("clicking nothing went somewhere")
	}
	if !strings.Contains(tb.footer.Text, "no place there") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// The rows a map was drawn from can be closed under it, and a click then says so
// rather than reaching into a tab that has gone.
func TestAMapOutlivingItsRows(t *testing.T) {
	fx, tb, p := mapped(t)
	rows := fx.s.tabFor(strings.TrimPrefix(tb.key, "map:"))
	fx.s.closeTab(rows.item)
	p.pick(geomap.Reading{Row: 1})
	if !strings.Contains(tb.footer.Text, "no longer open") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}
