package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/chart"
)

// The way into a chart (FR-11.1, FR-11.2, FR-11.3).

// charted opens a table, charts it, and waits for both.
func charted(t *testing.T) (*fixture, *tab, *chartPanel) {
	t.Helper()
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	rows := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return rows.browse != nil })

	tb := fx.s.OpenChart(rows)
	pump(t, fx.q, func() bool { return tb.chart != nil })
	return fx, tb, tb.chart
}

// A result charts itself, drawn rather than asking which of seven kinds was
// meant.
func TestAResultOpensAsAChart(t *testing.T) {
	_, tb, p := charted(t)
	if tb.item.Text != "Chart: items" {
		t.Errorf("the tab is called %q", tb.item.Text)
	}
	if len(p.rows) != fakeRows {
		t.Errorf("%d rows read, want %d", len(p.rows), fakeRows)
	}
	if p.w == nil || p.w.Err() != nil {
		t.Fatalf("nothing was drawn: %v", p.w.Err())
	}
	if len(p.built.Series) == 0 {
		t.Error("nothing was read as a series")
	}
	if !strings.Contains(tb.footer.Text, "250 rows") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	if strings.Contains(tb.footer.Text, "The first") {
		t.Errorf("the footer says %q, but every row was read", tb.footer.Text)
	}
}

// A row whose value cannot be read as a number is left out, and how many is
// said: a chart drawn over gaps nobody was told about lies by omission.
func TestRowsThatCannotBeDrawnAreCountedAndSaid(t *testing.T) {
	_, tb, p := charted(t)
	// name holds text, so nothing in it is a number.
	p.roles.Y = []int{1}
	p.draw()
	if p.built.Skipped != fakeRows {
		t.Errorf("%d rows left out, want %d", p.built.Skipped, fakeRows)
	}
	if !strings.Contains(tb.footer.Text, "250 rows left out") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	if !strings.Contains(tb.footer.Text, "nothing to draw") {
		t.Errorf("the footer says %q, want it to say the chart is empty", tb.footer.Text)
	}
}

// A chart of the first hundred thousand rows of a million is not a chart of
// the million, and somebody reading it has to be told.
func TestAResultCutShortSaysSo(t *testing.T) {
	was := chartRows
	chartRows = 10
	t.Cleanup(func() { chartRows = was })

	_, tb, p := charted(t)
	if len(p.rows) != 10 {
		t.Fatalf("%d rows read, want the 10 asked for", len(p.rows))
	}
	if !p.short {
		t.Error("a result that had more rows than were read was not marked short")
	}
	if !strings.Contains(tb.footer.Text, "The first 10 rows") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// Nothing can be charted until something is open.
func TestChartingNeedsAResult(t *testing.T) {
	fx := newFixture(t)
	if !fx.s.menuItems[cmdChart].Disabled {
		t.Error("Chart should be disabled with nothing open")
	}
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool { return tb.browse != nil })
	if fx.s.menuItems[cmdChart].Disabled {
		t.Error("a table's rows can be charted")
	}
}

// The chart of a result is one tab: asking again brings it forward rather
// than reading the rows a second time.
func TestChartingTwiceIsOneTab(t *testing.T) {
	fx, tb, _ := charted(t)
	rows := fx.s.tabFor(strings.TrimPrefix(tb.key, "chart:"))
	if rows == nil {
		t.Fatal("the result the chart came from is gone")
	}
	if again := fx.s.OpenChart(rows); again != tb {
		t.Error("a second chart of one result opened a second tab")
	}
}

// Changing the kind redraws from the rows already read. A chart that asked
// the server again would be a chart of something else.
func TestChangingTheKindRedrawsWithoutReadingAgain(t *testing.T) {
	_, _, p := charted(t)
	browsed := browsesSoFar()
	p.kinds.SetSelected(chart.KindName(chart.Bar))
	if p.kind != chart.Bar {
		t.Errorf("the kind is %s, want a bar chart", p.kind)
	}
	if got := browsesSoFar(); got != browsed {
		t.Errorf("%d browses after changing the kind, want the %d before it", got, browsed)
	}
	if len(p.rows) != fakeRows {
		t.Errorf("%d rows after changing the kind, want the %d already read", len(p.rows), fakeRows)
	}
	if p.w.Chart().Kind != chart.Bar {
		t.Errorf("the widget is drawing %s", p.w.Chart().Kind)
	}
}

// Every guess can be changed, because it will be wrong sometimes.
func TestTheAxesCanBeChanged(t *testing.T) {
	_, _, p := charted(t)
	p.xs.SetSelected("id")
	if p.roles.X != 0 {
		t.Errorf("the axis is column %d, want id", p.roles.X)
	}
	if p.w.Chart().XTitle != "id" {
		t.Errorf("the axis is named %q", p.w.Chart().XTitle)
	}
	p.xs.SetSelected(rowNumberName)
	if p.roles.X != chart.RowNumber {
		t.Errorf("the axis is column %d, want the row number", p.roles.X)
	}
	p.splits.SetSelected("name")
	if p.roles.Series != 1 {
		t.Errorf("the split is column %d, want name", p.roles.Series)
	}
	p.splits.SetSelected(nothingName)
	if p.roles.Series != chart.RowNumber {
		t.Errorf("the split is column %d, want nothing", p.roles.Series)
	}
}

// Only the columns a chart can draw are offered as values, and all of them
// together is one of the choices.
func TestTheValuesOfferedAreTheOnesThatCanBeDrawn(t *testing.T) {
	_, _, p := charted(t)
	got := p.valueChoices()
	if got[0] != everyValue {
		t.Errorf("the first choice is %q, want %q", got[0], everyValue)
	}
	if len(got) != 2 || got[1] != "id" {
		t.Errorf("it offers %v, want the numeric column only", got)
	}
	p.ys.SetSelected("id")
	if len(p.roles.Y) != 1 || p.roles.Y[0] != 0 {
		t.Errorf("the values are %v, want id alone", p.roles.Y)
	}
	p.ys.SetSelected(everyValue)
	if len(p.roles.Y) == 0 {
		t.Error("choosing every column drew none")
	}
}

// Both formats, and the name says which.
func TestAChartIsExportedAsAPictureItsNameChooses(t *testing.T) {
	fx, tb, p := charted(t)
	for _, c := range []struct{ name, holds string }{
		{"items.png", "PNG"},
		{"items.svg", "<svg"},
	} {
		p.export()
		if len(fx.files.saves) == 0 {
			t.Fatal("no dialog was shown")
		}
		if got := fx.files.saves[len(fx.files.saves)-1].Name; got != "items.png" {
			t.Errorf("%s: it offers %q", c.name, got)
		}
		path := filepath.Join(t.TempDir(), c.name)
		fx.files.answer(path, nil)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !strings.Contains(string(data), c.holds) {
			t.Errorf("%s: it wrote %d bytes beginning %q", c.name, len(data), first(data, 8))
		}
		if !strings.Contains(tb.footer.Text, "Exported to "+c.name) {
			t.Errorf("%s: the footer says %q", c.name, tb.footer.Text)
		}
	}
}

// Cancelling writes nothing and reports nothing; a file that cannot be
// written is reported rather than swallowed.
func TestExportingAChartThatGoesWrong(t *testing.T) {
	fx, tb, p := charted(t)
	tb.footer.SetText("nothing said yet")
	p.export()
	fx.files.answer("", nil)
	if tb.footer.Text != "nothing said yet" {
		t.Errorf("cancelling said %q", tb.footer.Text)
	}
	if fx.s.errors.text != "" {
		t.Errorf("cancelling reported a failure: %q", fx.s.errors.text)
	}
	p.export()
	fx.files.answer(filepath.Join(t.TempDir(), "no-such-directory", "c.png"), nil)
	if !strings.Contains(fx.s.errors.text, "could not export the chart") {
		t.Errorf("it said %q", fx.s.errors.text)
	}
}

// A name a filesystem would read as a path does not become one.
func TestWhatAnExportedChartIsCalled(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"items", "items.png"},
		{"a/b", "a-b.png"},
		{`C:\thing`, "C--thing.png"},
	} {
		if got := chartFileName(c.in); got != c.want {
			t.Errorf("%q became %q, want %q", c.in, got, c.want)
		}
	}
}

// The chart is drawn in the window's colours, so that an exported file is
// the chart that was on the screen.
func TestAChartIsDrawnInTheWindowsColours(t *testing.T) {
	fx, _, p := charted(t)
	pal := fx.s.colours()
	c := p.w.Chart()
	if c.Axis != pal.Label || c.Grid != pal.Separator || c.Background != pal.ContentBackground {
		t.Errorf("the chart's colours are %v/%v/%v, want the window's", c.Axis, c.Grid, c.Background)
	}
	if len(c.Colours) == 0 {
		t.Error("the series were given no colours")
	}
}

// What a click on a chart does (FR-11.4).

// lastBrowse is what the fake was last asked for.
func lastBrowse(t *testing.T) source.BrowseOptions {
	t.Helper()
	browses.Lock()
	defer browses.Unlock()
	if len(browses.opts) == 0 {
		t.Fatal("the source was never browsed")
	}
	return browses.opts[len(browses.opts)-1]
}

// lined draws the fake's rows as a line against the id column, which is the
// shape a click can be traced back through.
func lined(t *testing.T) (*fixture, *tab, *chartPanel) {
	t.Helper()
	fx, tb, p := charted(t)
	p.kinds.SetSelected(chart.KindName(chart.Line))
	p.xs.SetSelected("id")
	return fx, tb, p
}

// A mark that stands for one row narrows the result to the value on the
// bottom axis at that point.
func TestClickingAMarkNarrowsTheResult(t *testing.T) {
	fx, tb, p := lined(t)
	rows := fx.s.tabFor(strings.TrimPrefix(tb.key, "chart:"))
	if rows == nil {
		t.Fatal("the result is gone")
	}
	browsed := browsesSoFar()
	p.pick(chart.Reading{Row: 3, X: 3, Y: 3})
	pump(t, fx.q, func() bool { return browsesSoFar() > browsed })

	if last := lastBrowse(t); len(last.Filters) != 1 || last.Filters[0].Column != "id" {
		t.Errorf("the rows were re-read with %+v, want one filter on id", last.Filters)
	}
	if got := rows.grid.FilterTexts()[0]; got != "=3" {
		t.Errorf("the filter row says %q, want =3", got)
	}
	if pk, ok := rows.picked[0]; !ok || len(pk.values) != 1 {
		t.Errorf("picked %+v, want the one value", rows.picked)
	}
	if fx.s.activeTab() != rows {
		t.Error("the result was not brought forward to be looked at")
	}
	if !strings.Contains(tb.footer.Text, "Narrowed id to =3") {
		t.Errorf("the chart says %q", tb.footer.Text)
	}
}

// The value comes from the row rather than from the chart, because a chart
// holds numbers and a filter has to hold what the source gave.
func TestWhatIsNarrowedToIsTheSourcesOwnValue(t *testing.T) {
	fx, tb, p := lined(t)
	rows := fx.s.tabFor(strings.TrimPrefix(tb.key, "chart:"))
	browsed := browsesSoFar()
	p.pick(chart.Reading{Row: 7, X: 7, Y: 7})
	pump(t, fx.q, func() bool { return browsesSoFar() > browsed })
	pk := rows.picked[0]
	if len(pk.values) != 1 {
		t.Fatalf("picked %+v", pk)
	}
	if v, ok := pk.values[0].(int64); !ok || v != 7 {
		t.Errorf("it filtered on %#v, want the int64 the source gave", pk.values[0])
	}
}

// Where nothing on the bottom axis names anything to filter by, a click
// brings the row itself forward instead.
func TestClickingWhereTheAxisIsTheRowNumberShowsTheRow(t *testing.T) {
	fx, tb, p := charted(t)
	p.kinds.SetSelected(chart.KindName(chart.Line))
	if p.roles.X != chart.RowNumber {
		t.Fatalf("the axis is column %d; this test needs the row number", p.roles.X)
	}
	rows := fx.s.tabFor(strings.TrimPrefix(tb.key, "chart:"))
	p.pick(chart.Reading{Row: 5, X: 5, Y: 5})
	if got := rows.grid.FilterTexts()[0]; got != "" {
		t.Errorf("the filter row says %q, want nothing filtered", got)
	}
	if fx.s.activeTab() != rows {
		t.Error("the result was not brought forward")
	}
}

// A histogram's bar counts an interval that is open at its top, which the
// filter row's range is not. It says so rather than narrowing to something
// near enough.
func TestClickingAHistogramBarSaysWhyItCannotNarrow(t *testing.T) {
	fx, tb, p := charted(t)
	if p.kind != chart.Histogram {
		t.Fatalf("the chart is a %s; this test needs a histogram", p.kind)
	}
	rows := fx.s.tabFor(strings.TrimPrefix(tb.key, "chart:"))
	p.pick(chart.Reading{Row: chart.NoRow, X: 0, Y: 12})
	if got := rows.grid.FilterTexts()[0]; got != "" {
		t.Errorf("the filter row says %q, want nothing filtered", got)
	}
	if !strings.Contains(tb.footer.Text, "up to the next one") {
		t.Errorf("the chart says %q", tb.footer.Text)
	}
}

// A mark that stands for several rows narrows to nothing, and says so.
func TestClickingSomethingThatStandsForManyRows(t *testing.T) {
	_, tb, p := lined(t)
	p.pick(chart.Reading{Row: chart.NoRow})
	if !strings.Contains(tb.footer.Text, "more than one row") {
		t.Errorf("the chart says %q", tb.footer.Text)
	}
}

// A chart outlives the result it was drawn from, and says so rather than
// falling over.
func TestClickingWhenTheResultIsClosed(t *testing.T) {
	fx, tb, p := lined(t)
	rows := fx.s.tabFor(strings.TrimPrefix(tb.key, "chart:"))
	fx.s.closeTab(rows.item)
	p.pick(chart.Reading{Row: 3, X: 3, Y: 3})
	if !strings.Contains(tb.footer.Text, "no longer open") {
		t.Errorf("the chart says %q", tb.footer.Text)
	}
}

// The widget hands a click straight to the panel, which is what makes the
// whole path work in the window.
func TestAClickOnTheChartReachesThePanel(t *testing.T) {
	_, _, p := lined(t)
	if p.w.OnPick == nil {
		t.Fatal("the widget has nowhere to hand a click")
	}
}
