package shell

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2"
)

// threeQueries opens three query tabs on one connection, in a window wide
// enough to split.
func threeQueries(t *testing.T) (*fixture, *tab, *tab, *tab) {
	t.Helper()
	fx := newFixture(t)
	fx.s.win.Resize(fyne.NewSize(1200, 800))
	c := fx.create(t, "db1", nil)
	a, b, d := fx.s.OpenQuery(c.ID), fx.s.OpenQuery(c.ID), fx.s.OpenQuery(c.ID)
	return fx, a, b, d
}

func paneTabs(s *Shell, i int) []*tab {
	var out []*tab
	for _, it := range s.panes[i].Items {
		out = append(out, s.tabOf(it))
	}
	return out
}

func cmdEnabled(fx *fixture, id string) bool {
	c, _ := fx.s.reg.Get(id)
	return c.Enabled == nil || c.Enabled()
}

func TestSplittingMovesTheTabIntoASecondPane(t *testing.T) {
	fx, a, b, d := threeQueries(t)
	if !cmdEnabled(fx, cmdSplitRight) || cmdEnabled(fx, cmdJoinPanes) || cmdEnabled(fx, cmdMoveToPane) {
		t.Fatal("three tabs in one pane can split, and there is nothing to join")
	}
	fx.s.run(cmdSplitRight)
	if len(fx.s.panes) != 2 || !slices.Equal(paneTabs(fx.s, 0), []*tab{a, b}) || !slices.Equal(paneTabs(fx.s, 1), []*tab{d}) {
		t.Fatalf("%d panes, not the active tab alone on the right", len(fx.s.panes))
	}
	if fx.s.activeTab() != d || fx.s.tabs != fx.s.panes[1] || !fx.s.paneSplit.Horizontal {
		t.Error("the moved tab is the one worked in, in a pane to the right")
	}
	if fx.s.paneBox.Objects[0] != fx.s.paneSplit {
		t.Error("both panes are shown")
	}
	if cmdEnabled(fx, cmdSplitRight) || !cmdEnabled(fx, cmdJoinPanes) || !cmdEnabled(fx, cmdMoveToPane) {
		t.Error("two panes do not split again; they join, and a tab moves between them")
	}
	fx.s.selectTab(a) // the left pane, with two tabs
	fx.s.run(cmdSplitRight)
	if cmdEnabled(fx, cmdSplitDown) || len(fx.s.panes) != 2 {
		t.Errorf("%d panes: two panes do not split again, whichever is worked in", len(fx.s.panes))
	}
}

func TestSplitDownStacksThePanes(t *testing.T) {
	fx, _, _, _ := threeQueries(t)
	fx.s.run(cmdSplitDown)
	if fx.s.paneSplit == nil || fx.s.paneSplit.Horizontal {
		t.Error("Split Down puts the second pane below")
	}
}

func TestOneTabDoesNotSplit(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	fx.s.OpenQuery(c.ID)
	if cmdEnabled(fx, cmdSplitRight) || cmdEnabled(fx, cmdSplitDown) {
		t.Error("a tab cannot be split from itself")
	}
}

func TestAPaneLeftEmptyCloses(t *testing.T) {
	fx, a, b, d := threeQueries(t)
	fx.s.run(cmdSplitRight)
	fx.s.run(cmdMoveToPane)
	if len(fx.s.panes) != 1 || !slices.Equal(paneTabs(fx.s, 0), []*tab{a, b, d}) || fx.s.paneSplit != nil {
		t.Fatalf("moving the last tab out left %d panes", len(fx.s.panes))
	}
	if fx.s.activeTab() != d || fx.s.paneBox.Objects[0] != fx.s.panes[0] {
		t.Error("the moved tab stays the one worked in, and the one pane takes the room")
	}
	fx.s.run(cmdSplitRight)
	fx.s.closeTab(d.item)
	if len(fx.s.panes) != 1 || fx.s.tabs != fx.s.panes[0] || !slices.Equal(paneTabs(fx.s, 0), []*tab{a, b}) {
		t.Error("closing a pane's last tab closes the pane")
	}
}

func TestJoiningKeepsPinnedTabsTogether(t *testing.T) {
	fx, a, b, d := threeQueries(t)
	e := fx.s.OpenQuery(a.connID)
	fx.s.selectTab(a)
	fx.s.run(cmdPinTab)
	fx.s.selectTab(e)
	fx.s.run(cmdSplitRight)
	fx.s.selectTab(d)
	fx.s.run(cmdMoveToPane)
	if !slices.Equal(paneTabs(fx.s, 0), []*tab{a, b}) || !slices.Equal(paneTabs(fx.s, 1), []*tab{e, d}) {
		t.Fatal("a moved tab goes after the tabs already there")
	}
	fx.s.run(cmdPinTab)
	if !slices.Equal(paneTabs(fx.s, 1), []*tab{d, e}) {
		t.Fatal("a pin keeps to its pane")
	}
	fx.s.run(cmdJoinPanes)
	if len(fx.s.panes) != 1 || !slices.Equal(paneTabs(fx.s, 0), []*tab{a, d, b, e}) {
		t.Error("joined, the pinned tabs come first, then the rest, each pane's in order")
	}
	if fx.s.activeTab() != d {
		t.Error("the tab worked in stays selected")
	}
}

func TestACommandActsInThePaneHoldingTheFocus(t *testing.T) {
	fx, _, b, d := threeQueries(t)
	fx.s.run(cmdSplitRight)
	if fx.s.panes[0].Selected() != b.item {
		t.Fatal("the left pane shows b")
	}
	editor := b.query.editor.Focusable()
	fx.s.win.Canvas().Focus(editor) // a click in b's editor, on the left
	fx.s.run(cmdPinTab)
	if !b.pinned || d.pinned {
		t.Fatal("the command should act on the tab whose editor has the focus")
	}
	fx.s.selectTab(d) // chosen in the other pane, as from the explorer
	if fx.s.win.Canvas().Focused() != nil {
		t.Error("choosing a tab in one pane lets go of a focus left in the other")
	}
	fx.s.run(cmdPinTab)
	if !d.pinned {
		t.Error("with the focus let go, the command acts on the tab chosen")
	}
	fx.s.win.Canvas().Focus(editor) // back into b's editor
	fx.s.run(cmdPinTab)
	if b.pinned || !d.pinned {
		t.Error("a click back in the other pane takes the work there")
	}
}

func TestAMovedPinnedTabStaysWithThePinned(t *testing.T) {
	fx, a, b, d := threeQueries(t)
	fx.s.run(cmdSplitRight)
	fx.s.selectTab(a)
	fx.s.run(cmdPinTab)
	fx.s.run(cmdMoveToPane)
	if !slices.Equal(paneTabs(fx.s, 1), []*tab{a, d}) || !slices.Equal(paneTabs(fx.s, 0), []*tab{b}) {
		t.Error("a pinned tab moved across goes in front of the unpinned ones there")
	}
}

func TestTappingATabWorksInItsPane(t *testing.T) {
	fx, _, _, _ := threeQueries(t)
	fx.s.run(cmdSplitRight)
	left := fx.s.panes[0]
	left.OnTapped(left.Selected()) // as a tap on the tab in front there reports
	if fx.s.tabs != left {
		t.Error("tapping a tab, even the one in front, works in its pane")
	}
}

func TestTheSplitComesBackAtTheNextStart(t *testing.T) {
	fx := newFixture(t)
	fx.s.win.Resize(fyne.NewSize(1200, 800))
	c := fx.create(t, "db1", nil)
	fx.s.OpenObject(c.ID, itemsNode)
	fx.s.OpenObject(c.ID, otherNode)
	fx.s.OpenQuery(c.ID)
	pump(t, fx.q, func() bool {
		o, i := tabNamed(fx.s, "other"), tabNamed(fx.s, "items")
		return o != nil && o.browse != nil && i != nil && i.browse != nil
	})
	fx.s.run(cmdSplitDown)
	fx.s.selectTab(tabNamed(fx.s, "other"))
	fx.s.run(cmdMoveToPane)
	fx.s.selectTab(tabNamed(fx.s, "Query 1")) // in front below, though moved there first
	fx.s.selectTab(tabNamed(fx.s, "items"))
	fx.s.paneSplit.SetOffset(0.62)
	fx.s.shutdown()

	s := fx.relaunch(t)
	if len(s.panes) != 2 || s.splitDir != splitDown || s.paneSplit.Offset != 0.62 {
		t.Fatalf("%d panes, split %q", len(s.panes), s.splitDir)
	}
	var top, bottom []string
	for _, it := range s.panes[0].Items {
		top = append(top, it.Text)
	}
	for _, it := range s.panes[1].Items {
		bottom = append(bottom, it.Text)
	}
	if !slices.Equal(top, []string{"items"}) || !slices.Equal(bottom, []string{"Query 1", "other"}) {
		t.Errorf("panes came back as %v over %v", top, bottom)
	}
	if f := s.panes[1].Selected(); f == nil || f.Text != "Query 1" {
		t.Error("each pane's front tab comes back in front")
	}
	if a := s.activeTab(); a == nil || a.item.Text != "items" || s.tabs != s.panes[0] {
		t.Error("the tab worked in comes back selected, in its pane")
	}
	pump(t, fx.q, func() bool { return tabNamed(s, "items").browse != nil && tabNamed(s, "other").browse != nil })
}
