package view

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlcomplete"
	"github.com/ikigai-db/ikigai-db/internal/sqllex"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
)

// completing sets up an editor whose completer is the real engine over a
// small catalog, and returns it. The engine is what the shell wires in, so
// the test exercises the whole path from a keystroke to an insert.
func completing(t *testing.T, text string) (*Editor, fyne.Window, *int) {
	t.Helper()
	e, w := setup(t, text)
	cat := sqlcomplete.NewStatic()
	cat.AddObject("shop", "public", sqlcomplete.Object{Name: "orders"},
		sqlcomplete.Column{Name: "id", Type: "integer"},
		sqlcomplete.Column{Name: "order_date", Type: "date"},
	)
	cat.AddObject("shop", "public", sqlcomplete.Object{Name: "order_items"},
		sqlcomplete.Column{Name: "qty", Type: "integer"},
	)
	eng := sqlcomplete.New(sqllex.PostgreSQL, cat, nil)
	asked := 0
	e.SetCompleter(func(text string, cursor int) source.CompletionResult {
		asked++
		return eng.Complete(source.CompletionRequest{
			Text: text, Cursor: cursor, Database: "shop", Schema: "public", Limit: 20,
		})
	})
	return e, w, &asked
}

// newPos is a position in the document.
func newPos(line, col int) editor.Pos { return editor.Pos{Line: line, Col: col} }

// shown is the labels the popup is offering.
func shown(e *Editor) []string {
	out := make([]string, 0, len(e.comp.items))
	for _, it := range e.comp.items {
		out = append(out, it.Label)
	}
	return out
}

func TestCompletionOpensAsAWordIsTyped(t *testing.T) {
	e, _, asked := completing(t, "")
	test.Type(e.surface, "select * from ord")
	if !e.CompletionOpen() {
		t.Fatal("the popup is not open after typing a table's first letters")
	}
	if got := shown(e); len(got) == 0 || got[0] != "orders" {
		t.Errorf("offering %v, want orders first", got)
	}
	if *asked == 0 {
		t.Error("the completer was never asked")
	}
	// A space ends the word, and the popup with it.
	test.Type(e.surface, " ")
	if e.CompletionOpen() {
		t.Errorf("the popup stayed open past the word: %v", shown(e))
	}
}

func TestCompletionDoesNotOpenWhereThereIsNothingToOffer(t *testing.T) {
	e, _, _ := completing(t, "")
	test.Type(e.surface, "select * from zzz")
	if e.CompletionOpen() {
		t.Errorf("the popup opened with nothing to offer: %v", shown(e))
	}
}

func TestCompletionIsNotOfferedWithoutACompleter(t *testing.T) {
	e, _ := setup(t, "")
	test.Type(e.surface, "select * from ord")
	if e.CompletionOpen() {
		t.Error("the popup opened with no completer set")
	}
	// Setting one and unsetting it again closes what is open.
	e2, _, _ := completing(t, "")
	test.Type(e2.surface, "sel")
	if !e2.CompletionOpen() {
		t.Fatal("the popup is not open")
	}
	e2.SetCompleter(nil)
	if e2.CompletionOpen() {
		t.Error("the popup stayed open after completion was turned off")
	}
}

func TestCompletionMovesThroughItsCandidates(t *testing.T) {
	e, _, _ := completing(t, "")
	test.Type(e.surface, "select * from order")
	first := shown(e)
	if len(first) < 2 {
		t.Fatalf("offering %v, want at least two", first)
	}
	if e.comp.sel != 0 {
		t.Errorf("chosen %d, want the first", e.comp.sel)
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	if e.comp.sel != 1 {
		t.Errorf("chosen %d after Down, want the second", e.comp.sel)
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyUp})
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyUp})
	if e.comp.sel != len(first)-1 {
		t.Errorf("chosen %d after moving up past the first, want the last", e.comp.sel)
	}
	// The arrow keys did not move the caret while the popup was open.
	if got := e.Document().Text(); got != "select * from order" {
		t.Errorf("text %q, want it untouched", got)
	}
	if c := e.Document().Caret(); c.Col != len("select * from order") {
		t.Errorf("caret at %d, want it where typing left it", c.Col)
	}
}

func TestCompletionAcceptsOnReturnAndTab(t *testing.T) {
	for _, key := range []fyne.KeyName{fyne.KeyReturn, fyne.KeyTab} {
		e, _, _ := completing(t, "")
		test.Type(e.surface, "select * from ord")
		e.surface.TypedKey(&fyne.KeyEvent{Name: key})
		if got := e.Document().Text(); got != "select * from orders" {
			t.Errorf("%v: text %q, want the candidate written in", key, got)
		}
		if e.CompletionOpen() {
			t.Errorf("%v: the popup stayed open after accepting", key)
		}
		if c := e.Document().Caret(); c.Col != len("select * from orders") {
			t.Errorf("%v: caret at %d, want it after what was written", key, c.Col)
		}
	}
}

func TestCompletionAcceptsWhatIsChosen(t *testing.T) {
	e, _, _ := completing(t, "")
	test.Type(e.surface, "select * from order")
	want := shown(e)[1]
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if got := e.Document().Text(); got != "select * from "+want {
		t.Errorf("text %q, want the second candidate %q", got, want)
	}
}

func TestCompletionReplacesTheWholeWordBeingTyped(t *testing.T) {
	e, _, _ := completing(t, "select * from orders")
	// The caret in the middle of the word: accepting replaces all of it,
	// rather than leaving its tail behind.
	e.Document().SetCaret(newPos(0, len("select * from ord")), false)
	e.Complete()
	if !e.CompletionOpen() {
		t.Fatal("the popup is not open")
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if got := e.Document().Text(); got != "select * from orders" {
		t.Errorf("text %q, want the word replaced whole", got)
	}
}

func TestCompletionWritesAnAcceptedCandidateAsOneUndo(t *testing.T) {
	e, _, _ := completing(t, "")
	changes := 0
	e.OnChanged = func() { changes++ }
	test.Type(e.surface, "select * from ord")
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if got := e.Document().Text(); got != "select * from orders" {
		t.Fatalf("text %q", got)
	}
	if changes != len("select * from ord")+1 {
		t.Errorf("%d change reports, want one for the insert", changes)
	}
	e.Document().Undo()
	if got := e.Document().Text(); got != "select * from ord" {
		t.Errorf("undone to %q, want the word as it was typed", got)
	}
}

func TestCompletionClosesOnEscapeAndOnMoving(t *testing.T) {
	e, _, _ := completing(t, "")
	test.Type(e.surface, "select * from ord")
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if e.CompletionOpen() {
		t.Error("Escape did not close the popup")
	}
	// Escape did not reach the document.
	if got := e.Document().Text(); got != "select * from ord" {
		t.Errorf("text %q", got)
	}
	// Typing again opens it; an arrow key then closes it.
	test.Type(e.surface, "e")
	if !e.CompletionOpen() {
		t.Fatal("typing after Escape did not open the popup again")
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyLeft})
	if e.CompletionOpen() {
		t.Error("moving the caret did not close the popup")
	}
	// A click closes it too.
	test.Type(e.surface, "r")
	if !e.CompletionOpen() {
		t.Fatal("the popup is not open")
	}
	e.surface.MouseDown(&desktop.MouseEvent{})
	if e.CompletionOpen() {
		t.Error("a click did not close the popup")
	}
}

func TestCompletionFollowsWhatIsDeleted(t *testing.T) {
	e, _, _ := completing(t, "")
	test.Type(e.surface, "select * from order_i")
	if got := shown(e); len(got) != 1 || got[0] != "order_items" {
		t.Fatalf("offering %v, want order_items alone", got)
	}
	for i := 0; i < 2; i++ {
		e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyBackspace})
	}
	if got := shown(e); len(got) < 2 {
		t.Errorf("offering %v after deleting, want both tables again", got)
	}
	// Deleting where nothing is open does not open anything.
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyBackspace})
	if e.CompletionOpen() {
		t.Error("deleting opened the popup")
	}
}

func TestCompletionOpensAfterADot(t *testing.T) {
	e, _, _ := completing(t, "")
	test.Type(e.surface, "select * from orders o where o.")
	if !e.CompletionOpen() {
		t.Fatal("the popup did not open after a dot")
	}
	if got := shown(e); len(got) != 2 || got[0] != "id" {
		t.Errorf("offering %v, want the aliased table's columns", got)
	}
	test.Type(e.surface, "select 1 ")
	if e.CompletionOpen() {
		t.Error("the popup stayed open after a space")
	}
}

func TestCompletionExplicitlyAsked(t *testing.T) {
	e, _, _ := completing(t, "select * from ")
	e.Document().SetCaret(newPos(0, len("select * from ")), false)
	if e.CompletionOpen() {
		t.Fatal("the popup is open before it was asked for")
	}
	e.Complete()
	if !e.CompletionOpen() {
		t.Fatal("the popup did not open when asked")
	}
	if got := shown(e); len(got) == 0 {
		t.Error("nothing offered where every table can go")
	}
}

func TestCompletionAcceptsQuotedAndQualifiedInserts(t *testing.T) {
	e, _ := setup(t, "")
	e.SetCompleter(func(string, int) source.CompletionResult {
		return source.CompletionResult{
			Candidates: []source.Completion{{Kind: source.CompletionColumn, Label: "Customer Id", Insert: `"Customer Id"`}},
			Start:      0, End: 1,
		}
	})
	test.Type(e.surface, "c")
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if got := e.Document().Text(); got != `"Customer Id"` {
		t.Errorf("text %q, want what the candidate said to write", got)
	}
}

func TestCompletionDrawsWhatItOffers(t *testing.T) {
	e, _, _ := completing(t, "")
	test.Type(e.surface, "select * from ord")
	r := test.TempWidgetRenderer(t, e.comp)
	r.Refresh()
	var words []string
	for _, o := range r.Objects() {
		if txt, ok := o.(*canvas.Text); ok && txt.Text != "" {
			words = append(words, txt.Text)
		}
	}
	joined := strings.Join(words, " ")
	for _, want := range []string{"orders", "order_items", "table", "public"} {
		if !strings.Contains(joined, want) {
			t.Errorf("drawn %q, want %q in it", joined, want)
		}
	}
}

func TestCompletionDrawsAWindowOverALongList(t *testing.T) {
	e, _ := setup(t, "")
	var many []source.Completion
	for _, name := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10", "a11", "a12"} {
		many = append(many, source.Completion{Kind: source.CompletionTable, Label: name, Insert: name})
	}
	e.SetCompleter(func(string, int) source.CompletionResult {
		return source.CompletionResult{Candidates: many, Start: 0, End: 1}
	})
	test.Type(e.surface, "a")
	if got := e.comp.rows(); got != completionRows {
		t.Errorf("%d rows drawn, want %d", got, completionRows)
	}
	// Moving past the last drawn row scrolls the window.
	for i := 0; i < completionRows; i++ {
		e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	}
	if e.comp.top == 0 {
		t.Errorf("the drawn window did not follow the selection (top %d)", e.comp.top)
	}
	if e.comp.sel < e.comp.top || e.comp.sel >= e.comp.top+completionRows {
		t.Errorf("chosen %d is outside the drawn window [%d,%d)", e.comp.sel, e.comp.top, e.comp.top+completionRows)
	}
	// Page keys move a windowful.
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyPageUp})
	if e.comp.sel != 0 {
		t.Errorf("chosen %d after Page Up, want the first", e.comp.sel)
	}
}

func TestCompletionSpeaksTheChosenCandidate(t *testing.T) {
	e, _, _ := completing(t, "")
	test.Type(e.surface, "select * from ord")
	got := e.comp.AccessibilityLabel()
	if !strings.HasPrefix(got, "orders, table, public, 1 of ") {
		t.Errorf("spoken %q, want the candidate, its kind, its detail and its place", got)
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	if got := e.comp.AccessibilityLabel(); !strings.Contains(got, "2 of ") {
		t.Errorf("spoken %q after moving, want the second", got)
	}
	if e.comp.AccessibilityRole() != fyne.AccessibleRoleContainer {
		t.Errorf("role %q", e.comp.AccessibilityRole())
	}
}

func TestCompletionSitsUnderTheWordBeingTyped(t *testing.T) {
	e, w, _ := completing(t, "")
	w.Resize(fyne.NewSize(600, 400))
	test.Type(e.surface, "select * from ord")
	e.Refresh()
	m := currentMetrics()
	pos := e.comp.Position()
	wantX := pad + float32(len("select * from "))*m.cw + e.gutter.Size().Width
	if diff := pos.X - wantX; diff > 1 || diff < -1 {
		t.Errorf("popup at x=%g, want it at the word's start x=%g", pos.X, wantX)
	}
	if pos.Y <= pad {
		t.Errorf("popup at y=%g, want it below the line", pos.Y)
	}
}

func TestCompletionOpensAboveWhereThereIsNoRoomBelow(t *testing.T) {
	e, w, _ := completing(t, strings.Repeat("\n", 40))
	w.Resize(fyne.NewSize(600, 200))
	e.Document().SetCaret(newPos(40, 0), false)
	test.Type(e.surface, "select * from ord")
	e.Refresh()
	m := currentMetrics()
	if got := e.comp.Position().Y + e.comp.Size().Height; got > e.Size().Height+1 {
		t.Errorf("popup ends at y=%g, past the editor's %g", got, e.Size().Height)
	}
	// It opens above the line being typed, rather than over it.
	line := pad + float32(e.Document().Caret().Line)*m.lh - e.scroll.Offset.Y
	if got := e.comp.Position().Y + e.comp.Size().Height; got > line+1 {
		t.Errorf("popup ends at y=%g, over the line being typed at y=%g", got, line)
	}
}

func TestCompletionCountsTheCursorInRunes(t *testing.T) {
	e, _ := setup(t, "")
	var gotText string
	var gotCursor int
	e.SetCompleter(func(text string, cursor int) source.CompletionResult {
		gotText, gotCursor = text, cursor
		return source.CompletionResult{
			Candidates: []source.Completion{{Kind: source.CompletionColumn, Label: "naïveté", Insert: "naïveté"}},
			Start:      cursor - 2, End: cursor,
		}
	})
	// The comment holds a two-byte rune, so a byte offset taken for a rune
	// one would place the cursor, and what it replaces, a character out.
	e.Document().SetText("-- ü\n")
	e.Document().SetCaret(newPos(1, 0), false)
	test.Type(e.surface, "na")
	if gotText != "-- ü\nna" || gotCursor != 7 {
		t.Errorf("asked about %q at %d, want %q at 7 runes", gotText, gotCursor, "-- ü\nna")
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if got := e.Document().Text(); got != "-- ü\nnaïveté" {
		t.Errorf("text %q, want the word replaced where it is", got)
	}
}

func TestCompletionClosesOnAShortcutAndOnLosingTheFocus(t *testing.T) {
	e, _, _ := completing(t, "")
	test.Type(e.surface, "select * from ord")
	e.surface.TypedShortcut(&fyne.ShortcutSelectAll{})
	if e.CompletionOpen() {
		t.Error("a shortcut did not close the popup")
	}
	e.Document().SetCaret(newPos(0, len("select * from ord")), false)
	test.Type(e.surface, "e")
	if !e.CompletionOpen() {
		t.Fatal("the popup is not open")
	}
	e.surface.FocusLost()
	if e.CompletionOpen() {
		t.Error("losing the focus did not close the popup")
	}
}

func TestCompletionMarksTheChosenRow(t *testing.T) {
	e, _, _ := completing(t, "")
	test.Type(e.surface, "select * from ord")
	r := test.TempWidgetRenderer(t, e.comp)
	filled := func() []int {
		r.Refresh()
		var out []int
		row := 0
		for _, o := range r.Objects() {
			rect, ok := o.(*canvas.Rectangle)
			if !ok {
				continue
			}
			if rect == test.WidgetRenderer(e.comp).(*completionsRenderer).bg {
				continue
			}
			if _, _, _, a := rect.FillColor.RGBA(); a > 0 {
				out = append(out, row)
			}
			row++
		}
		return out
	}
	if got := filled(); len(got) != 1 || got[0] != 0 {
		t.Errorf("rows filled %v, want the first alone", got)
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	if got := filled(); len(got) != 1 || got[0] != 1 {
		t.Errorf("rows filled %v after moving, want the second alone", got)
	}
}

func TestCompletionRefreshesWithoutLosingWhatWasChosen(t *testing.T) {
	e, _ := setup(t, "")
	items := []source.Completion{
		{Kind: source.CompletionKeyword, Label: "SELECT", Insert: "select"},
		{Kind: source.CompletionKeyword, Label: "SET", Insert: "set"},
	}
	e.SetCompleter(func(string, int) source.CompletionResult {
		return source.CompletionResult{Candidates: items, Start: 0, End: 1}
	})
	test.Type(e.surface, "s")
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	if e.comp.sel != 1 {
		t.Fatalf("chosen %d, want the second", e.comp.sel)
	}
	// The cache has loaded: more candidates, and the chosen one moves down
	// the list. What was about to be accepted is still what is chosen.
	items = append([]source.Completion{{Kind: source.CompletionTable, Label: "sales", Insert: "sales"}}, items...)
	e.RefreshCompletion()
	if got := e.comp.items[e.comp.sel].Label; got != "SET" {
		t.Errorf("chosen %q after the list grew, want SET", got)
	}
	// Closed, it stays closed.
	e.comp.dismiss()
	e.RefreshCompletion()
	if e.CompletionOpen() {
		t.Error("refreshing opened the popup")
	}
}
