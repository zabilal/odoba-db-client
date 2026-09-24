package shell

import (
	"bytes"
	"fmt"
	"image/png"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// Visible focus (NFR-A1): what the keyboard is on has to be visible, or
// the keyboard is a cursor nobody can see.

// focusables are the things in a window that can take the focus, with a
// name to report them by.
func focusables(o fyne.CanvasObject) []fyne.Focusable {
	var out []fyne.Focusable
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		if o == nil || !o.Visible() {
			return // nothing hidden takes the focus
		}
		if f, ok := o.(fyne.Focusable); ok {
			if d, isDisableable := o.(fyne.Disableable); !isDisableable || !d.Disabled() {
				out = append(out, f)
			}
		}
		switch v := o.(type) {
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
		case fyne.Widget:
			for _, c := range test.WidgetRenderer(v).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	return out
}

// shot is what the window draws now.
func shot(t *testing.T, w fyne.Window) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := w.Canvas().Capture()
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestEveryFocusableThingShowsIt holds the other half of "the keyboard is
// enough": a window where the focus is invisible can be driven only by
// somebody who already knows where it is.
func TestEveryFocusableThingShowsIt(t *testing.T) {
	fx, tb := openItems(t)
	fx.s.OpenQuery(tb.connID)
	fx.s.win.Resize(fyne.NewSize(1100, 700))
	pump(t, fx.q, func() bool { return len(fx.s.open) == 2 })
	fx.q.Flush()

	c := fx.s.win.Canvas()
	c.Unfocus()
	fx.q.Flush()
	for i, f := range focusables(fx.s.win.Content()) {
		before := shot(t, fx.s.win)
		c.Focus(f)
		fx.q.Flush()
		after := shot(t, fx.s.win)
		c.Unfocus()
		fx.q.Flush()
		if bytes.Equal(before, after) {
			t.Errorf("focusing %s drew nothing", describeFocusable(i, f))
		}
	}
}

func describeFocusable(i int, f fyne.Focusable) string {
	switch v := f.(type) {
	case *widget.Entry:
		return fmt.Sprintf("entry %d (%q)", i, v.PlaceHolder)
	case *widget.Button:
		return fmt.Sprintf("button %d (%q)", i, v.Text)
	}
	return fmt.Sprintf("%T %d", f, i)
}
