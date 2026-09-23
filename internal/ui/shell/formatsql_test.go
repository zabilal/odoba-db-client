package shell

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
)

// Laying a statement out from the window (FR-5.12).

func withQuery(t *testing.T, text string) (*fixture, *tab, *queryTab) {
	t.Helper()
	fx := newFixture(t)
	tb, q := openQuery(t, fx, "")
	q.editor.Document().SetText(text)
	fx.s.sync()
	return fx, tb, q
}

// The whole script is laid out when nothing is selected.
func TestTheWholeScriptIsLaidOut(t *testing.T) {
	fx, tb, q := withQuery(t, "select a,b from t where a=1;")
	if fx.s.menuItems[cmdFormatSQL].Disabled {
		t.Fatal("there is a statement to lay out")
	}
	fx.s.formatQuery()
	got := q.editor.Document().Text()
	if got != "SELECT a, b\nFROM t\nWHERE a = 1;\n" {
		t.Errorf("it laid out %q", got)
	}
	if !strings.Contains(tb.footer.Text, "Laid out") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// What is selected is what is laid out: somebody who highlighted three
// lines meant those three.
func TestOnlyWhatIsSelectedIsLaidOut(t *testing.T) {
	fx, _, q := withQuery(t, "select a,b from t;\nselect c,d from u;")
	doc := q.editor.Document()
	doc.SetCaret(editor.Pos{Line: 1}, false)
	doc.SetCaret(doc.PosAt(len(doc.Text())), true)

	fx.s.formatQuery()
	if got, want := doc.Text(), "select a,b from t;\nSELECT c, d\nFROM u;\n"; got != want {
		t.Errorf("it laid out\n%q\nwant\n%q", got, want)
	}
}

// Laying out what is already laid out changes nothing, and costs nobody an
// undo.
func TestLayingOutWhatIsAlreadyLaidOut(t *testing.T) {
	fx, tb, q := withQuery(t, "SELECT a, b\nFROM t;\n")
	before := q.editor.Document().Revision()
	fx.s.formatQuery()
	if got := q.editor.Document().Revision(); got != before {
		t.Errorf("the document moved from revision %d to %d", before, got)
	}
	if !strings.Contains(tb.footer.Text, "Already laid out") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}

// The caret stays near what was being looked at.
func TestTheCaretStaysNearWhereItWas(t *testing.T) {
	fx, _, q := withQuery(t, "select a,b,c from some_table where a=1 and b=2;")
	doc := q.editor.Document()
	doc.SetCaret(doc.PosAt(25), false)
	fx.s.formatQuery()
	if at := doc.Offset(doc.Caret()); at == 0 || at > len(doc.Text()) {
		t.Errorf("the caret is at %d of %d", at, len(doc.Text()))
	}
}

// A statement that cannot be laid out safely says so, and the rest is still
// laid out.
func TestWhatCouldNotBeLaidOutIsSaid(t *testing.T) {
	fx, tb, q := withQuery(t, "select a,b from t;\nselect 'un\nterminated' from u;")
	fx.s.formatQuery()
	if !strings.Contains(tb.footer.Text, "except") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
	if !strings.Contains(tb.footer.Text, "1 statement") {
		t.Errorf("the footer says %q, want how many were left", tb.footer.Text)
	}
	if !strings.Contains(q.editor.Document().Text(), "SELECT a, b") {
		t.Errorf("the rest was not laid out:\n%s", q.editor.Document().Text())
	}
}

// Nothing is offered where there is nothing to lay out.
func TestLayingOutNeedsAStatement(t *testing.T) {
	fx := newFixture(t)
	if !fx.s.menuItems[cmdFormatSQL].Disabled {
		t.Error("it was offered with nothing open")
	}
	openQuery(t, fx, "")
	fx.s.sync()
	if !fx.s.menuItems[cmdFormatSQL].Disabled {
		t.Error("it was offered on an empty editor")
	}
}

// It is laid out in the connection's own language, which is what decides
// where a statement even ends.
func TestItIsLaidOutInTheConnectionsOwnLanguage(t *testing.T) {
	// The fake speaks PostgreSQL, where a backtick is not a quote and not
	// anything else either, so the statement is left as it was written.
	// MySQL would read it as a name and lay the statement out.
	fx, tb, q := withQuery(t, "select `a` from t;")
	fx.s.formatQuery()
	if got := q.editor.Document().Text(); got != "select `a` from t;\n" {
		t.Errorf("it laid out %q in a language this connection does not speak", got)
	}
	if !strings.Contains(tb.footer.Text, "except") {
		t.Errorf("the footer says %q", tb.footer.Text)
	}
}
