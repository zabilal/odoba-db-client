package view

import (
	"context"
	"testing"

	"fyne.io/fyne/v2"
	fynetheme "fyne.io/fyne/v2/theme"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// fixedStatus is how each open connection is, as a test sets it.
type fixedStatus map[string]app.Status

func (f fixedStatus) Status(id string) (app.Status, bool) {
	st, ok := f[id]
	return st, ok
}

func TestALostConnectionSaysSoOnItsRow(t *testing.T) {
	newApp(t)
	l, saved := setup(t, "prod-db")
	e := New(l, (&uithread.Queue{}).Run, 0)
	e.Model.Children(explorer.RootID)
	root := waitReal(t, e.Model, explorer.RootID)
	r := newNodeRow()
	for _, c := range []struct {
		name   string
		status fixedStatus
		badge  string
	}{
		{"lost", fixedStatus{saved[0].ID: {State: app.StateDisconnected}}, "Disconnected"},
		{"well", fixedStatus{saved[0].ID: {State: app.StateConnected}}, "PROD"},
		{"not open", fixedStatus{}, "PROD"},
	} {
		e.stater = c.status
		e.update(root[0], r)
		if r.badge.Text != c.badge || !r.badge.TextStyle.Bold {
			t.Errorf("%s: the row's badge says %q (bold %v), want %q", c.name, r.badge.Text, r.badge.TextStyle.Bold, c.badge)
		}
	}
}

func TestTheLoaderSaysHowAConnectionIs(t *testing.T) {
	newApp(t)
	l, saved := setup(t, "a")
	if e := New(l, (&uithread.Queue{}).Run, 0); e.stater == nil {
		t.Error("the explorer should ask its loader how connections are")
	}
	if _, open := l.Status(saved[0].ID); open {
		t.Error("a connection not opened has no health to report")
	}
	if _, err := l.WS.Connect(context.Background(), saved[0].ID); err != nil {
		t.Fatal(err)
	}
	if st, open := l.Status(saved[0].ID); !open || st.State != app.StateConnected {
		t.Errorf("an open connection is %+v, open %v", st, open)
	}
}

// The tree says when the keyboard is in it: a selected row says which row,
// and nothing about whether the tree is listening (NFR-A1).
func TestTheTreeShowsWhenItHasTheFocus(t *testing.T) {
	newApp(t)
	l, _ := setup(t, "primary")
	e := New(l, (&uithread.Queue{}).Run, 0)
	if e.ring == nil {
		t.Fatal("the tree has no ring to show")
	}
	if e.ring.Visible() {
		t.Error("the ring is drawn before anything has the focus")
	}
	e.Tree.FocusGained()
	if !e.ring.Visible() {
		t.Fatal("the focused tree draws no ring")
	}
	if _, _, _, a := e.ring.StrokeColor.RGBA(); a == 0 {
		t.Error("the ring is drawn in nothing at all")
	}
	if e.ring.StrokeWidth <= 0 {
		t.Errorf("the ring is %v wide", e.ring.StrokeWidth)
	}
	e.Tree.FocusLost()
	if e.ring.Visible() {
		t.Error("the ring stayed after the focus went")
	}
}

// The ring is the theme's focus colour, so that it is the same sign
// everywhere else in the window uses one.
func TestTheRingIsTheThemesFocusColour(t *testing.T) {
	newApp(t)
	l, _ := setup(t, "primary")
	e := New(l, (&uithread.Queue{}).Run, 0)
	e.Tree.FocusGained()
	th, v := fyne.CurrentApp().Settings().Theme(), fyne.CurrentApp().Settings().ThemeVariant()
	want := th.Color(fynetheme.ColorNameFocus, v)
	if e.ring.StrokeColor != want {
		t.Errorf("the ring is %v, and the theme's focus colour is %v", e.ring.StrokeColor, want)
	}
}

// Taking the focus puts the tree's own highlight on the first row, so
// that the arrow keys have somewhere to start.
func TestFocusingTheTreeStartsAtTheFirstRow(t *testing.T) {
	newApp(t)
	l, _ := setup(t, "primary")
	e := New(l, (&uithread.Queue{}).Run, 0)
	e.Model.Children(explorer.RootID)
	root := waitReal(t, e.Model, explorer.RootID)
	highlighted := ""
	e.Tree.OnHighlighted = func(id string) { highlighted = id }
	e.Tree.FocusGained()
	if highlighted != root[0] {
		t.Errorf("the keyboard starts at %q, and the first row is %q", highlighted, root[0])
	}
}
