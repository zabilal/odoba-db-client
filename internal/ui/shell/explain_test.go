package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
)

// tabText is everything a tab's body says, for a tab showing a message
// rather than a tree.
func tabText(t *tab) string {
	var out []string
	for _, o := range t.body.Objects {
		out = append(out, labelsIn(o)...)
	}
	return strings.Join(out, " | ")
}

// The way into a query plan (FR-5.13).

// planned writes a statement into a query tab and asks for its plan.
func planned(t *testing.T, analyze bool) (*fixture, *tab, *planPanel) {
	t.Helper()
	fx := newFixture(t)
	qt, q := openQuery(t, fx, "")
	q.editor.Document().SetText("items where id > 1")

	tb := fx.s.OpenPlan(qt, q.editor.Document().Text(), analyze, false)
	pump(t, fx.q, func() bool { return tb.plan != nil })
	return fx, tb, tb.plan
}

// labelsIn is every label a container holds, however deeply.
func labelsIn(o fyne.CanvasObject) []string {
	switch v := o.(type) {
	case *widget.Label:
		return []string{v.Text}
	case *fyne.Container:
		var out []string
		for _, c := range v.Objects {
			out = append(out, labelsIn(c)...)
		}
		return out
	case *widget.Accordion:
		return nil
	}
	return nil
}

func (p *planPanel) said() string {
	var out []string
	for _, o := range p.body.Objects {
		out = append(out, labelsIn(o)...)
	}
	return strings.Join(out, " | ")
}

// bars are the heat blocks the tree draws.
func barsIn(o fyne.CanvasObject) []*fcanvas.Rectangle {
	switch v := o.(type) {
	case *fcanvas.Rectangle:
		return []*fcanvas.Rectangle{v}
	case *fyne.Container:
		var out []*fcanvas.Rectangle
		for _, c := range v.Objects {
			out = append(out, barsIn(c)...)
		}
		return out
	}
	return nil
}

// fills is the coloured part of each row's bar: how much of the work that
// step did, drawn as a length.
func (p *planPanel) fills() []float32 {
	var out []float32
	for _, o := range p.body.Objects {
		rs := barsIn(o)
		if len(rs) < 2 {
			continue
		}
		out = append(out, rs[0].MinSize().Width)
	}
	return out
}

// A plan opens as a tree of steps, indented as it nests.
func TestAStatementOpensAsAPlan(t *testing.T) {
	_, tb, p := planned(t, false)
	if !strings.HasPrefix(tb.item.Text, "Plan: ") {
		t.Errorf("the tab is called %q", tb.item.Text)
	}
	said := p.said()
	for _, want := range []string{"Aggregate", "Seq Scan on items"} {
		if !strings.Contains(said, want) {
			t.Errorf("the plan says %q; %s is not in it", said, want)
		}
	}
	if !strings.Contains(said, "Filter: (id > 1)") {
		t.Errorf("the plan says %q; the step's own words are not in it", said)
	}
	// The scan sits under the aggregate, which indentation says.
	if !strings.Contains(said, "    Seq Scan on items") {
		t.Errorf("the plan says %q; the scan is not indented under what runs it", said)
	}
	if len(p.fills()) != len(p.reading.Rows) {
		t.Errorf("%d bars for %d steps", len(p.fills()), len(p.reading.Rows))
	}
}

// The bar is a length, not only a colour: how much of the work a step did is
// something to compare down the column at a glance.
func TestTheBarIsAsLongAsTheShareItStandsFor(t *testing.T) {
	_, _, p := planned(t, true)
	fills := p.fills()
	if len(fills) != 2 {
		t.Fatalf("%d bars", len(fills))
	}
	// Nine tenths of the work, so nine times the bar.
	if fills[1] <= fills[0]*4 {
		t.Errorf("the bars are %v and %v for shares %v and %v",
			fills[0], fills[1], p.reading.Rows[0].Share, p.reading.Rows[1].Share)
	}
	for i, w := range fills {
		if w > heatWidth {
			t.Errorf("bar %d is %v, wider than the track it is in", i, w)
		}
		if w < heatMin {
			t.Errorf("bar %d is %v, too small to see for a step that did work", i, w)
		}
	}
	// The bar and what is left of its track are the track, so every row's
	// bar begins and ends in the same place down the column.
	for i, o := range p.body.Objects {
		rs := barsIn(o)
		if len(rs) < 2 {
			t.Fatalf("row %d has %d parts to its bar", i, len(rs))
		}
		if got := rs[0].MinSize().Width + rs[1].MinSize().Width; got != heatWidth {
			t.Errorf("row %d's bar and track are %v wide, want %v", i, got, float32(heatWidth))
		}
	}
}

// A plan with no figures has no bars at all, rather than bars standing for
// nothing.
func TestAPlanWithNoFiguresDrawsNoBars(t *testing.T) {
	_, _, p := planned(t, false)
	p.reading = app.PlanReading{By: app.ByNothing, Hottest: -1, Rows: p.reading.Rows}
	p.draw()
	for i, w := range p.fills() {
		if w != 0 {
			t.Errorf("bar %d is %v long in a plan that measured nothing", i, w)
		}
	}
}

// The footer says how many steps there are and what the heat means, because
// a bar that stood for a guess and a bar that stood for a measurement would
// otherwise look the same.
func TestThePlanSaysWhatItsHeatMeans(t *testing.T) {
	_, tb, _ := planned(t, false)
	if !strings.Contains(tb.footer.Text, "2 steps") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	if !strings.Contains(tb.footer.Text, "Estimated") {
		t.Errorf("the footer says %q; an unmeasured plan is an estimate", tb.footer.Text)
	}
	if strings.Contains(tb.footer.Text, "share of that time") {
		t.Errorf("the footer says %q about a plan that was never run", tb.footer.Text)
	}
}

// Measuring runs the statement, and then the shares are of time taken.
func TestAMeasuredPlanSaysWhatItTook(t *testing.T) {
	_, tb, p := planned(t, true)
	if p.reading.By != app.ByTime {
		t.Fatalf("the plan is measured by %v", p.reading.By)
	}
	if !strings.Contains(tb.footer.Text, "Measured") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	if !strings.Contains(p.said(), "9,000 rows (10 expected)") {
		t.Errorf("the plan says %q; what a step returned and what was expected are both worth having",
			p.said())
	}
	// The worst estimate is named, because that is where a plan goes wrong.
	if !strings.Contains(tb.footer.Text, "Seq Scan on items expected 10 rows and returned 9000") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// The step that did the most work is what a plan is opened to find, so it is
// emboldened as well as coloured: colour alone is not a difference everybody
// can see.
func TestTheHottestStepIsMarkedWithoutRelyingOnColour(t *testing.T) {
	_, _, p := planned(t, true)
	if p.reading.Hottest < 0 {
		t.Fatal("nothing was marked as hottest")
	}
	bold := 0
	for _, o := range p.body.Objects {
		boldIn(o, &bold)
	}
	if bold != 1 {
		t.Errorf("%d steps are emboldened, want the hottest alone", bold)
	}
	// And the share is written as a number beside the bar.
	if !strings.Contains(p.said(), "%") {
		t.Errorf("the plan says %q; no share is written", p.said())
	}
}

func boldIn(o fyne.CanvasObject, n *int) {
	switch v := o.(type) {
	case *widget.Label:
		if v.TextStyle.Bold {
			*n++
		}
	case *fyne.Container:
		for _, c := range v.Objects {
			boldIn(c, n)
		}
	}
}

// The server's own words are kept, so somebody can read what it actually
// said rather than only this program's rendering of it.
func TestTheServersOwnWordsAreKept(t *testing.T) {
	_, _, p := planned(t, false)
	if p.raw.Text != `{"Plan": "as the server said"}` {
		t.Errorf("the raw plan is %q", p.raw.Text)
	}
}

// Asking again replaces the plan in the same tab rather than opening
// another: two tabs of one statement would be two things to compare that are
// the same thing.
func TestAskingAgainStaysInOneTab(t *testing.T) {
	fx, tb, p := planned(t, false)
	before := len(fx.s.open)
	again := fx.s.OpenPlan(tb, p.sql, false, false)
	pump(t, fx.q, func() bool { return tb.plan != nil && tb.plan != p })
	if again != tb {
		t.Error("asking again opened a second tab")
	}
	if len(fx.s.open) != before {
		t.Errorf("%d tabs open, %d before", len(fx.s.open), before)
	}
}

// Only a connection that will say how it runs a statement offers to.
func TestPlanningNeedsAConnectionThatWill(t *testing.T) {
	fx := newFixture(t)
	if !fx.s.menuItems[cmdExplain].Disabled {
		t.Error("Explain should be disabled with nothing open")
	}
	_, q := openQuery(t, fx, "")
	q.editor.Document().SetText("items where id > 1")
	fx.s.sync()
	if fx.s.menuItems[cmdExplain].Disabled {
		t.Error("a query can be planned")
	}
	if fx.s.menuItems[cmdExplainMeasure].Disabled {
		t.Error("this connection measures, so the command is offered")
	}
}

// An empty editor has no statement to plan.
func TestAnEmptyQueryHasNothingToPlan(t *testing.T) {
	fx := newFixture(t)
	openQuery(t, fx, "")
	fx.s.sync()
	if !fx.s.menuItems[cmdExplain].Disabled {
		t.Error("an empty editor was offered a plan")
	}
}

// A connection that plans but will not measure offers only the one.
func TestAConnectionThatWillNotMeasure(t *testing.T) {
	fx := newFixture(t)
	_, q := openQueryOn(t, fx, "", "noanalyse")
	q.editor.Document().SetText("items where id > 1")
	fx.s.sync()
	if fx.s.menuItems[cmdExplain].Disabled {
		t.Error("it plans, so the command is offered")
	}
	if !fx.s.menuItems[cmdExplainMeasure].Disabled {
		t.Error("it will not measure, so that command is not offered")
	}
}

// A statement the server will not plan says so rather than showing an empty
// tree.
func TestAStatementTheServerWillNotPlan(t *testing.T) {
	fx := newFixture(t)
	qt, _ := openQuery(t, fx, "")
	tb := fx.s.OpenPlan(qt, "unplannable", false, false)
	pump(t, fx.q, func() bool { return strings.Contains(tabText(tb), "could not read the plan") })
	if tb.plan != nil {
		t.Error("a tree was drawn for a plan that was never had")
	}
}

// Measuring runs the statement, so a read-only connection refuses it and
// says why.
func TestMeasuringOnAReadOnlyConnection(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	live, err := fx.ws.Connect(fx.s.ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = live
	qt, _ := openQuery(t, fx, "")
	_ = qt
	if _, err := app.Explain(fx.s.ctx,
		fakeSource{guard: source.Guard{ReadOnly: true}},
		source.Statement{SQL: "update items set n = 1"}, true); err == nil {
		t.Error("a read-only connection measured a write, which runs it")
	}
}

// What is planned is what is selected, or else the statement the caret is
// in: a whole script would be a plan of something nobody asked about.
func TestWhatIsPlannedIsWhatIsPointedAt(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	doc := q.editor.Document()
	doc.SetText("items one;\nitems two;\n")

	// The caret is left at the end, which is inside the second statement:
	// that one alone, not the script it is in.
	_, sql, ok := fx.s.explainTarget()
	if !ok {
		t.Fatal("nothing to plan")
	}
	if sql != "items two;" {
		t.Errorf("it would plan %q, want the statement the caret is in", sql)
	}
	// A selection is what is planned where there is one, whatever statement
	// the caret is in: somebody who highlighted something meant that.
	doc.SetCaret(editor.Pos{}, false)
	doc.SetCaret(editor.Pos{Line: 0, Col: 5}, true)
	_, sql, ok = fx.s.explainTarget()
	if !ok {
		t.Fatal("nothing to plan")
	}
	if sql != "items" {
		t.Errorf("it would plan %q, want what is selected", sql)
	}
}
