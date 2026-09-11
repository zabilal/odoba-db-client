package shell

import "testing"

func TestFilterObjectsShowsTheSidebarAndFocusesTheFilter(t *testing.T) {
	fx := newFixture(t)
	c, _ := fx.s.reg.Get(cmdFilterObjects)
	if got := c.Shortcut.Label("darwin"); got != "⇧⌘F" {
		t.Errorf("Filter Objects is on %q, want ⇧⌘F", got)
	}
	fx.s.toggleSidebar()
	if fx.s.sidebar.Visible() {
		t.Fatal("setup: the sidebar should be hidden")
	}
	fx.s.run(cmdFilterObjects)
	if !fx.s.sidebar.Visible() || fx.s.win.Canvas().Focused() != fx.s.Explorer.Filter {
		t.Error("⇧⌘F should show the sidebar and put the keyboard in the explorer's filter")
	}
}
