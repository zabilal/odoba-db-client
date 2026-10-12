package editor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

func docOf(text string) *Document { return NewDocument(text, sqllex.DialectFor("sql")) }

// marked is the document's text with every caret drawn in it: | where a
// caret is, [ and ] about a selection with the caret's end marked, and the
// first caret's mark doubled so it can be told from the others.
func marked(d *Document) string {
	type mark struct {
		at Pos
		s  string
	}
	var marks []mark
	all, first := d.all()
	for i, r := range all {
		bar := "|"
		if i == first {
			bar = "‖"
		}
		if r.Empty() {
			marks = append(marks, mark{r.Caret, bar})
			continue
		}
		open, shut := "[", "]"
		if r.Caret == r.From() {
			open = bar + "["
		} else {
			shut = "]" + bar
		}
		marks = append(marks, mark{r.From(), open}, mark{r.To(), shut})
	}
	lines := strings.Split(d.Text(), "\n")
	for i := len(marks) - 1; i >= 0; i-- {
		m := marks[i]
		l := lines[m.at.Line]
		lines[m.at.Line] = l[:m.at.Col] + m.s + l[m.at.Col:]
	}
	return strings.Join(lines, "⏎")
}

func TestTypingHappensAtEveryCaret(t *testing.T) {
	d := docOf("aa\nbb\ncc")
	d.SetCaret(Pos{0, 1}, false)
	d.AddCaret(Pos{1, 1})
	d.AddCaret(Pos{2, 1})
	if !d.Multiple() || len(d.Carets()) != 3 || marked(d) != "a|a⏎b|b⏎c‖c" {
		t.Fatalf("three carets, the last placed the first: %s", marked(d))
	}
	d.Insert("X")
	d.Insert("YZ")
	if got := marked(d); got != "aXYZ|a⏎bXYZ|b⏎cXYZ‖c" {
		t.Errorf("typed at each: %s", got)
	}
	// Two on one line: the second is carried by what the first types.
	d = docOf("abcdef")
	d.SetCaret(Pos{0, 2}, false)
	d.AddCaret(Pos{0, 4})
	d.Insert("--")
	if got := marked(d); got != "ab--|cd--‖ef" {
		t.Errorf("two carets on a line: %s", got)
	}
	// Selections are replaced, each by what is typed.
	d = docOf("one two\nthree four")
	d.SelectBlock(Pos{0, 0}, Pos{1, 3})
	if got := marked(d); got != "[one]| two⏎[thr]‖ee four" {
		t.Fatalf("a block: %s", got)
	}
	d.Insert("N")
	if got := marked(d); got != "N| two⏎N‖ee four" {
		t.Errorf("selections replaced: %s", got)
	}
	// A new line at each, with each line's own indentation.
	d = docOf("  a b\n    c d")
	d.SetCaret(Pos{0, 3}, false)
	d.AddCaret(Pos{1, 5})
	d.Newline()
	if got := marked(d); got != "  a⏎  | b⏎    c⏎    ‖ d" {
		t.Errorf("a new line at each: %s", got)
	}
	// Tab goes to each caret's own next stop.
	d = docOf("a\nbbb")
	d.SetCaret(Pos{0, 1}, false)
	d.AddCaret(Pos{1, 3})
	d.Tab()
	if got := marked(d); got != "a   |⏎bbb ‖" {
		t.Errorf("tab at each: %s", got)
	}
}

func TestDeletingHappensAtEveryCaretAndCaretsThatMeetAreOne(t *testing.T) {
	d := docOf("abc\ndef")
	d.SetCaret(Pos{0, 2}, false)
	d.AddCaret(Pos{1, 2})
	d.Backspace()
	if got := marked(d); got != "a|c⏎d‖f" {
		t.Errorf("backspace at each: %s", got)
	}
	d.Delete()
	if got := marked(d); got != "a|⏎d‖" {
		t.Errorf("delete at each: %s", got)
	}
	// Backspace over the line break between them: the lines join, and the
	// two carets, now far apart on one line, are still two.
	d = docOf("ab\ncd")
	d.SetCaret(Pos{0, 1}, false)
	d.AddCaret(Pos{1, 0})
	d.Backspace()
	if got := marked(d); got != "|b‖cd" {
		t.Errorf("joined lines: %s", got)
	}
	// Side by side, deleting back brings them to one place, and they are one.
	d = docOf("abcd")
	d.SetCaret(Pos{0, 3}, false)
	d.AddCaret(Pos{0, 4})
	d.Backspace()
	if got := marked(d); got != "ab‖" || d.Multiple() {
		t.Errorf("two carets that met: %s, multiple %v", got, d.Multiple())
	}
	// Selections are deleted, and Cut gives back each, a line apiece.
	d = docOf("one two\nthree four")
	d.SelectBlock(Pos{0, 4}, Pos{1, 7})
	if got := d.SelectedTexts(); got != "two\ne f" {
		t.Fatalf("what is selected: %q", got)
	}
	if got := d.Cut(); got != "two\ne f" || marked(d) != "one |⏎thre‖our" {
		t.Errorf("cut %q, leaving %s", got, marked(d))
	}
	if d.SelectedTexts() != "" || d.Cut() != "" {
		t.Error("with nothing selected there is nothing to copy or cut")
	}
}

func TestOneKeystrokeAtSeveralCaretsIsOneStepToUndo(t *testing.T) {
	d := docOf("aa\nbb\ncc")
	d.SetCaret(Pos{0, 1}, false)
	d.AddCaret(Pos{1, 1})
	d.AddCaret(Pos{2, 1})
	d.Insert("X")
	d.Insert("Y")
	d.Backspace()
	if got := marked(d); got != "aX|a⏎bX|b⏎cX‖c" {
		t.Fatalf("%s", got)
	}
	for _, want := range []string{"aXY|a⏎bXY|b⏎cXY‖c", "aX|a⏎bX|b⏎cX‖c", "a|a⏎b|b⏎c‖c"} {
		if !d.Undo() || marked(d) != want {
			t.Fatalf("undone to %s, not %s", marked(d), want)
		}
	}
	if d.Undo() || d.CanUndo() {
		t.Error("three steps were made, and more than three were taken back")
	}
	for _, want := range []string{"aX|a⏎bX|b⏎cX‖c", "aXY|a⏎bXY|b⏎cXY‖c", "aX|a⏎bX|b⏎cX‖c"} {
		if !d.Redo() || marked(d) != want {
			t.Fatalf("redone to %s, not %s", marked(d), want)
		}
	}
	if d.Redo() {
		t.Error("more was redone than was undone")
	}
	// A step made at one caret, after steps at several, is its own; and
	// undoing it brings one caret back, not several.
	d.SetCaret(Pos{0, 0}, false)
	d.Insert("Q")
	if !d.Undo() || marked(d) != "‖aXa⏎bXb⏎cXc" {
		t.Errorf("%s", marked(d))
	}
	if !d.Undo() || marked(d) != "aXY|a⏎bXY|b⏎cXY‖c" {
		t.Errorf("%s", marked(d))
	}
	// Lines that change in number are undone whole.
	d = docOf("ab\ncd\nef")
	d.SetCaret(Pos{0, 1}, false)
	d.AddCaret(Pos{1, 1})
	d.AddCaret(Pos{2, 1})
	d.Newline()
	if got := d.Text(); got != "a\nb\nc\nd\ne\nf" {
		t.Fatalf("%q", got)
	}
	d.Insert("x")
	typing, breaking := d.Undo(), d.Undo()
	if !typing || !breaking || d.Text() != "ab\ncd\nef" || marked(d) != "a|b⏎c|d⏎e‖f" {
		t.Errorf("%s", marked(d))
	}
	if !d.Redo() || d.Text() != "a\nb\nc\nd\ne\nf" || !d.Redo() || d.Text() != "a\nxb\nc\nxd\ne\nxf" {
		t.Errorf("%q", d.Text())
	}
}

func TestEveryCaretMoves(t *testing.T) {
	d := docOf("one two\nab\nthree four")
	d.SetCaret(Pos{0, 5}, false)
	d.AddCaret(Pos{2, 7})
	d.Move(Left, false)
	d.Move(WordLeft, false)
	if got := marked(d); got != "|one two⏎ab⏎‖three four" {
		t.Errorf("each moved: %s", got)
	}
	d.Move(WordRight, true)
	if got := marked(d); got != "[one]| two⏎ab⏎[three]‖ four" {
		t.Errorf("each selected: %s", got)
	}
	d.Move(Left, false) // each collapses to its own left
	if got := marked(d); got != "|one two⏎ab⏎‖three four" {
		t.Errorf("each collapsed: %s", got)
	}
	// Down through a short line, each keeps its own column.
	d = docOf("abcdef\nab\nabcdef\nabcdef")
	d.SetCaret(Pos{0, 5}, false)
	d.AddCaret(Pos{1, 1})
	d.Move(Down, false)
	d.Move(Down, false)
	if got := marked(d); got != "abcdef⏎ab⏎abcde|f⏎a‖bcdef" {
		t.Errorf("each kept its column: %s", got)
	}
	// Moved to the same place, they are one; and to the same line's start
	// and end, they are not.
	d.Move(DocEnd, false)
	if d.Multiple() || marked(d) != "abcdef⏎ab⏎abcdef⏎abcdef‖" {
		t.Errorf("carets at one place: %s", marked(d))
	}
	// Selections that grow into each other are one selection, and the first
	// caret is the one that is left.
	d = docOf("abcdefgh")
	d.SetCaret(Pos{0, 2}, false)
	d.AddCaret(Pos{0, 4})
	d.Move(Right, true)
	d.Move(Right, true)
	if got := marked(d); got != "ab[cd]|[ef]‖gh" {
		t.Fatalf("touching, they are two: %s", got)
	}
	d.Move(Right, true)
	if got := marked(d); got != "ab[cdefg]‖h" || d.Multiple() {
		t.Errorf("overlapping, they are one: %s", got)
	}
	if from, to, ok := d.Selection(); !ok || from != (Pos{0, 2}) || to != (Pos{0, 7}) || d.Caret() != (Pos{0, 7}) {
		t.Errorf("the first caret's selection is the whole: %v %v, caret %v", from, to, d.Caret())
	}
	// Growing backwards, the caret stays at the front.
	d = docOf("abcdefgh")
	d.SetCaret(Pos{0, 6}, false)
	d.AddCaret(Pos{0, 3})
	d.Move(Left, true)
	d.Move(Left, true)
	d.Move(Left, true)
	d.Move(Left, true)
	if got := marked(d); got != "‖[abcdef]gh" {
		t.Errorf("%s", got)
	}
}

func TestWhatIsLeftWhenCaretsMeetOrOneIsUndone(t *testing.T) {
	// The first caret's selection meets one before it: the two are one, the
	// first is what is left, and its caret is at the end it was at.
	d := docOf("abcdefgh")
	d.extra = []Range{{Caret: Pos{0, 1}, Anchor: Pos{0, 4}}}
	d.caret, d.anchor = Pos{0, 6}, Pos{0, 3}
	d.tidy()
	if got := marked(d); got != "a[bcdef]‖gh" || d.Multiple() {
		t.Errorf("%s", got)
	}
	// With another caret before them both, the first is still the first.
	d = docOf("abcdefgh\nxy")
	d.extra = []Range{{Caret: Pos{0, 0}, Anchor: Pos{0, 0}}, {Caret: Pos{0, 2}, Anchor: Pos{0, 5}}, {Caret: Pos{1, 1}, Anchor: Pos{1, 1}}}
	d.caret, d.anchor = Pos{0, 7}, Pos{0, 4}
	d.tidy()
	if got := marked(d); got != "|ab[cdefg]‖h⏎x|y" {
		t.Errorf("%s", got)
	}
	// A page down moves every caret.
	d = docOf("ab\ncd\nef\ngh")
	d.SetCaret(Pos{0, 1}, false)
	d.AddCaret(Pos{1, 2})
	d.MoveLines(2, false)
	if got := marked(d); got != "ab⏎cd⏎e|f⏎gh‖" {
		t.Errorf("each moved two lines: %s", got)
	}
	// Tab with several carets goes in at each, whatever each has selected:
	// lines are indented for one selection only.
	d = docOf("ab\ncd\nef")
	d.extra = []Range{{Caret: Pos{2, 1}, Anchor: Pos{2, 1}}}
	d.caret, d.anchor = Pos{1, 1}, Pos{0, 1}
	d.Tab()
	if got := d.Text(); got != "a   d\ne   f" {
		t.Errorf("tab at each: %q", got)
	}
	// A step made at one caret, undone, leaves one caret, whatever was
	// added since.
	d = docOf("ab\ncd")
	d.SetCaret(Pos{0, 1}, false)
	d.Insert("X")
	d.AddCaret(Pos{1, 1})
	if !d.Undo() || d.Multiple() || marked(d) != "a‖b⏎cd" {
		t.Errorf("%s", marked(d))
	}
	if !d.Redo() || d.Multiple() || marked(d) != "aX‖b⏎cd" {
		t.Errorf("%s", marked(d))
	}
}

func TestCaretsAreAddedTakenAwayAndCollapsed(t *testing.T) {
	d := docOf("abcd\nab\nabcd")
	d.SetCaret(Pos{0, 3}, false)
	// Below, at the same column, through a line too short for it.
	d.AddCaretLine(1)
	d.AddCaretLine(1)
	if got := marked(d); got != "abc|d⏎ab|⏎abc‖d" {
		t.Fatalf("carets below: %s", got)
	}
	d.AddCaretLine(1) // there is no line below
	if len(d.Carets()) != 3 {
		t.Errorf("a caret past the end: %s", marked(d))
	}
	d = docOf("abcd\nab\nabcd")
	d.SetCaret(Pos{2, 3}, false)
	d.AddCaretLine(-1)
	d.AddCaretLine(-1)
	d.AddCaretLine(-1)
	if got := marked(d); got != "abc‖d⏎ab|⏎abc|d" {
		t.Errorf("carets above: %s", got)
	}
	// Clicked again, a caret goes; the first, and another becomes it; the
	// last one stays.
	d.AddCaret(Pos{1, 2})
	if got := marked(d); got != "abc‖d⏎ab⏎abc|d" {
		t.Errorf("one taken away: %s", got)
	}
	d.AddCaret(Pos{0, 3})
	if got := marked(d); got != "abcd⏎ab⏎abc‖d" || d.Multiple() {
		t.Errorf("the first taken away: %s", got)
	}
	d.AddCaret(Pos{2, 3})
	if got := marked(d); got != "abcd⏎ab⏎abc‖d" {
		t.Errorf("the only caret is not taken away: %s", got)
	}
	// Past the text, a caret is put at the nearest place there is.
	d.AddCaret(Pos{9, 9})
	if got := marked(d); got != "abcd⏎ab⏎abc|d‖" {
		t.Errorf("%s", got)
	}
	// Placing the caret, selecting everything or a word, and setting the
	// text each leave one; so does Escape.
	for name, one := range map[string]func(){
		"a click":    func() { d.SetCaret(Pos{0, 0}, false) },
		"⇧-click":    func() { d.SetCaret(Pos{0, 2}, true) },
		"select all": func() { d.SelectAll() },
		"a word":     func() { d.SelectWord(Pos{0, 1}) },
		"new text":   func() { d.SetText("x\ny") },
		"escape":     func() { d.Collapse() },
		"indenting":  func() { d.IndentLines() },
	} {
		d.SetCaret(Pos{0, 0}, false)
		d.AddCaret(Pos{1, 1})
		if !d.Multiple() {
			t.Fatalf("%s: no second caret to begin with", name)
		}
		if one(); d.Multiple() {
			t.Errorf("after %s there is more than one caret: %s", name, marked(d))
		}
	}
}

func TestABlockIsCaretsOnConsecutiveLines(t *testing.T) {
	d := docOf("abcdef\nab\n\nabcdef")
	d.SelectBlock(Pos{0, 1}, Pos{3, 4})
	// A line that ends inside the block has what it has of it selected, and
	// one too short to reach it has a caret at its end.
	if got := marked(d); got != "a[bcd]|ef⏎a[b]|⏎|⏎a[bcd]‖ef" {
		t.Errorf("a block: %s", got)
	}
	d = docOf("abcdef\nab\nabcdef")
	// Dragged upwards and leftwards, the carets are at the block's left and
	// the line the drag reached holds the first.
	d.SelectBlock(Pos{2, 4}, Pos{0, 1})
	if got := marked(d); got != "a‖[bcd]ef⏎a|[b]⏎a|[bcd]ef" {
		t.Errorf("a block dragged back: %s", got)
	}
	// Past the text, a block reaches as far as there is text.
	d.SelectBlock(Pos{1, 1}, Pos{9, 9})
	if got := marked(d); got != "abcdef⏎a[b]|⏎a[bcdef]‖" {
		t.Errorf("a block past the end: %s", got)
	}
	// One line is one selection; no width is carets down a column.
	d.SelectBlock(Pos{1, 0}, Pos{1, 2})
	if got := marked(d); got != "abcdef⏎[ab]‖⏎abcdef" || d.Multiple() {
		t.Errorf("a block of one line: %s", got)
	}
	d.SelectBlock(Pos{0, 1}, Pos{2, 1})
	if got := marked(d); got != "a|bcdef⏎a|b⏎a‖bcdef" {
		t.Errorf("a block of no width: %s", got)
	}
	// What was copied from a block is put back a line to each caret; any
	// other text goes in whole at each.
	d.Paste("1\r\n2\r\n3") // as another program's clipboard may break lines
	if got := d.Text(); got != "a1bcdef\na2b\na3bcdef" {
		t.Errorf("dealt out: %q", got)
	}
	d.Paste("x\ny")
	if got := d.Text(); got != "a1x\nybcdef\na2x\nyb\na3x\nybcdef" {
		t.Errorf("whole at each: %q", got)
	}
	d = docOf("ab")
	d.SetCaret(Pos{0, 1}, false)
	d.Paste("x\r\ny")
	if got := d.Text(); got != "ax\nyb" {
		t.Errorf("one caret pastes as it always did: %q", got)
	}
}

func TestAPositionIsCarriedByAChangeAboveIt(t *testing.T) {
	for _, c := range []struct {
		at                Pos
		removed, inserted string
		p, want           Pos
	}{
		{Pos{0, 2}, "", "xy", Pos{0, 2}, Pos{0, 4}},     // typed just before it
		{Pos{0, 2}, "", "xy", Pos{0, 5}, Pos{0, 7}},     // further along the line
		{Pos{0, 2}, "", "xy", Pos{1, 5}, Pos{1, 5}},     // a line below: not at all
		{Pos{0, 2}, "", "x\ny", Pos{0, 5}, Pos{1, 4}},   // a line break before it
		{Pos{0, 2}, "", "x\ny", Pos{3, 5}, Pos{4, 5}},   // and below
		{Pos{0, 2}, "ab", "", Pos{0, 4}, Pos{0, 2}},     // deleted up to it
		{Pos{0, 2}, "ab\ncd", "", Pos{1, 3}, Pos{0, 3}}, // lines joined before it
		{Pos{0, 2}, "ab\ncd", "", Pos{4, 3}, Pos{3, 3}},
		{Pos{0, 2}, "ab", "wxyz", Pos{0, 4}, Pos{0, 6}}, // replaced
		{Pos{1, 0}, "", "q", Pos{0, 9}, Pos{0, 9}},      // before the change
		{Pos{0, 5}, "ab", "", Pos{0, 3}, Pos{0, 3}},     // before it on its own line
	} {
		ch := change{at: c.at, removed: c.removed, inserted: c.inserted}
		if got := carried(c.p, ch); got != c.want {
			t.Errorf("%v after %+v at %v is %v, not %v", c.p, []string{c.removed, c.inserted}, c.at, got, c.want)
		}
	}
	if fmt.Sprint(Range{Caret: Pos{0, 1}, Anchor: Pos{0, 3}}.From(), Range{Caret: Pos{0, 1}, Anchor: Pos{0, 3}}.To()) != fmt.Sprint(Pos{0, 1}, Pos{0, 3}) {
		t.Error("a range's ends, in order")
	}
}
