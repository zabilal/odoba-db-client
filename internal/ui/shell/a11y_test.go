package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// buttons finds every visible button in a canvas object: inside containers,
// inside widgets' renderers, and in each list's row, which is not in the
// window until it is drawn.
func buttons(o fyne.CanvasObject) []*widget.Button {
	var out []*widget.Button
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		if o == nil || !o.Visible() {
			return // hidden, so no one sees it or hears it
		}
		switch v := o.(type) {
		case *widget.Button:
			out = append(out, v)
			return
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
			return
		case *widget.List:
			if v.CreateItem != nil {
				walk(v.CreateItem())
			}
		case *container.DocTabs:
			// Fyne's tab bars draw a "…" menu of their own, which the app
			// cannot name. What the tabs hold is the app's, and is walked.
			if it := v.Selected(); it != nil {
				walk(it.Content)
			}
			return
		case *container.AppTabs:
			if it := v.Selected(); it != nil {
				walk(it.Content)
			}
			return
		}
		if w, ok := o.(fyne.Widget); ok {
			for _, c := range test.WidgetRenderer(w).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	return out
}

// wordless reports the buttons in the window, and on it, with no words.
func wordless(s *Shell) []string {
	objs := append([]fyne.CanvasObject{s.win.Content()}, s.win.Canvas().Overlays().List()...)
	var bad []string
	for _, o := range objs {
		for _, b := range buttons(o) {
			if strings.TrimSpace(b.Text) == "" {
				name := "no icon either"
				if b.Icon != nil {
					name = b.Icon.Name()
				}
				bad = append(bad, name)
			}
		}
	}
	return bad
}

// TestEveryButtonHasWords holds UX principle 13: a button with only an
// icon has no name for a screen reader to read out.
func TestEveryButtonHasWords(t *testing.T) {
	t.Run("a table with its viewer, an error and Saved Queries", func(t *testing.T) {
		fx, tb := openItems(t)
		fx.s.toggleViewerFor(tb, tb.grid)
		fx.s.ShowError(errTest("something went wrong"))
		fx.s.showSaved()
		if bad := wordless(fx.s); len(bad) > 0 {
			t.Errorf("buttons with only an icon: %v", bad)
		}
	})
	t.Run("a query with its find bar and History", func(t *testing.T) {
		fx := newFixture(t)
		openQuery(t, fx, "")
		fx.s.withFind(func(f *findBar) { f.show(true) })
		fx.s.showHistory()
		if bad := wordless(fx.s); len(bad) > 0 {
			t.Errorf("buttons with only an icon: %v", bad)
		}
	})
}

type errTest string

func (e errTest) Error() string { return string(e) }
