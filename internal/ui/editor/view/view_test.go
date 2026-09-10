package view

import (
	"runtime"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// setup shows an editor in a test window, with the application's theme: Fyne's
// default theme measures text differently.
func setup(t *testing.T, text string) (*Editor, fyne.Window) {
	t.Helper()
	a := test.NewTempApp(t)
	a.Settings().SetTheme(uitheme.New())
	e := New(editor.NewDocument(text, sqllex.PostgreSQL), uitheme.Light)
	w := test.NewTempWindow(t, e)
	w.Resize(fyne.NewSize(600, 400))
	e.Focus()
	return e, w
}

func texts(objs []fyne.CanvasObject) []*canvas.Text {
	var out []*canvas.Text
	for _, o := range objs {
		if t, ok := o.(*canvas.Text); ok {
			out = append(out, t)
		}
	}
	return out
}

func surfaceObjects(t *testing.T, e *Editor) []fyne.CanvasObject {
	t.Helper()
	r := test.TempWidgetRenderer(t, e.surface)
	r.Refresh()
	return r.Objects()
}

func TestTypingEditsTheDocumentAndReportsChanges(t *testing.T) {
	e, _ := setup(t, "")
	changes := 0
	e.OnChanged = func() { changes++ }
	test.Type(e.surface, "select 1")
	if got := e.Document().Text(); got != "select 1" {
		t.Fatalf("text %q", got)
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyLeft})
	if changes != 8 {
		t.Errorf("%d change reports for 8 keystrokes and a caret move", changes)
	}
}

func TestShiftArrowsSelect(t *testing.T) {
	e, _ := setup(t, "SELECT 1")
	e.surface.KeyDown(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	e.surface.KeyUp(&fyne.KeyEvent{Name: desktop.KeyShiftLeft})
	if got := e.Document().SelectedText(); got != "SE" {
		t.Fatalf("selected %q", got)
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	if _, _, sel := e.Document().Selection(); sel {
		t.Error("an arrow without Shift should collapse the selection")
	}
}

func TestTabIndentsInsteadOfMovingFocus(t *testing.T) {
	e, _ := setup(t, "x")
	if !e.surface.AcceptsTab() {
		t.Fatal("Tab would move the focus out of the editor")
	}
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyTab})
	if got := e.Document().Text(); got != "    x" {
		t.Errorf("text %q", got)
	}
}

func TestWordAndDocumentChords(t *testing.T) {
	e, _ := setup(t, "SELECT foo\nFROM bar")
	d := e.Document()
	e.surface.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyRight, Modifier: wordModifier()})
	if c := d.Caret(); c != (editor.Pos{Line: 0, Col: 6}) {
		t.Errorf("word right → %v", c)
	}
	end := &desktop.CustomShortcut{KeyName: fyne.KeyDown, Modifier: fyne.KeyModifierShortcutDefault}
	if runtime.GOOS != "darwin" {
		end.KeyName = fyne.KeyEnd
	}
	e.surface.TypedShortcut(end)
	if c := d.Caret(); c != (editor.Pos{Line: 1, Col: 8}) {
		t.Errorf("document end → %v", c)
	}
	e.surface.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyBackspace, Modifier: wordModifier()})
	if got := d.Text(); got != "SELECT foo\nFROM " {
		t.Errorf("delete word → %q", got)
	}
}

func TestEveryReservedChordIsHandled(t *testing.T) {
	e, _ := setup(t, "x")
	for _, sc := range Reserved() {
		if cs, ok := sc.(*desktop.CustomShortcut); ok && e.surface.chord(cs) == nil {
			t.Errorf("%s is reserved but the editor ignores it", cs.ShortcutName())
		}
	}
}

func TestClipboard(t *testing.T) {
	e, w := setup(t, "SELECT 1")
	cb := w.Clipboard()
	d := e.Document()
	d.SelectWord(editor.Pos{Line: 0, Col: 2})
	e.surface.TypedShortcut(&fyne.ShortcutCopy{Clipboard: cb})
	if cb.Content() != "SELECT" {
		t.Fatalf("copied %q", cb.Content())
	}
	e.surface.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyRight, Modifier: fyne.KeyModifierShortcutDefault})
	if runtime.GOOS != "darwin" {
		d.Move(editor.LineEnd, false)
	}
	e.surface.TypedShortcut(&fyne.ShortcutPaste{Clipboard: cb})
	if got := d.Text(); got != "SELECT 1SELECT" {
		t.Errorf("pasted into %q", got)
	}
	e.surface.TypedShortcut(&fyne.ShortcutUndo{})
	if got := d.Text(); got != "SELECT 1" {
		t.Errorf("undo left %q", got)
	}
}

func TestClickPlacesTheCaretAndDragSelects(t *testing.T) {
	e, _ := setup(t, "SELECT 1\nFROM t")
	m := currentMetrics()
	point := func(line, col int) fyne.Position {
		return fyne.NewPos(pad+float32(col)*m.cw+1, pad+float32(line)*m.lh+m.lh/2)
	}
	e.surface.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: point(1, 2)}, Button: desktop.MouseButtonPrimary})
	if c := e.Document().Caret(); c != (editor.Pos{Line: 1, Col: 2}) {
		t.Fatalf("click → %v", c)
	}
	e.surface.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: point(0, 7)}})
	if got := e.Document().SelectedText(); got != "1\nFR" {
		t.Errorf("drag selected %q", got)
	}
	e.surface.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: point(9, 0)}, Button: desktop.MouseButtonPrimary})
	if c := e.Document().Caret(); c != (editor.Pos{Line: 1, Col: 6}) {
		t.Errorf("a click below the text should go to its end, got %v", c)
	}
}

func TestKeywordsDrawInTheKeywordColour(t *testing.T) {
	e, _ := setup(t, "SELECT 'x' -- note")
	want := map[string]bool{"SELECT": false, "'x'": false, "-- note": false}
	for _, tx := range texts(surfaceObjects(t, e)) {
		switch tx.Text {
		case "SELECT":
			want[tx.Text] = tx.Color == uitheme.Light.SyntaxKeyword
		case "'x'":
			want[tx.Text] = tx.Color == uitheme.Light.SyntaxString
		case "-- note":
			want[tx.Text] = tx.Color == uitheme.Light.SyntaxComment
		}
	}
	for run, ok := range want {
		if !ok {
			t.Errorf("%q missing or in the wrong colour", run)
		}
	}
}

func TestOnlyVisibleLinesAreDrawn(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 10000; i++ {
		b.WriteString("SELECT col, 1 FROM t;\n")
	}
	e, _ := setup(t, b.String())
	// Each line draws five runs: SELECT, " col, ", 1, FROM, " t;". The whole
	// script would be 50 000; a viewport's worth is what may be drawn.
	const runsPerLine = 5
	n := len(texts(surfaceObjects(t, e)))
	lines := int(400/currentMetrics().lh) + 2
	if n < runsPerLine || n > lines*runsPerLine {
		t.Errorf("%d text runs drawn for a 400pt viewport (~%d lines of %d runs)", n, lines, runsPerLine)
	}
}

func TestCaretStaysInView(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("x\n")
	}
	e, _ := setup(t, b.String())
	end := &desktop.CustomShortcut{KeyName: fyne.KeyDown, Modifier: fyne.KeyModifierShortcutDefault}
	if runtime.GOOS != "darwin" {
		end.KeyName = fyne.KeyEnd
	}
	e.surface.TypedShortcut(end)
	m := currentMetrics()
	y := pad + float32(e.Document().Caret().Line)*m.lh
	off, h := e.scroll.Offset.Y, e.scroll.Size().Height
	if y < off || y+m.lh > off+h+1 {
		t.Errorf("caret at y=%v outside the viewport [%v, %v]", y, off, off+h)
	}
}

func TestGutterNumbersTheVisibleLines(t *testing.T) {
	e, _ := setup(t, "a\nb\nc")
	r := test.TempWidgetRenderer(t, e.gutter)
	r.Refresh()
	got := texts(r.Objects())
	if len(got) != 3 || got[0].Text != "1" || got[2].Text != "3" {
		t.Errorf("gutter shows %d labels", len(got))
	}
}

func TestBracketPairIsOutlined(t *testing.T) {
	e, _ := setup(t, "(1)")
	e.Document().SetCaret(editor.Pos{Line: 0, Col: 3}, false)
	outlined := 0
	for _, o := range surfaceObjects(t, e) {
		if r, ok := o.(*canvas.Rectangle); ok && r.StrokeWidth > 0 {
			outlined++
		}
	}
	if outlined != 2 {
		t.Errorf("%d brackets outlined, want the pair", outlined)
	}
}

func TestMarkErrorUnderlinesUntilTheNextEdit(t *testing.T) {
	e, _ := setup(t, "SELECT oops FROM t")
	e.MarkError(editor.Pos{Line: 0, Col: 8})
	if c := e.Document().Caret(); c != (editor.Pos{Line: 0, Col: 7}) {
		t.Errorf("caret %v; it should move to the start of the rejected word", c)
	}
	underlined := func() bool {
		for _, o := range surfaceObjects(t, e) {
			if r, ok := o.(*canvas.Rectangle); ok && r.FillColor == uitheme.Light.Danger {
				return true
			}
		}
		return false
	}
	if !underlined() {
		t.Fatal("no underline under the rejected word")
	}
	test.Type(e.surface, "x")
	if underlined() {
		t.Error("the mark outlived an edit that may have moved what it pointed at")
	}
}

func TestMatchesAreOutlinedUntilTheTextChanges(t *testing.T) {
	e, _ := setup(t, "a b a")
	ms, _ := e.Document().FindAll(editor.Search{Text: "a"}, 0)
	e.SetMatches(ms)
	outlined := func() int {
		n := 0
		for _, o := range surfaceObjects(t, e) {
			if r, ok := o.(*canvas.Rectangle); ok && r.StrokeColor == uitheme.Light.ControlAccent {
				n++
			}
		}
		return n
	}
	if n := outlined(); n != 2 {
		t.Fatalf("%d matches outlined, want 2", n)
	}
	test.Type(e.surface, "x")
	if outlined() != 0 {
		t.Error("outlines outlived an edit that moved what they marked")
	}
}
