package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

var viewNode = model.Node{Ref: model.NewRef(model.KindView, "main", "recent"), Label: "recent"}

// editingSource opens the source editor on an object and waits for it to
// have read one.
func editingSource(t *testing.T, n model.Node) (*fixture, *tab, *sourcePanel) {
	t.Helper()
	forgetStatements()
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	tb := fx.s.OpenSource(c.ID, n)
	pump(t, fx.q, func() bool { return tb.source != nil })
	return fx, tb, tb.source
}

func TestASourceEditorOpensOnWhatTheEngineKeeps(t *testing.T) {
	_, tb, p := editingSource(t, viewNode)
	if tb.item.Text != "Source: recent" {
		t.Errorf("the tab is called %q", tb.item.Text)
	}
	if got := p.ed.Document().Text(); got != "SELECT id, name FROM items" {
		t.Errorf("it opened on %q", got)
	}
	if got := tb.footer.Text; got != "No changes yet." {
		t.Errorf("it opens saying %q", got)
	}
}

func TestEditingSourceSaysSoAndRunsNothing(t *testing.T) {
	fx, tb, p := editingSource(t, viewNode)
	p.ed.Document().SetText("SELECT id FROM items WHERE id > 40")
	p.say()

	if got := tb.footer.Text; !strings.Contains(got, "Changed") {
		t.Errorf("the footer says %q", got)
	}
	if got := len(ranStatements()); got != 0 {
		t.Errorf("%d statements ran before anybody read them", got)
	}
	_ = fx
}

// What runs is rendered from what was edited, and read first.
func TestPreviewingEditedSource(t *testing.T) {
	fx, tb, p := editingSource(t, viewNode)
	p.ed.Document().SetText("SELECT id FROM items WHERE id > 40")

	test.Tap(findButton(tb.body, "Preview…"))
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	shown := previewText(fx.s.win.Canvas().Overlays().Top())
	if !strings.Contains(shown, "CREATE OR REPLACE VIEW recent AS SELECT id FROM items WHERE id > 40") {
		t.Errorf("the preview shows %q", shown)
	}
	if got := len(ranStatements()); got != 0 {
		t.Errorf("%d statements ran before the preview was answered", got)
	}

	tapOnTop(t, fx, "Run")
	pump(t, fx.q, func() bool { return len(ranStatements()) > 0 })
	if got := ranStatements()[0]; !strings.Contains(got, "id > 40") {
		t.Errorf("it ran %q", got)
	}
}

// Reverting puts back what the engine gave.
func TestRevertingSource(t *testing.T) {
	_, tb, p := editingSource(t, viewNode)
	p.ed.Document().SetText("nonsense")
	p.say()
	test.Tap(findButton(tb.body, "Revert"))
	if got := p.ed.Document().Text(); got != "SELECT id, name FROM items" {
		t.Errorf("after reverting it holds %q", got)
	}
	if got := tb.footer.Text; got != "No changes yet." {
		t.Errorf("the footer says %q", got)
	}
}

// A routine and a trigger are re-sent as the statement the engine printed,
// because that is what they are.
func TestARoutineAndATriggerAreEditedAsTheirStatements(t *testing.T) {
	routine := model.Node{Ref: model.NewRef(model.KindRoutine, "main", "f(integer)"), Label: "f(integer)"}
	_, _, p := editingSource(t, routine)
	if !strings.HasPrefix(p.ed.Document().Text(), "CREATE OR REPLACE FUNCTION") {
		t.Errorf("a routine opened on %q", p.ed.Document().Text())
	}

	trig := model.Node{Ref: model.NewRef(model.KindTrigger, "main", "items", "audit"), Label: "audit"}
	_, _, tp := editingSource(t, trig)
	if !strings.HasPrefix(tp.ed.Document().Text(), "CREATE TRIGGER") {
		t.Errorf("a trigger opened on %q", tp.ed.Document().Text())
	}
}

// A sequence has no source, so what is shown is the statement that would set
// it — and editing that statement is editing the sequence.
func TestASequenceIsEditedAsItsNumbers(t *testing.T) {
	seq := model.Node{Ref: model.NewRef(model.KindSequence, "main", "items_id_seq"), Label: "items_id_seq"}
	fx, tb, p := editingSource(t, seq)
	if !strings.Contains(p.ed.Document().Text(), "ALTER SEQUENCE items_id_seq START WITH 1") {
		t.Errorf("a sequence opened on %q", p.ed.Document().Text())
	}

	p.ed.Document().SetText("ALTER SEQUENCE items_id_seq START WITH 100")
	test.Tap(findButton(tb.body, "Preview…"))
	pump(t, fx.q, func() bool { return fx.s.win.Canvas().Overlays().Top() != nil })
	tapOnTop(t, fx, "Run")
	pump(t, fx.q, func() bool { return len(ranStatements()) > 0 })
	if got := ranStatements()[0]; !strings.Contains(got, "START WITH 100") {
		t.Errorf("it ran %q", got)
	}
}

// The editor is offered for the kinds it can edit, and not for a table,
// which has a designer of its own.
func TestWhichObjectsHaveASourceToEdit(t *testing.T) {
	for kind, want := range map[model.ObjectKind]bool{
		model.KindView:             true,
		model.KindMaterializedView: true,
		model.KindRoutine:          true,
		model.KindTrigger:          true,
		model.KindSequence:         true,
		model.KindTable:            false,
		model.KindColumn:           false,
	} {
		if got := sourceKinds[kind]; got != want {
			t.Errorf("%s has a source to edit: %v, want %v", kind, got, want)
		}
	}
}

// Opening the same object twice brings the first tab forward.
func TestEditingTheSameSourceTwiceOpensOneTab(t *testing.T) {
	fx, tb, _ := editingSource(t, viewNode)
	if again := fx.s.OpenSource(tb.connID, viewNode); again != tb {
		t.Error("it opened a second tab on the same source")
	}
	if got := len(fx.s.open); got != 1 {
		t.Errorf("%d tabs are open", got)
	}
}
