package editor

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

func TestLiteralSearchIsLiteral(t *testing.T) {
	d := NewDocument("SELECT f(a.b) FROM t; -- f(a.b)", sqllex.PostgreSQL)
	ms, err := d.FindAll(Search{Text: "f(a.b)"}, 0)
	if err != nil || len(ms) != 2 {
		t.Fatalf("%v, %v; a literal \"(\" is not regex syntax", ms, err)
	}
	if ms[0].From != (Pos{0, 7}) || ms[0].To != (Pos{0, 13}) {
		t.Errorf("first match %v", ms[0])
	}
}

func TestCaseAndWholeWords(t *testing.T) {
	d := NewDocument("id idx ID Id", sqllex.PostgreSQL)
	count := func(s Search) int {
		ms, _ := d.FindAll(s, 0)
		return len(ms)
	}
	if n := count(Search{Text: "id"}); n != 4 {
		t.Errorf("case-insensitive: %d, want 4", n)
	}
	if n := count(Search{Text: "id", Case: true}); n != 2 {
		t.Errorf("match case: %d, want 2 (id, idx)", n)
	}
	if n := count(Search{Text: "id", Word: true}); n != 3 {
		t.Errorf("whole words: %d, want 3 (not idx)", n)
	}
}

func TestFindNextWrapsBothWays(t *testing.T) {
	d := doc(t, "a x a x |a")
	s := Search{Text: "a"}
	d.FindNext(s, false)
	if from, _, _ := d.Selection(); from != (Pos{0, 8}) {
		t.Errorf("next → %v", from)
	}
	d.FindNext(s, false)
	if from, _, _ := d.Selection(); from != (Pos{0, 0}) {
		t.Errorf("wrap forward → %v", from)
	}
	d.FindNext(s, true)
	if from, _, _ := d.Selection(); from != (Pos{0, 8}) {
		t.Errorf("wrap backward → %v", from)
	}
	d.FindNext(s, true)
	if from, _, _ := d.Selection(); from != (Pos{0, 4}) {
		t.Errorf("back → %v", from)
	}
}

func TestMatchesAcrossNonASCIIText(t *testing.T) {
	d := NewDocument("é é\nnaïve café", sqllex.PostgreSQL)
	ms, _ := d.FindAll(Search{Text: "café"}, 0)
	if len(ms) != 1 || ms[0].From != (Pos{1, len("naïve ")}) || ms[0].To != (Pos{1, len("naïve café")}) {
		t.Errorf("%v", ms)
	}
}

func TestReplaceSelectionThenMovesOn(t *testing.T) {
	d := doc(t, "|a b a")
	s := Search{Text: "a"}
	d.FindNext(s, false) // selects the first a
	if ok, _ := d.ReplaceSelection(s, "z"); !ok {
		t.Fatal("did not replace")
	}
	if d.Text() != "z b a" || d.SelectedText() != "a" {
		t.Errorf("text %q, selected %q", d.Text(), d.SelectedText())
	}
}

func TestRegexReplaceAllExpandsGroupsInOneUndo(t *testing.T) {
	d := NewDocument("ann@a.com, bob@b.org", sqllex.PostgreSQL)
	n, err := d.ReplaceAll(Search{Text: `(\w+)@(\w+)`, Regex: true}, "$2:$1")
	if err != nil || n != 2 || d.Text() != "a:ann.com, b:bob.org" {
		t.Fatalf("%d, %v, %q", n, err, d.Text())
	}
	d.Undo()
	if d.Text() != "ann@a.com, bob@b.org" {
		t.Errorf("one undo left %q; replace all must be one step", d.Text())
	}
}

func TestLiteralReplacementIsTakenAsWritten(t *testing.T) {
	d := NewDocument("a a", sqllex.PostgreSQL)
	d.ReplaceAll(Search{Text: "a"}, "$1")
	if d.Text() != "$1 $1" {
		t.Errorf("%q", d.Text())
	}
}

func TestInvalidRegexIsAnErrorAndEmptyMatchesAreSkipped(t *testing.T) {
	d := NewDocument("aaa b", sqllex.PostgreSQL)
	if _, err := d.FindAll(Search{Text: "(", Regex: true}, 0); err == nil {
		t.Error("an invalid regex should say so")
	}
	ms, err := d.FindAll(Search{Text: "a*", Regex: true}, 0)
	if err != nil || len(ms) != 1 || ms[0].To != (Pos{0, 3}) {
		t.Errorf("a* → %v, %v; only the non-empty run", ms, err)
	}
	if n, _ := d.ReplaceAll(Search{Text: "x*", Regex: true}, "y"); n != 0 || d.Text() != "aaa b" {
		t.Errorf("an all-empty pattern replaced %d: %q", n, d.Text())
	}
}

func TestFindAllLimit(t *testing.T) {
	d := NewDocument("a a a a a", sqllex.PostgreSQL)
	if ms, _ := d.FindAll(Search{Text: "a"}, 3); len(ms) != 3 {
		t.Errorf("%d matches with a limit of 3", len(ms))
	}
}
