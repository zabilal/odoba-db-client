package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
)

func TestFindCountsAndStepsThroughMatches(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	doc := q.editor.Document()
	doc.SetText("select a;\nselect b;\nSELECT c;")
	doc.SetCaret(editor.Pos{}, false)
	fx.s.run(cmdFind)
	f := q.find
	if !f.visible() {
		t.Fatal("⌘F should open the find bar")
	}
	test.Type(f.find, "select")
	if f.status.Text != "3 matches" {
		t.Errorf("status %q", f.status.Text)
	}
	fx.s.run(cmdFindNext)
	if doc.SelectedText() != "select" || f.status.Text != "1 of 3" {
		t.Errorf("selected %q, status %q", doc.SelectedText(), f.status.Text)
	}
	fx.s.run(cmdFindNext)
	fx.s.run(cmdFindNext)
	if doc.SelectedText() != "SELECT" || f.status.Text != "3 of 3" {
		t.Errorf("selected %q, status %q", doc.SelectedText(), f.status.Text)
	}
	fx.s.run(cmdFindPrev)
	if f.status.Text != "2 of 3" {
		t.Errorf("previous: status %q", f.status.Text)
	}
	// The selection is the second "select"; with case matched, "SELECT" drops
	// out and it is the second of two.
	f.match.SetChecked(true)
	if f.status.Text != "2 of 2" {
		t.Errorf("match case: status %q", f.status.Text)
	}
}

func TestFindStartsFromTheSelection(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	doc := q.editor.Document()
	doc.SetText("select customer_id from t")
	doc.SelectWord(editor.Pos{Line: 0, Col: 9})
	fx.s.run(cmdFind)
	if q.find.find.Text != "customer_id" {
		t.Errorf("find field %q; it should start with the selected word", q.find.find.Text)
	}
}

func TestReplaceAllIsOneUndoAndMarksTheTabEdited(t *testing.T) {
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	doc := q.editor.Document()
	doc.SetText("a b a")
	fx.s.run(cmdFindReplace)
	f := q.find
	if !f.replace.Visible() {
		t.Fatal("⌥⌘F should show the replace row")
	}
	test.Type(f.find, "a")
	f.with.SetText("z")
	if n := f.replaceAll(); n != 2 || doc.Text() != "z b z" {
		t.Fatalf("replaced %d: %q", n, doc.Text())
	}
	if !strings.HasSuffix(tb.item.Text, "•") {
		t.Errorf("tab %q should show the replacement as an unsaved edit", tb.item.Text)
	}
	doc.Undo()
	if doc.Text() != "a b a" {
		t.Errorf("one undo left %q", doc.Text())
	}
}

func TestAnInvalidPatternSaysSo(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	q.editor.Document().SetText("x")
	fx.s.run(cmdFind)
	q.find.regex.SetChecked(true)
	test.Type(q.find.find, "(")
	if q.find.status.Text != "Invalid pattern" || q.find.status.Importance != widget.DangerImportance {
		t.Errorf("status %q (%v)", q.find.status.Text, q.find.status.Importance)
	}
}

func TestEscapeClosesTheFindBar(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	fx.s.run(cmdFind)
	q.find.find.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if q.find.visible() {
		t.Error("Escape should close the find bar")
	}
}

func TestTheFindBarGetsRoomWhenShown(t *testing.T) {
	// Laid out while hidden, the bar kept a zero width, and showing it gave
	// its fields negative widths. Found by rendering screenshots, where the
	// software renderer crashed on them.
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	fx.s.run(cmdFindReplace)
	if w := q.find.find.Size().Width; w < 100 {
		t.Errorf("the find field is %v wide", w)
	}
	if w := q.find.with.Size().Width; w < 100 {
		t.Errorf("the replace field is %v wide", w)
	}
}

func TestTheMatchCountIsNotClipped(t *testing.T) {
	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	q.editor.Document().SetText("select 1; select 2; select 3; select 4")
	fx.s.run(cmdFind)
	test.Type(q.find.find, "select")
	if got, need := q.find.status.Size().Width, q.find.status.MinSize().Width; got < need {
		t.Errorf("%q drawn %v wide but needs %v", q.find.status.Text, got, need)
	}
}
