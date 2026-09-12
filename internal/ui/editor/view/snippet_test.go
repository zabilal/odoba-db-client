package view

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// snippeting sets up an editor offering one snippet under "sn".
func snippeting(t *testing.T, body string) *Editor {
	t.Helper()
	e, _ := setup(t, "")
	e.SetCompleter(func(text string, cursor int) source.CompletionResult {
		// Only the trigger is offered, so that typing into a place being
		// filled in does not open the popup over it.
		runes := []rune(text)
		if cursor < 2 || string(runes[cursor-2:cursor]) != "sn" {
			return source.CompletionResult{Start: cursor, End: cursor}
		}
		return source.CompletionResult{
			Candidates: []source.Completion{{Kind: source.CompletionSnippet, Label: "sn", Insert: body}},
			Start:      cursor - 2, End: cursor,
		}
	})
	return e
}

// writeSnippet types the trigger and accepts the snippet.
func writeSnippet(t *testing.T, e *Editor) {
	t.Helper()
	test.Type(e.surface, "sn")
	if !e.CompletionOpen() {
		t.Fatal("the popup is not open")
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
}

// selected is the selected text, or the caret's offset where nothing is.
func selected(e *Editor) (string, int) {
	d := e.Document()
	return d.SelectedText(), d.Offset(d.Caret())
}

func TestParseSnippetReadsThePlacesToFillIn(t *testing.T) {
	text, marks := parseSnippet("SELECT ${1:*}\nFROM ${2:table}$0")
	if text != "SELECT *\nFROM table" {
		t.Errorf("text %q, want the places written out", text)
	}
	if len(marks) != 3 {
		t.Fatalf("%d places, want three", len(marks))
	}
	stops := order(marks, 0)
	if len(stops) != 3 {
		t.Fatalf("%d stops, want three", len(stops))
	}
	if got := text[stops[0].start:stops[0].end]; got != "*" {
		t.Errorf("the first place holds %q, want %q", got, "*")
	}
	if got := text[stops[1].start:stops[1].end]; got != "table" {
		t.Errorf("the second place holds %q, want %q", got, "table")
	}
	// $0 is last however it was written, and holds nothing.
	if stops[2].start != len(text) || stops[2].end != len(text) {
		t.Errorf("the last stop is [%d,%d), want the end at %d", stops[2].start, stops[2].end, len(text))
	}
}

func TestParseSnippetOrdersByNumberWithTheCaretLast(t *testing.T) {
	text, marks := parseSnippet("a $0 b ${2:two} c ${1:one}")
	if text != "a  b two c one" {
		t.Errorf("text %q", text)
	}
	stops := order(marks, 0)
	if len(stops) != 3 {
		t.Fatalf("%d stops, want three", len(stops))
	}
	if got := text[stops[0].start:stops[0].end]; got != "one" {
		t.Errorf("first %q, want one", got)
	}
	if got := text[stops[1].start:stops[1].end]; got != "two" {
		t.Errorf("second %q, want two", got)
	}
	if stops[2].start != 2 {
		t.Errorf("the caret's place is at %d, want where $0 was written", stops[2].start)
	}
}

func TestParseSnippetKeepsTheFirstOfARepeatedPlace(t *testing.T) {
	// A CTE's name is written twice and filled in once.
	text, marks := parseSnippet("WITH ${1:name} AS (x) SELECT * FROM ${1:name}")
	if text != "WITH name AS (x) SELECT * FROM name" {
		t.Errorf("text %q, want the name written at both", text)
	}
	stops := order(marks, 0)
	if len(stops) != 1 || stops[0].start != 5 {
		t.Errorf("stops %v, want the first alone", stops)
	}
}

func TestParseSnippetLeavesTextAlone(t *testing.T) {
	for _, body := range []string{"cost $ 5", "select $ from t", "${x}", "${1", "${1:unclosed", "a $ b"} {
		text, marks := parseSnippet(body)
		if text != body || len(marks) != 0 {
			t.Errorf("%q read as %q with %d places, want it left alone", body, text, len(marks))
		}
	}
	// A place with no text of its own still marks a point.
	text, marks := parseSnippet("a${1}b")
	if text != "ab" || len(marks) != 1 || marks[0].start != 1 || marks[0].end != 1 {
		t.Errorf("%q read as %q with %v", "a${1}b", text, marks)
	}
}

func TestSnippetIsWrittenWithItsFirstPlaceSelected(t *testing.T) {
	e := snippeting(t, "SELECT ${1:*}\nFROM ${2:table}")
	writeSnippet(t, e)
	if got := e.Document().Text(); got != "SELECT *\nFROM table" {
		t.Fatalf("text %q, want the snippet written out", got)
	}
	if sel, _ := selected(e); sel != "*" {
		t.Errorf("selected %q, want the first place", sel)
	}
	if !e.FillingSnippet() {
		t.Error("the snippet is not being filled in")
	}
}

func TestSnippetStepsThroughItsPlacesOnTab(t *testing.T) {
	e := snippeting(t, "SELECT ${1:*}\nFROM ${2:table}$0")
	writeSnippet(t, e)
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyTab})
	if sel, _ := selected(e); sel != "table" {
		t.Errorf("selected %q after Tab, want the second place", sel)
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyTab})
	if sel, at := selected(e); sel != "" || at != len("SELECT *\nFROM table") {
		t.Errorf("selected %q at %d after the last place, want the caret at the end", sel, at)
	}
	if e.FillingSnippet() {
		t.Error("the snippet is still being filled in at its last place")
	}
	// Tab indents again once there is nowhere left to go.
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyTab})
	if got := e.Document().Text(); !strings.HasSuffix(got, "table  ") {
		t.Errorf("text %q, want Tab to indent again", got)
	}
}

func TestSnippetTypingAtOnePlaceMovesTheRest(t *testing.T) {
	e := snippeting(t, "SELECT ${1:*}\nFROM ${2:table}")
	writeSnippet(t, e)
	test.Type(e.surface, "id, name") // replaces the selected *
	if got := e.Document().Text(); got != "SELECT id, name\nFROM table" {
		t.Fatalf("text %q", got)
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyTab})
	if sel, _ := selected(e); sel != "table" {
		t.Errorf("selected %q, want the place that moved along with what was typed", sel)
	}
	// And when what was typed is shorter than what was there.
	e2 := snippeting(t, "SELECT ${1:columns}\nFROM ${2:table}")
	writeSnippet(t, e2)
	test.Type(e2.surface, "x")
	e2.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyTab})
	if sel, _ := selected(e2); sel != "table" {
		t.Errorf("selected %q after a shorter fill, want the next place", sel)
	}
}

func TestSnippetEndsWhenTheCaretGoesElsewhere(t *testing.T) {
	e := snippeting(t, "SELECT ${1:*}\nFROM ${2:table}")
	writeSnippet(t, e)
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	if e.FillingSnippet() {
		t.Error("moving the caret left the snippet being filled in")
	}
	e2 := snippeting(t, "SELECT ${1:*}\nFROM ${2:table}")
	writeSnippet(t, e2)
	e2.surface.MouseDown(&desktop.MouseEvent{})
	if e2.FillingSnippet() {
		t.Error("a click left the snippet being filled in")
	}
	e3 := snippeting(t, "SELECT ${1:*}\nFROM ${2:table}")
	writeSnippet(t, e3)
	e3.surface.FocusLost()
	if e3.FillingSnippet() {
		t.Error("losing the focus left the snippet being filled in")
	}
}

func TestSnippetWithNoPlacesIsJustText(t *testing.T) {
	e := snippeting(t, "COMMIT")
	writeSnippet(t, e)
	if got := e.Document().Text(); got != "COMMIT" {
		t.Errorf("text %q", got)
	}
	if e.FillingSnippet() {
		t.Error("a snippet with nothing to fill in is being filled in")
	}
}

func TestSnippetUndoesInOneStep(t *testing.T) {
	e := snippeting(t, "SELECT ${1:*}\nFROM ${2:table}")
	writeSnippet(t, e)
	e.Document().Undo()
	if got := e.Document().Text(); got != "sn" {
		t.Errorf("undone to %q, want the trigger as it was typed", got)
	}
}

func TestOnlyASnippetsInsertIsReadForPlaces(t *testing.T) {
	// A table called ${1:x} is a table, not a place to fill in. Nothing but
	// a snippet's insert is read that way.
	e, _ := setup(t, "")
	e.SetCompleter(func(text string, cursor int) source.CompletionResult {
		return source.CompletionResult{
			Candidates: []source.Completion{{Kind: source.CompletionTable, Label: "odd", Insert: "${1:odd}"}},
			Start:      cursor - 2, End: cursor,
		}
	})
	test.Type(e.surface, "sn")
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if got := e.Document().Text(); got != "${1:odd}" {
		t.Errorf("text %q, want it written as it is", got)
	}
	if e.FillingSnippet() {
		t.Error("a table's name began a snippet")
	}
}

func TestSnippetFromTheRealEngine(t *testing.T) {
	// The snippets the engine offers, written and stepped through as they
	// reach the editor.
	e, _, _ := completing(t, "")
	test.Type(e.surface, "ins")
	if !e.CompletionOpen() {
		t.Fatal("the popup is not open")
	}
	for e.comp.items[e.comp.sel].Kind != source.CompletionSnippet {
		e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	got := e.Document().Text()
	if !strings.HasPrefix(got, "insert into table") || !strings.Contains(got, "values (values)") {
		t.Fatalf("text %q, want the insert snippet written out", got)
	}
	if sel, _ := selected(e); sel != "table" {
		t.Errorf("selected %q, want the first place", sel)
	}
	test.Type(e.surface, "orders")
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape}) // the popup, not the snippet
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyTab})
	if sel, _ := selected(e); sel != "columns" {
		t.Errorf("selected %q after Tab, want the next place", sel)
	}
	if !strings.HasPrefix(e.Document().Text(), "insert into orders (columns)") {
		t.Errorf("text %q, want what was typed in the first place", e.Document().Text())
	}
}
