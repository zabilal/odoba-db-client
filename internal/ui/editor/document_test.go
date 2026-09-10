package editor

import (
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// doc builds a document from text in which "|" marks the caret and, when
// present, "^" marks the selection's anchor.
func doc(t *testing.T, marked string) *Document {
	t.Helper()
	d := NewDocument(strings.NewReplacer("|", "", "^", "").Replace(marked), sqllex.PostgreSQL)
	find := func(mark string) (Pos, bool) {
		for i, l := range strings.Split(marked, "\n") {
			clean := strings.NewReplacer("|", "", "^", "")
			if at := strings.Index(l, mark); at >= 0 {
				return Pos{i, len(clean.Replace(l[:at]))}, true
			}
		}
		return Pos{}, false
	}
	caret, ok := find("|")
	if !ok {
		t.Fatalf("no caret in %q", marked)
	}
	d.SetCaret(caret, false)
	if anchor, ok := find("^"); ok {
		d.SetCaret(anchor, false)
		d.SetCaret(caret, true)
	}
	return d
}

// show renders a document the way doc reads it.
func show(d *Document) string {
	type mark struct {
		p Pos
		s string
	}
	marks := []mark{{d.Caret(), "|"}}
	if _, _, ok := d.Selection(); ok {
		marks = append(marks, mark{d.anchor, "^"})
		if d.anchor.Less(d.caret) {
			marks[0], marks[1] = marks[1], marks[0]
		}
	}
	lines := strings.Split(d.Text(), "\n")
	for i := len(marks) - 1; i >= 0; i-- {
		m := marks[i]
		lines[m.p.Line] = lines[m.p.Line][:m.p.Col] + m.s + lines[m.p.Line][m.p.Col:]
	}
	return strings.Join(lines, "\n")
}

func expect(t *testing.T, d *Document, want string) {
	t.Helper()
	if got := show(d); got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestTypingUndoesAWordAtATime(t *testing.T) {
	d := doc(t, "|")
	for _, r := range "select a" {
		d.Insert(string(r))
	}
	expect(t, d, "select a|")
	d.Undo()
	expect(t, d, "select|")
	d.Undo()
	expect(t, d, "|")
	d.Redo()
	d.Redo()
	expect(t, d, "select a|")
}

func TestAClickSeparatesUndoSteps(t *testing.T) {
	d := doc(t, "|")
	d.Insert("a")
	d.Insert("b")
	d.SetCaret(Pos{0, 0}, false)
	d.Insert("c")
	d.Undo()
	expect(t, d, "|ab")
}

func TestNewlineKeepsTheIndent(t *testing.T) {
	d := doc(t, "    select|")
	d.Newline()
	expect(t, d, "    select\n    |")
}

func TestBackspaceJoinsLinesAndRemovesAnIndentLevel(t *testing.T) {
	d := doc(t, "ab\n|cd")
	d.Backspace()
	expect(t, d, "ab|cd")

	d = doc(t, "        |x")
	d.Backspace()
	expect(t, d, "    |x")
	d = doc(t, "      |x")
	d.Backspace()
	expect(t, d, "    |x")
}

func TestARunOfBackspacesIsOneStep(t *testing.T) {
	d := doc(t, "select 1|")
	d.Backspace()
	d.Backspace()
	d.Backspace()
	expect(t, d, "selec|")
	d.Undo()
	expect(t, d, "select 1|")
}

func TestMovementIsByRuneNotByte(t *testing.T) {
	d := doc(t, "é😀|")
	d.Move(Left, false)
	expect(t, d, "é|😀")
	d.Move(Left, false)
	expect(t, d, "|é😀")
	d.Move(Right, false)
	d.Backspace()
	expect(t, d, "|😀")
	if d.Column(Pos{0, len("😀")}) != 1 {
		t.Error("an emoji is one column, not four bytes")
	}
}

func TestWordMotions(t *testing.T) {
	d := doc(t, "SELECT  foo.bar|")
	for _, want := range []string{"SELECT  foo.|bar", "SELECT  foo|.bar", "SELECT  |foo.bar", "|SELECT  foo.bar"} {
		d.Move(WordLeft, false)
		expect(t, d, want)
	}
	for _, want := range []string{"SELECT|  foo.bar", "SELECT  foo|.bar", "SELECT  foo.|bar", "SELECT  foo.bar|"} {
		d.Move(WordRight, false)
		expect(t, d, want)
	}
}

func TestVerticalMovesKeepTheirColumn(t *testing.T) {
	d := doc(t, "abcdef|\nab\nabcdefgh")
	d.Move(Down, false)
	expect(t, d, "abcdef\nab|\nabcdefgh")
	d.Move(Down, false)
	expect(t, d, "abcdef\nab\nabcdef|gh")
	d.Move(Up, false)
	d.Move(Up, false)
	expect(t, d, "abcdef|\nab\nabcdefgh")
	d.Move(Up, false)
	expect(t, d, "|abcdef\nab\nabcdefgh")
}

func TestLineStartIsSmart(t *testing.T) {
	d := doc(t, "    foo|")
	d.Move(LineStart, false)
	expect(t, d, "    |foo")
	d.Move(LineStart, false)
	expect(t, d, "|    foo")
	d.Move(LineStart, false)
	expect(t, d, "    |foo")
}

func TestTypingReplacesTheSelectionAndArrowsCollapseIt(t *testing.T) {
	d := doc(t, "select ^abc| from t")
	d.Insert("x")
	expect(t, d, "select x| from t")

	d = doc(t, "select ^abc| from t")
	d.Move(Left, false)
	expect(t, d, "select |abc from t")
	d = doc(t, "select |abc^ from t")
	d.Move(Right, false)
	expect(t, d, "select abc| from t")
}

func TestShiftArrowsExtendTheSelection(t *testing.T) {
	d := doc(t, "ab|cd")
	d.Move(Right, true)
	d.Move(Right, true)
	if got := d.SelectedText(); got != "cd" {
		t.Errorf("selected %q", got)
	}
	d.Move(WordLeft, true)
	expect(t, d, "|ab^cd")
}

func TestTabIndentsSelectedLinesAsOneStep(t *testing.T) {
	d := doc(t, "^a\n\nb|\nc")
	d.Tab()
	expect(t, d, "^    a\n\n    b|\nc")
	d.Outdent()
	expect(t, d, "^a\n\nb|\nc")
	d.Undo()
	d.Undo()
	expect(t, d, "^a\n\nb|\nc")

	d = doc(t, "ab|")
	d.Tab()
	expect(t, d, "ab  |")
}

func TestSelectionEndingAtALineStartDoesNotIndentThatLine(t *testing.T) {
	d := doc(t, "^a\n|b")
	d.Tab()
	if d.Text() != "    a\nb" {
		t.Errorf("text %q", d.Text())
	}
}

func TestSelectWord(t *testing.T) {
	d := doc(t, "|SELECT foo_bar, 1")
	d.SelectWord(Pos{0, 9})
	if got := d.SelectedText(); got != "foo_bar" {
		t.Errorf("word %q", got)
	}
	d.SelectWord(Pos{0, 14})
	if got := d.SelectedText(); got != "," {
		t.Errorf("punctuation %q", got)
	}
}

func TestPastedLineEndingsAreNormalised(t *testing.T) {
	d := doc(t, "|")
	d.Insert("a\r\nb\rc")
	if d.Text() != "a\nb\nc" || d.Buffer().LineCount() != 3 {
		t.Errorf("%q", d.Text())
	}
}

func TestClickPositionRoundsToTheNearerSide(t *testing.T) {
	d := doc(t, "|a\tb")
	if p := d.PosAtColumn(0, 0); p.Col != 0 {
		t.Errorf("column 0 → %v", p)
	}
	if p := d.PosAtColumn(0, 2); p.Col != 1 { // inside the tab, nearer its start
		t.Errorf("column 2 → %v", p)
	}
	if p := d.PosAtColumn(0, 3); p.Col != 2 { // nearer the tab's end
		t.Errorf("column 3 → %v", p)
	}
	if p := d.PosAtColumn(0, 99); p.Col != 3 {
		t.Errorf("past the end → %v", p)
	}
}

// TestRandomEditsUndoRedoAndHighlight drives thousands of random edits, then
// checks three things against independent oracles: undoing everything gives
// the original text, redoing gives the final text, and the incrementally
// maintained highlighting matches a fresh lex of the result (ADR-0003: an
// oracle sharing the implementation's assumptions proves nothing).
func TestRandomEditsUndoRedoAndHighlight(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		randomEdits(t, seed)
	}
}

// randomEdits checks highlighting after EVERY edit. Checking at intervals let
// a divergence that later healed itself pass unseen.
func randomEdits(t *testing.T, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	pieces := []string{"a", "é", "😀", " ", "\n", "/*", "*/", "'", "--", "$$", "select ", "(1)", "\t", "\n  x\n", "\n/* a\n b\n"}
	const original = "SELECT 1;\n/* block\n comment */\nSELECT 'x';"
	d := NewDocument(original, sqllex.PostgreSQL)
	for i := 0; i < 1500; i++ {
		switch rng.Intn(12) {
		case 0, 1, 2:
			d.Insert(pieces[rng.Intn(len(pieces))])
		case 3:
			d.Backspace()
		case 4:
			d.Delete()
		case 5:
			d.Newline()
		case 6:
			d.Move(Motion(rng.Intn(int(DocEnd)+1)), rng.Intn(3) == 0)
		case 7:
			line := rng.Intn(d.Buffer().LineCount())
			d.SetCaret(Pos{line, rng.Intn(len(d.Buffer().Line(line)) + 1)}, rng.Intn(2) == 0)
		case 8:
			d.Tab()
		case 9:
			d.Outdent()
		case 10:
			d.Undo()
		case 11:
			d.Redo()
		}
		checkHighlight(t, d)
	}
	final := d.Text()
	checkHighlight(t, d)

	// Redo exactly the steps just undone. Random Undo ops can leave older
	// entries on the redo stack, beneath these; redoing until it is empty
	// would apply those too and overshoot.
	undone := 0
	for d.Undo() {
		undone++
	}
	if d.Text() != original {
		t.Fatalf("undoing everything gave\n%q\nwant\n%q", d.Text(), original)
	}
	checkHighlight(t, d)
	for i := 0; i < undone; i++ {
		d.Redo()
	}
	if d.Text() != final {
		t.Fatalf("redoing everything gave\n%q\nwant\n%q", d.Text(), final)
	}
	checkHighlight(t, d)
}

func checkHighlight(t *testing.T, d *Document) {
	t.Helper()
	if t.Failed() {
		return
	}
	fresh := NewHighlighter(NewBuffer(d.Text()), sqllex.PostgreSQL)
	for i := 0; i < d.Buffer().LineCount(); i++ {
		if got, want := d.Highlighter().Tokens(i), fresh.Tokens(i); !reflect.DeepEqual(got, want) {
			t.Fatalf("line %d %q: incremental tokens %v, fresh lex %v\ndocument: %q", i, d.Buffer().Line(i), got, want, d.Text())
		}
	}
}

func TestMatchBracket(t *testing.T) {
	for _, c := range []struct {
		marked   string
		at, pair Pos
		ok       bool
	}{
		{"(a (b)| c)", Pos{0, 5}, Pos{0, 3}, true}, // just before the caret wins
		{"|(a (b) c)", Pos{0, 0}, Pos{0, 8}, true}, // else just after it
		{"(\n  1\n)|", Pos{2, 0}, Pos{0, 0}, true}, // across lines
		{"select '(', (1)|", Pos{0, 14}, Pos{0, 12}, true},
		{"select 1 -- (|", Pos{}, Pos{}, false}, // in a comment: text, not code
		{"(1|", Pos{}, Pos{}, false},            // unmatched
	} {
		d := doc(t, c.marked)
		at, pair, ok := d.MatchBracket()
		if ok != c.ok || ok && (at != c.at || pair != c.pair) {
			t.Errorf("%q: got %v %v %v, want %v %v %v", c.marked, at, pair, ok, c.at, c.pair, c.ok)
		}
	}
}

func TestIndentLinesWorksAnywhereInTheLine(t *testing.T) {
	d := doc(t, "ab|cd")
	d.IndentLines()
	expect(t, d, "    ab|cd")
	d.Outdent()
	expect(t, d, "ab|cd")
}

func TestRevisionCountsEditsNotMoves(t *testing.T) {
	d := doc(t, "ab|")
	r := d.Revision()
	d.Move(Left, false)
	d.SelectAll()
	if d.Revision() != r {
		t.Error("moving the caret changed the revision")
	}
	d.Insert("x")
	if d.Revision() == r {
		t.Error("an edit did not change the revision")
	}
}

func TestOffset(t *testing.T) {
	d := doc(t, "é|b\ncd")
	if got := d.Offset(d.Caret()); got != 2 {
		t.Errorf("offset after é = %d, want 2 bytes", got)
	}
	if got := d.Offset(Pos{1, 1}); got != len("éb\nc") {
		t.Errorf("offset %d", got)
	}
}

func TestPosAtInvertsOffset(t *testing.T) {
	d := doc(t, "|é1\n\nab😀")
	for line := 0; line < d.Buffer().LineCount(); line++ {
		l := d.Buffer().Line(line)
		for col := 0; col <= len(l); col++ {
			if col < len(l) && !utf8RuneStart(l[col]) {
				continue
			}
			p := Pos{line, col}
			if got := d.PosAt(d.Offset(p)); got != p {
				t.Errorf("PosAt(Offset(%v)) = %v", p, got)
			}
		}
	}
	if got := d.PosAt(1 << 20); got != (Pos{2, len("ab😀")}) {
		t.Errorf("past the end → %v", got)
	}
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

func TestWordAt(t *testing.T) {
	d := doc(t, "|select foo_bar")
	from, to := d.WordAt(Pos{0, 9})
	if from != (Pos{0, 7}) || to != (Pos{0, 14}) {
		t.Errorf("WordAt → %v..%v", from, to)
	}
}
