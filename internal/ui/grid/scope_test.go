package grid

import (
	"runtime"
	"testing"
	_ "unsafe" // for go:linkname

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

// Fyne lets go of a widget's renderer when it has been out of sight for a
// while, and has no public way to ask for that now.
//
//go:linkname destroyRenderer fyne.io/fyne/v2/internal/cache.DestroyRenderer
func destroyRenderer(w fyne.Widget)

func heapNow() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// keptPerDraw draws in a window n times and answers how many megabytes each
// time left behind.
func keptPerDraw(t *testing.T, n int, draw func(w fyne.Window)) float64 {
	t.Helper()
	test.NewApp()
	w := test.NewWindow(widget.NewLabel("warm"))
	w.Resize(fyne.NewSize(300, 100))
	_ = w.Canvas().Capture()
	draw(w) // the first of anything pays for the fonts once
	before := heapNow()
	for range n {
		draw(w)
	}
	after := heapNow()
	if after < before {
		return 0
	}
	return float64(after-before) / float64(n) / 1e6
}

// A small control costs what a control costs. Fyne keeps a parsed copy of the
// theme's fonts for every theme override that measures text — megabytes each —
// so a control that was an override of its own left that behind every time one
// was made. Twenty of them at that rate is over a hundred megabytes; the bound
// here is a tenth of one each.
func TestASmallControlKeepsNoFontsOfItsOwn(t *testing.T) {
	kept := keptPerDraw(t, 20, func(w fyne.Window) {
		e := widget.NewEntry()
		e.SetText("hello")
		w.SetContent(newSmall(e))
		_ = w.Canvas().Capture()
	})
	if kept > 0.1 {
		t.Errorf("each small control left %.1f MB behind", kept)
	}
}

// Nor does refreshing one, which a header does every time the table is.
func TestRefreshingASmallControlKeepsNothing(t *testing.T) {
	e := widget.NewEntry()
	var s *small
	kept := keptPerDraw(t, 20, func(w fyne.Window) {
		if s == nil {
			s = newSmall(e)
			w.SetContent(s)
		}
		e.SetText("hello")
		s.Refresh()
		_ = w.Canvas().Capture()
	})
	if kept > 0.1 {
		t.Errorf("each refresh left %.1f MB behind", kept)
	}
}

// What sharing a scope must not change: a small control is the size Fyne's own
// override would have made it, drawn with the compact theme and not the
// application's.
func TestASmallControlIsTheSizeAnOverrideWouldMakeIt(t *testing.T) {
	test.NewApp()
	entry := func() *widget.Entry {
		e := widget.NewEntry()
		e.SetText("hello")
		return e
	}
	plain := entry().MinSize()
	want := container.NewThemeOverride(entry(), compact{}).MinSize()
	got := newSmall(entry()).MinSize()
	if got != want {
		t.Errorf("a small control is %v and an override is %v", got, want)
	}
	if got == plain {
		t.Errorf("a small control is %v, which is the size of one that is not small", got)
	}
}

// And it still is after Fyne has let go of its renderer, which is when Fyne
// forgets the scope a widget was in.
func TestASmallControlIsStillSmallOnceItsRendererHasGone(t *testing.T) {
	test.NewApp()
	e := widget.NewEntry()
	e.SetText("hello")
	s := newSmall(e)
	want := s.MinSize()
	destroyRenderer(e)
	destroyRenderer(s)
	e.Refresh() // as a redraw would, and with it the theme the entry remembered
	if got := s.MinSize(); got != want {
		t.Errorf("it was %v and came back %v", want, got)
	}
}

// It is small from the moment it is wrapped, whatever it had been before: an
// entry that has already been measured remembers the theme it was measured
// with until it is refreshed.
func TestWhatIsWrappedIsSmallAtOnce(t *testing.T) {
	test.NewApp()
	e := widget.NewEntry()
	e.SetText("hello")
	plain := e.MinSize()
	newSmall(e)
	if got := e.MinSize(); got == plain {
		t.Errorf("it is still %v, the size it was before", got)
	}
}

// A refresh puts back a scope the content has lost, the wrapper's own renderer
// having outlived the content's.
func TestARefreshPutsTheContentBackInItsScope(t *testing.T) {
	test.NewApp()
	e := widget.NewEntry()
	e.SetText("hello")
	s := newSmall(e)
	want := s.MinSize()
	destroyRenderer(e)
	s.Refresh()
	if got := e.MinSize(); got != want {
		t.Errorf("it was %v and came back %v", want, got)
	}
}

// counted is something drawn that says how often it was asked to draw.
type counted struct {
	canvas.Rectangle
	refreshed int
}

func (c *counted) Refresh() { c.refreshed++ }

// Refreshing the wrapper refreshes what it wraps, which is what a header means
// by refreshing its filter.
func TestRefreshingASmallControlRefreshesWhatItHolds(t *testing.T) {
	test.NewApp()
	c := &counted{}
	s := newSmall(c)
	before := c.refreshed
	s.Refresh()
	if c.refreshed != before+1 {
		t.Errorf("refreshed %d times by one refresh", c.refreshed-before)
	}
}

// entrySizes is how big an entry is under the compact theme and under the
// application's.
func entrySizes(text string) (small, plain fyne.Size) {
	e := widget.NewEntry()
	e.SetText(text)
	plain = e.MinSize()
	return container.NewThemeOverride(e, compact{}).MinSize(), plain
}

// The two small controls the grid has are small: a column's filter and a cell's
// editor, which are what the shared scope is for.
func TestAColumnsFilterIsASmallControl(t *testing.T) {
	g := filterableGrid(t)
	h := header(t, g, 1)
	want, plain := entrySizes("")
	if got := h.field.MinSize(); got.Height != want.Height || got.Height == plain.Height {
		t.Errorf("the filter is %v high: small is %v and not small is %v", got.Height, want.Height, plain.Height)
	}
}

func TestACellsEditorIsASmallControl(t *testing.T) {
	g, _, _ := editGrid(t)
	click(g, 1, 2, 0)
	g.table.TypedKey(key(fyne.KeyReturn))
	if g.editing == nil || g.editing.box == nil {
		t.Fatal("Return should open an editor on the active cell")
	}
	want, plain := entrySizes("x")
	if got := g.editing.box.MinSize(); got.Height != want.Height || got.Height == plain.Height {
		t.Errorf("the editor is %v high: small is %v and not small is %v", got.Height, want.Height, plain.Height)
	}
}
