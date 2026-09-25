package shell

import (
	"testing"

	"fyne.io/fyne/v2"
	fynetheme "fyne.io/fyne/v2/theme"

	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// How large text is drawn (NFR-A2).

// Choosing a larger text size draws text larger, keeps the choice, and
// ticks the size chosen.
func TestChoosingALargerTextSize(t *testing.T) {
	fx := newFixture(t)
	body := func() float32 { return fx.s.d.Theme.Size(fynetheme.SizeNameText) }
	was := body()
	fx.s.sync()
	if !fx.s.menuItems[textSizeID(uitheme.TextDefault)].Checked {
		t.Error("the default size is not ticked to begin with")
	}
	fx.s.run(textSizeID(uitheme.TextLarger))
	if got := body(); got <= was {
		t.Errorf("text is %v, and was %v", got, was)
	}
	if got := fx.settings.Get().TextSize; got != "larger" {
		t.Errorf("it saved %q", got)
	}
	fx.s.sync()
	if !fx.s.menuItems[textSizeID(uitheme.TextLarger)].Checked ||
		fx.s.menuItems[textSizeID(uitheme.TextDefault)].Checked {
		t.Error("the size chosen is not the one ticked")
	}
	// And the next start comes back at that size.
	s := fx.relaunch(t)
	if s.d.Theme.Text != uitheme.TextLarger {
		t.Errorf("it came back at %v", s.d.Theme.Text)
	}
}

// A name nobody offers reads as the default, so that a settings file from
// a later version is a window that opens rather than one that does not.
func TestATextSizeNobodyOffers(t *testing.T) {
	for _, name := range []string{"", "enormous", "LARGE"} {
		got := textSizeNamed(name)
		want := uitheme.TextDefault
		if name == "LARGE" {
			want = uitheme.TextLarge
		}
		if got != want {
			t.Errorf("%q read as %v", name, got)
		}
	}
}

// The window still fits at the largest text size: a window that needs more
// room than a small laptop has is a window somebody cannot use at the size
// they need to read it.
func TestTheWindowFitsAtTheLargestTextSize(t *testing.T) {
	fx, _ := openItems(t)
	fx.s.OpenQuery(fx.conns.List()[0].ID)
	pump(t, fx.q, func() bool { return len(fx.s.open) == 2 })
	fx.s.run(textSizeID(uitheme.TextLarger))
	fx.s.win.Resize(fyne.NewSize(1024, 768))
	fx.q.Flush()
	if got := fx.s.win.Content().MinSize(); got.Width > 1024 || got.Height > 768 {
		t.Errorf("at the largest text size the window needs %v, and a small laptop has 1024x768", got)
	}
	// And the things somebody needs at any size are still drawn.
	if !fx.s.Explorer.Filter.Visible() || !fx.s.status.Visible() {
		t.Error("the filter or the status line went away")
	}
}
