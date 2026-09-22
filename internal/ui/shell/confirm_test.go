package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/store"
)

// richTexts collects the text a Form draws for its item labels. A Form
// renders them through RichText rather than Label, so labelTexts cannot see
// them — the same quirk that hid a Select's text in T2.74.
func richTexts(o fyne.CanvasObject) []string {
	var out []string
	switch v := o.(type) {
	case *widget.RichText:
		out = append(out, v.String())
	case *fyne.Container:
		for _, c := range v.Objects {
			out = append(out, richTexts(c)...)
		}
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(v).Objects() {
			out = append(out, richTexts(c)...)
		}
	}
	return out
}

// typeOnTop types into the confirmation on top, which is what a person has
// to do before a production write will go anywhere (FR-4.9).
func typeOnTop(t *testing.T, fx *fixture, text string) {
	t.Helper()
	e := entryIn(fx.s.win.Canvas().Overlays().Top())
	if e == nil {
		t.Fatal("the confirmation has nothing to type into")
	}
	e.SetText(text)
}

// asking puts up one typed confirmation for a connection with this name and
// answers how many times the operation ran.
func asking(t *testing.T, name string) (*fixture, *int, *int) {
	t.Helper()
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{
		Name: name, Driver: "postgres", Host: "db1", Environment: "production"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ran, declined := 0, 0
	fx.s.askToType(c.ID, "Change Data on Production?",
		productionBody("script changes data on", name, "Nothing has run yet."),
		"Run", func() { ran++ }, func() { declined++ })
	if fx.s.win.Canvas().Overlays().Top() == nil {
		t.Fatal("nothing was asked")
	}
	return fx, &ran, &declined
}

func TestAProductionWriteIsConfirmedByTypingAndNotByClicking(t *testing.T) {
	fx, ran, _ := asking(t, "prod")

	run := findButton(fx.s.win.Canvas().Overlays().Top(), "Run")
	if run == nil {
		t.Fatal("the confirmation has no Run button")
	}
	if !run.Disabled() {
		t.Fatal("Run could be pressed before anything was typed")
	}
	test.Tap(run)
	if *ran != 0 {
		t.Fatal("clicking alone ran it")
	}

	typeOnTop(t, fx, "prod")
	if run.Disabled() {
		t.Fatal("the name was typed and Run stayed out of reach")
	}
	test.Tap(run)
	if *ran != 1 {
		t.Errorf("it ran %d times", *ran)
	}
}

func TestAlmostTheNameIsNotTheName(t *testing.T) {
	// Each of these is something somebody might type while meaning to get
	// past the question rather than to read it.
	for _, typed := range []string{"", "p", "pro", "prod2", "PROD", "Prod", "production", "prod prod"} {
		t.Run("“"+typed+"”", func(t *testing.T) {
			fx, ran, _ := asking(t, "prod")
			typeOnTop(t, fx, typed)
			run := findButton(fx.s.win.Canvas().Overlays().Top(), "Run")
			if !run.Disabled() {
				t.Fatalf("%q was accepted for “prod”", typed)
			}
			test.Tap(run)
			if *ran != 0 {
				t.Errorf("%q ran it", typed)
			}
		})
	}
}

func TestSpaceAroundTheNameIsForgiven(t *testing.T) {
	// A name is often pasted, and a pasted name brings whitespace with it.
	// That is not the carelessness this guards against.
	fx, ran, _ := asking(t, "prod")
	typeOnTop(t, fx, "  prod  ")
	run := findButton(fx.s.win.Canvas().Overlays().Top(), "Run")
	if run.Disabled() {
		t.Fatal("a pasted name was refused")
	}
	test.Tap(run)
	if *ran != 1 {
		t.Errorf("it ran %d times", *ran)
	}
}

func TestSayingNoRunsNothingAndSaysSo(t *testing.T) {
	fx, ran, declined := asking(t, "prod")
	typeOnTop(t, fx, "prod") // even having typed it
	tapOnTop(t, fx, "Cancel")
	if *ran != 0 {
		t.Errorf("it ran %d times after Cancel", *ran)
	}
	if *declined != 1 {
		t.Errorf("the caller was told %d times", *declined)
	}
}

func TestTheQuestionNamesTheConnectionAndSaysNothingHasHappened(t *testing.T) {
	fx, _, _ := asking(t, "orders-prod")
	top := fx.s.win.Canvas().Overlays().Top()
	text := strings.Join(labelTexts(top), " ")
	for _, want := range []string{"orders-prod", "marked Production", "Nothing has run yet"} {
		if !strings.Contains(text, want) {
			t.Errorf("the question does not say %q: %q", want, text)
		}
	}
	// And it says what to do, which is the part a person acts on.
	prompt := strings.Join(richTexts(top), " ")
	if !strings.Contains(prompt, "Type orders-prod to continue") {
		t.Errorf("the question does not say what to type: %q", prompt)
	}
	if e := entryIn(top); e == nil || e.PlaceHolder != "orders-prod" {
		t.Errorf("the box does not show the name to type")
	}
}

// A connection this no longer knows about — deleted while the operation was
// in flight — would otherwise be confirmed by typing nothing at all, which
// is no confirmation. The store refuses a connection with no name, so this
// is the way that case arises.
func TestAConnectionWithNoNameStillHasSomethingToType(t *testing.T) {
	fx := newFixture(t)
	ran := 0
	fx.s.askToType("no-such-connection", "Change Data on Production?", "body", "Run", func() { ran++ }, nil)

	run := findButton(fx.s.win.Canvas().Overlays().Top(), "Run")
	if run == nil || !run.Disabled() {
		t.Fatal("a connection with no name was confirmed by typing nothing")
	}
	typeOnTop(t, fx, "")
	if !run.Disabled() {
		t.Fatal("typing nothing was accepted")
	}
	typeOnTop(t, fx, unnamed)
	if run.Disabled() {
		t.Fatalf("typing %q was refused", unnamed)
	}
	test.Tap(run)
	if ran != 1 {
		t.Errorf("it ran %d times", ran)
	}
}
