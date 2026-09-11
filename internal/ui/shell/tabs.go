package shell

import (
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/tabbar"

	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// Tabs move, by dragging or by command, and pin (T1.4, FR-15.2). Pinned
// tabs stay together at the left, in the order they were pinned, marked
// with a pin, and a tab moves only among the tabs of its own kind; new tabs
// open after the pinned ones. The tab bar is our own (internal/ui/tabbar):
// Fyne's cannot be dragged.

// tabOf is the tab an item belongs to.
func (s *Shell) tabOf(it *container.TabItem) *tab {
	for _, t := range s.open {
		if t.item == it {
			return t
		}
	}
	return nil
}

// canMoveTab reports whether the active tab can move by delta places
// without leaving its group.
func (s *Shell) canMoveTab(delta int) bool {
	t := s.activeTab()
	if t == nil {
		return false
	}
	i := slices.Index(s.tabs.Items, t.item)
	j := i + delta
	if i < 0 || j < 0 || j >= len(s.tabs.Items) {
		return false
	}
	next := s.tabOf(s.tabs.Items[j])
	return next != nil && next.pinned == t.pinned
}

// moveTab moves the active tab by delta places among the tabs of its kind.
func (s *Shell) moveTab(delta int) {
	if !s.canMoveTab(delta) {
		return
	}
	t := s.activeTab()
	items := slices.Clone(s.tabs.Items)
	i := slices.Index(items, t.item)
	items[i], items[i+delta] = items[i+delta], items[i]
	s.setTabs(s.tabs, items, t.item)
}

// togglePin pins the active tab, which then comes last among the pinned
// tabs, or unpins it, which then comes first among the rest.
func (s *Shell) togglePin() {
	if t := s.activeTab(); t != nil {
		s.pin(t, !t.pinned)
	}
}

// pin pins a tab or unpins it. A pinned tab joins the end of the pinned
// ones; an unpinned one goes first among the rest.
func (s *Shell) pin(t *tab, on bool) {
	p := s.paneOf(t.item)
	if p == nil {
		return
	}
	t.pinned = on
	items := s.tabsBut(p, t.item)
	t.item.Icon = nil
	if t.pinned {
		t.item.Icon = s.app.Settings().Theme().Icon(uitheme.IconNamePin)
	}
	s.setTabs(p, slices.Insert(items, s.pinnedAmong(items), t.item), t.item)
}

// dropTab puts a dragged tab where it was dropped, kept among the tabs of
// its kind, and selects it.
func (s *Shell) dropTab(it *container.TabItem, to int) {
	t, p := s.tabOf(it), s.paneOf(it)
	if t == nil || p == nil {
		return
	}
	items := s.tabsBut(p, it)
	lo, hi := s.pinnedAmong(items), len(items)
	if t.pinned {
		lo, hi = 0, lo
	}
	s.setTabs(p, slices.Insert(items, min(max(to, lo), hi), it), it)
}

// tabMenu is a tab's context menu, made of commands (ADR-0011 §13). They
// act on the selected tab, so the tab is selected first.
func (s *Shell) tabMenu(it *container.TabItem) *fyne.Menu {
	if t := s.tabOf(it); t != nil {
		s.selectTab(t)
	}
	return fyne.NewMenu("", s.popupItems([]string{cmdPinTab, "", cmdMoveTabLeft, cmdMoveTabRight, cmdMoveToPane, "", cmdTabClose})...)
}

func (s *Shell) showTabMenu(it *container.TabItem, at fyne.Position) {
	widget.ShowPopUpMenuAtPosition(s.tabMenu(it), s.win.Canvas(), at)
}

// tabsBut is pane p's tabs in order without it.
func (s *Shell) tabsBut(p *tabbar.Tabs, it *container.TabItem) []*container.TabItem {
	return slices.DeleteFunc(slices.Clone(p.Items), func(o *container.TabItem) bool { return o == it })
}

// pinnedAmong counts the pinned tabs among items.
func (s *Shell) pinnedAmong(items []*container.TabItem) int {
	n := 0
	for _, it := range items {
		if o := s.tabOf(it); o != nil && o.pinned {
			n++
		}
	}
	return n
}

// setTabs puts pane p's tabs in a new order, selects one, and works in p.
func (s *Shell) setTabs(p *tabbar.Tabs, items []*container.TabItem, selected *container.TabItem) {
	p.SetItems(items)
	p.Select(selected)
	s.workIn(p)
	s.sync()
}
