package view

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/ui/editor"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// at is the point on the surface a place in the text is drawn at.
func at(line, col int) fyne.Position {
	m := currentMetrics()
	return fyne.NewPos(pad+float32(col)*m.cw+1, pad+float32(line)*m.lh+m.lh/2)
}

func click(e *Editor, line, col int, mod fyne.KeyModifier) {
	e.surface.MouseDown(&desktop.MouseEvent{PointEvent: fyne.PointEvent{Position: at(line, col)},
		Button: desktop.MouseButtonPrimary, Modifier: mod})
}

// caretsAt is where every caret is, in document order.
func caretsAt(e *Editor) []editor.Pos {
	var out []editor.Pos
	for _, r := range e.Document().Carets() {
		out = append(out, r.Caret)
	}
	return out
}

func samePlaces(a []editor.Pos, b ...editor.Pos) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// rects are the rectangles drawn in a colour, by where each is.
func rects(objs []fyne.CanvasObject, fill color.Color, width float32) []fyne.Position {
	var out []fyne.Position
	for _, o := range objs {
		if r, ok := o.(*canvas.Rectangle); ok && r.FillColor == fill && (width == 0 || r.Size().Width == width) {
			out = append(out, r.Position())
		}
	}
	return out
}

func TestAClickWithTheModifierAddsACaret(t *testing.T) {
	e, _ := setup(t, "abcd\nabcd\nabcd")
	click(e, 0, 1, 0)
	click(e, 1, 2, blockModifier)
	click(e, 2, 3, blockModifier)
	if !samePlaces(caretsAt(e), editor.Pos{Line: 0, Col: 1}, editor.Pos{Line: 1, Col: 2}, editor.Pos{Line: 2, Col: 3}) {
		t.Fatalf("three carets: %v", caretsAt(e))
	}
	// Every caret is drawn, each where it is.
	m := currentMetrics()
	drawn := rects(surfaceObjects(t, e), uitheme.Light.Label, 2)
	if len(drawn) != 3 {
		t.Fatalf("%d carets drawn", len(drawn))
	}
	for _, want := range []fyne.Position{
		{X: pad + 1*m.cw, Y: pad}, {X: pad + 2*m.cw, Y: pad + m.lh}, {X: pad + 3*m.cw, Y: pad + 2*m.lh},
	} {
		found := false
		for _, p := range drawn {
			found = found || p == want
		}
		if !found {
			t.Errorf("no caret is drawn at %v: %v", want, drawn)
		}
	}
	// Typing goes in at each, and is one thing to undo.
	test.Type(e.surface, "X")
	if got := e.Document().Text(); got != "aXbcd\nabXcd\nabcXd" {
		t.Errorf("typed %q", got)
	}
	e.surface.TypedShortcut(&fyne.ShortcutUndo{})
	if got := e.Document().Text(); got != "abcd\nabcd\nabcd" || len(caretsAt(e)) != 3 {
		t.Errorf("undone to %q with %d carets", got, len(caretsAt(e)))
	}
	// The same click again takes that caret away.
	click(e, 1, 2, blockModifier)
	if !samePlaces(caretsAt(e), editor.Pos{Line: 0, Col: 1}, editor.Pos{Line: 2, Col: 3}) {
		t.Errorf("one taken away: %v", caretsAt(e))
	}
	// Escape leaves one; and with one, Escape does nothing.
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if e.Document().Multiple() {
		t.Error("Escape left several carets")
	}
	before := caretsAt(e)
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	if !samePlaces(caretsAt(e), before...) {
		t.Error("Escape moved the only caret")
	}
	// A plain click among several leaves the one clicked; with Shift as
	// well the modifier adds nothing, and the selection is extended.
	click(e, 0, 0, 0)
	click(e, 1, 1, blockModifier)
	click(e, 2, 2, 0)
	if !samePlaces(caretsAt(e), editor.Pos{Line: 2, Col: 2}) {
		t.Errorf("a plain click: %v", caretsAt(e))
	}
	click(e, 2, 4, blockModifier|fyne.KeyModifierShift)
	if e.Document().Multiple() || e.Document().SelectedText() != "cd" {
		t.Errorf("⇧ with the modifier: %v, selected %q", caretsAt(e), e.Document().SelectedText())
	}
	// Unfocused, no caret is drawn, as none is for one.
	click(e, 0, 0, 0)
	click(e, 1, 1, blockModifier)
	e.surface.FocusLost()
	if drawn := rects(surfaceObjects(t, e), uitheme.Light.Label, 2); len(drawn) != 0 {
		t.Errorf("%d carets drawn in an editor that does not have the keyboard", len(drawn))
	}
}

func TestADragWithTheModifierSelectsABlock(t *testing.T) {
	e, _ := setup(t, "abcdef\nab\nabcdef")
	click(e, 0, 1, blockModifier)
	e.surface.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: at(2, 4)}})
	if got := e.Document().SelectedTexts(); got != "bcd\nb\nbcd" {
		t.Fatalf("the block holds %q", got)
	}
	// Each line's part is drawn selected, and only that.
	m := currentMetrics()
	picked := rects(surfaceObjects(t, e), uitheme.Light.RangeSelection, 0)
	if len(picked) != 3 {
		t.Fatalf("%d selections drawn", len(picked))
	}
	for _, p := range picked {
		if p.X != pad+m.cw {
			t.Errorf("a line's selection starts at %v, not at the block's left", p.X)
		}
	}
	// Dragged on, the block follows; let go, the next drag is an ordinary
	// one from where it begins.
	e.surface.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: at(1, 2)}})
	if got := e.Document().SelectedTexts(); got != "b\nb" {
		t.Errorf("the block dragged back holds %q", got)
	}
	click(e, 0, 0, 0)
	e.surface.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: at(1, 1)}})
	if e.Document().Multiple() || e.Document().SelectedText() != "abcdef\na" {
		t.Errorf("an ordinary drag after a block: %q", e.Document().SelectedTexts())
	}
	// What a block copies is put back where a block is.
	cb := fyne.CurrentApp().Clipboard()
	click(e, 0, 0, blockModifier)
	e.surface.Dragged(&fyne.DragEvent{PointEvent: fyne.PointEvent{Position: at(2, 1)}})
	e.surface.DragEnd()
	e.surface.TypedShortcut(&fyne.ShortcutCopy{Clipboard: cb})
	if cb.Content() != "a\na\na" {
		t.Fatalf("copied %q", cb.Content())
	}
	e.surface.TypedShortcut(&fyne.ShortcutCut{Clipboard: cb})
	if got := e.Document().Text(); got != "bcdef\nb\nbcdef" || cb.Content() != "a\na\na" {
		t.Errorf("cut left %q, with %q copied", got, cb.Content())
	}
	cb.SetContent("1\n2\n3")
	e.surface.TypedShortcut(&fyne.ShortcutPaste{Clipboard: cb})
	if got := e.Document().Text(); got != "1bcdef\n2b\n3bcdef" {
		t.Errorf("pasted %q", got)
	}
}

func TestTheKeysThatAddACaretAboveAndBelow(t *testing.T) {
	e, _ := setup(t, "abcd\nabcd\nabcd")
	click(e, 1, 2, 0)
	up := &desktop.CustomShortcut{KeyName: fyne.KeyUp, Modifier: caretModifier()}
	down := &desktop.CustomShortcut{KeyName: fyne.KeyDown, Modifier: caretModifier()}
	e.surface.TypedShortcut(up)
	e.surface.TypedShortcut(down)
	if !samePlaces(caretsAt(e), editor.Pos{Line: 0, Col: 2}, editor.Pos{Line: 1, Col: 2}, editor.Pos{Line: 2, Col: 2}) {
		t.Fatalf("carets above and below: %v", caretsAt(e))
	}
	// The arrow keys move them all, and deleting a word happens at each.
	e.surface.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	if !samePlaces(caretsAt(e), editor.Pos{Line: 0, Col: 3}, editor.Pos{Line: 1, Col: 3}, editor.Pos{Line: 2, Col: 3}) {
		t.Errorf("moved: %v", caretsAt(e))
	}
	e.surface.TypedShortcut(&desktop.CustomShortcut{KeyName: fyne.KeyBackspace, Modifier: wordModifier()})
	if got := e.Document().Text(); got != "d\nd\nd" {
		t.Errorf("a word deleted at each: %q", got)
	}
	// With Shift it is not this chord, and adds nothing.
	click(e, 1, 0, 0)
	if fn := e.surface.chord(&desktop.CustomShortcut{KeyName: fyne.KeyUp, Modifier: caretModifier() | fyne.KeyModifierShift}); fn != nil {
		fn()
	}
	if e.Document().Multiple() {
		t.Error("with Shift held, a caret was added")
	}
	// Both are among the chords the editor keeps from the menus.
	kept := 0
	for _, sc := range Reserved() {
		if cs, ok := sc.(*desktop.CustomShortcut); ok && cs.Modifier == caretModifier() && (cs.KeyName == fyne.KeyUp || cs.KeyName == fyne.KeyDown) {
			kept++
		}
	}
	if kept != 2 {
		t.Errorf("%d of the two chords are kept from the menus", kept)
	}
}
