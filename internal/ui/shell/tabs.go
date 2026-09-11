package shell

import (
	"slices"

	"fyne.io/fyne/v2/container"

	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// Tabs move left and right, and pin (T1.4, FR-15.2). Pinned tabs stay
// together at the left, in the order they were pinned, marked with a pin,
// and a tab moves only among the tabs of its own kind; new tabs open after
// the pinned ones. Fyne's tab bar cannot be dragged, so moving is by
// command: dragging needs a tab bar of our own.

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
	s.setTabs(items, t.item)
}

// togglePin pins the active tab, which then comes last among the pinned
// tabs, or unpins it, which then comes first among the rest.
func (s *Shell) togglePin() {
	t := s.activeTab()
	if t == nil {
		return
	}
	t.pinned = !t.pinned
	items := slices.DeleteFunc(slices.Clone(s.tabs.Items), func(it *container.TabItem) bool { return it == t.item })
	at := 0
	for _, it := range items {
		if o := s.tabOf(it); o != nil && o.pinned {
			at++
		}
	}
	t.item.Icon = nil
	if t.pinned {
		t.item.Icon = s.app.Settings().Theme().Icon(uitheme.IconNamePin)
	}
	s.setTabs(slices.Insert(items, at, t.item), t.item)
}

func (s *Shell) setTabs(items []*container.TabItem, selected *container.TabItem) {
	s.tabs.SetItems(items)
	s.tabs.Select(selected)
	s.sync()
}
