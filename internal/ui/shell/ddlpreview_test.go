package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// previewed opens the designer, adds a column, and asks what would run.
func previewed(t *testing.T) (*fixture, *tab, *designPanel) {
	t.Helper()
	forgetStatements()
	fx, tb, p := designing(t)
	test.Tap(findButton(tb.body, "Add Column"))
	pump(t, fx.q, func() bool { return len(p.design.Columns()) == 3 })
	test.Tap(findButton(tb.body, "Preview…"))
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	return fx, tb, p
}

// Nothing structural runs without being read first (UX-6, FR-6.4).
func TestThePreviewShowsWhatWouldRunAndRunsNothingYet(t *testing.T) {
	fx, tb, _ := previewed(t)
	top := fx.s.win.Canvas().Overlays().Top()

	said := previewText(top)
	if !strings.Contains(said, "ADD COLUMN column_3") {
		t.Errorf("the preview does not show the statement: %q", said)
	}
	if !strings.Contains(said, "Nothing has run yet") {
		t.Errorf("the preview does not say nothing has run: %q", said)
	}
	if got := len(ranStatements()); got != 0 {
		t.Errorf("%d statements ran before anybody read them", got)
	}
	_ = tb
}

// Saying no runs nothing.
func TestSayingNoToThePreviewRunsNothing(t *testing.T) {
	fx, tb, _ := previewed(t)
	tapOnTop(t, fx, "Cancel")

	// Running happens in the background, so asking straight away would find
	// nothing whatever Cancel did. Opening the preview again is something
	// to wait for, and by the time it is up a stray run would have landed.
	test.Tap(findButton(tb.body, "Preview…"))
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	if got := len(ranStatements()); got != 0 {
		t.Errorf("%d statements ran after Cancel: %v", got, ranStatements())
	}
}

// And what runs is exactly what was read, in that order.
func TestWhatRunsIsWhatWasRead(t *testing.T) {
	fx, tb, _ := previewed(t)
	top := fx.s.win.Canvas().Overlays().Top()
	shown := previewText(top)

	tapOnTop(t, fx, "Run")
	pump(t, fx.q, func() bool { return len(ranStatements()) > 0 })

	ran := ranStatements()
	if len(ran) != 1 {
		t.Fatalf("%d statements ran: %v", len(ran), ran)
	}
	if !strings.Contains(shown, ran[0]) {
		t.Errorf("it ran %q, which was not in what was read: %q", ran[0], shown)
	}
	waitFooter(t, fx, tb, "1 statement ran.")
}

// The preview is offered only when there is something to run, so it is never
// a button that opens onto nothing.
func TestPreviewIsOfferedOnlyWhenThereIsSomethingToRun(t *testing.T) {
	_, tb, _ := designing(t)
	preview := findButton(tb.body, "Preview…")
	if preview == nil {
		t.Fatal("there is no Preview")
	}
	if !preview.Disabled() {
		t.Error("Preview is live on a design nobody has changed")
	}
	entriesIn(tb.body)[0].SetText("identifier")
	if preview.Disabled() {
		t.Error("Preview is out of reach on a design that has changed")
	}
}

// waitFooter waits for the footer to say something, because what it says
// arrives from a goroutine.
func waitFooter(t *testing.T, fx *fixture, tb *tab, want string) {
	t.Helper()
	pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, want) })
}

// previewText is everything the preview draws: its sentence, and the
// statements themselves, which are a code grid rather than labels.
func previewText(o fyne.CanvasObject) string {
	var parts []string
	parts = append(parts, labelTexts(o)...)
	for _, g := range textGridsIn(o) {
		parts = append(parts, g.Text())
	}
	return strings.Join(parts, " ")
}

func textGridsIn(o fyne.CanvasObject) []*widget.TextGrid {
	var out []*widget.TextGrid
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case nil:
			return
		case *widget.TextGrid:
			out = append(out, v)
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
		case fyne.Widget:
			for _, c := range test.WidgetRenderer(v).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	return out
}

// A structural change on a production connection is asked about before
// anything runs, and the guard refuses before the first statement reaches
// the server — so asking and then running is safe (FR-4.9, ADR-0113).
func TestChangingStructureOnProductionAsksFirst(t *testing.T) {
	forgetStatements()
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{
		Name: "live", Driver: "postgres", Host: "db1", Environment: "production"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tb := fx.s.OpenDesign(c.ID, itemsNode)
	pump(t, fx.q, func() bool { return tb.design != nil })

	test.Tap(findButton(tb.body, "Add Column"))
	pump(t, fx.q, func() bool { return len(tb.design.design.Columns()) == 3 })
	test.Tap(findButton(tb.body, "Preview…"))
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	tapOnTop(t, fx, "Run")

	// It asks, and nothing has run.
	pump(t, fx.q, func() bool {
		top := fx.s.win.Canvas().Overlays().Top()
		return top != nil && strings.Contains(strings.Join(labelTexts(top), " "), "marked Production")
	})
	if got := len(ranStatements()); got != 0 {
		t.Fatalf("%d statements ran before the question was answered", got)
	}

	// And it cannot be clicked past: the connection's name has to be typed.
	top := fx.s.win.Canvas().Overlays().Top()
	run := findButton(top, "Run")
	if run == nil || !run.Disabled() {
		t.Fatal("a production change could be confirmed with one click")
	}
	typeOnTop(t, fx, "live")
	test.Tap(run)
	pump(t, fx.q, func() bool { return len(ranStatements()) == 1 })
}

// A read-only connection refuses, and says so rather than asking: read-only
// is a decision about the connection, not a question about the change.
func TestChangingStructureOnAReadOnlyConnection(t *testing.T) {
	forgetStatements()
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{
		Name: "locked", Driver: "postgres", Host: "db1", ReadOnly: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tb := fx.s.OpenDesign(c.ID, itemsNode)
	pump(t, fx.q, func() bool { return tb.design != nil })

	test.Tap(findButton(tb.body, "Add Column"))
	pump(t, fx.q, func() bool { return len(tb.design.design.Columns()) == 3 })
	test.Tap(findButton(tb.body, "Preview…"))
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	tapOnTop(t, fx, "Run")

	waitFooter(t, fx, tb, "read-only")
	if got := len(ranStatements()); got != 0 {
		t.Errorf("%d statements ran on a read-only connection", got)
	}
	if top := fx.s.win.Canvas().Overlays().Top(); top != nil {
		t.Errorf("it offered a way past: %q", strings.Join(labelTexts(top), " "))
	}
}
