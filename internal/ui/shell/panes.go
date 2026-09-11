package shell

import (
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/tabbar"
)

// Split panes (T1.5, FR-15.2): the tabs can be shown in two panes, side by
// side or one above the other, each with its own tab bar. A tab moves to
// the other pane by command; a pane left with no tab closes, and the other
// takes the room. s.tabs is the pane worked in: the one whose tab was last
// chosen or, when a command runs, the one holding the keyboard's focus.

const (
	splitRight = "right"
	splitDown  = "down"
)

// newPane makes the tab bar of a pane.
func (s *Shell) newPane() *tabbar.Tabs {
	p := tabbar.New()
	p.CloseIntercept = s.requestClose
	p.OnSelected = func(*container.TabItem) { s.workIn(p); s.sync() }
	p.OnTapped = func(*container.TabItem) { s.workIn(p); s.sync() }
	p.OnMove = s.dropTab
	p.OnMenu = s.showTabMenu
	p.MarkFor = s.tabMark
	return p
}

// workIn makes p the pane worked in. A focus left in the other pane is let
// go, so that the keyboard and the work are never in different panes.
func (s *Shell) workIn(p *tabbar.Tabs) {
	s.tabs = p
	if q := s.focusPane(); q != nil && q != p {
		s.win.Canvas().Unfocus()
	}
}

// followFocus works in the pane holding the keyboard's focus, so that a
// command acts on the grid or editor a person last clicked in, in either
// pane. Focus outside the panes, as in the explorer, changes nothing.
func (s *Shell) followFocus() {
	if p := s.focusPane(); p != nil {
		s.tabs = p
	}
}

// focusPane is the pane holding the keyboard's focus, or nil. It is found
// by place, since Fyne cannot say which container holds a widget.
func (s *Shell) focusPane() *tabbar.Tabs {
	if len(s.panes) < 2 {
		return nil
	}
	o, ok := s.win.Canvas().Focused().(fyne.CanvasObject)
	if !ok {
		return nil
	}
	d := s.app.Driver()
	at := d.AbsolutePositionForObject(o)
	for _, p := range s.panes {
		pos, size := d.AbsolutePositionForObject(p), p.Size()
		if at.X >= pos.X && at.Y >= pos.Y && at.X < pos.X+size.Width && at.Y < pos.Y+size.Height {
			return p
		}
	}
	return nil
}

// paneOf is the pane holding a tab.
func (s *Shell) paneOf(it *container.TabItem) *tabbar.Tabs {
	for _, p := range s.panes {
		if slices.Contains(p.Items, it) {
			return p
		}
	}
	return nil
}

// selectTab brings a tab forward in its pane, and works in that pane.
func (s *Shell) selectTab(t *tab) {
	if p := s.paneOf(t.item); p != nil {
		p.Select(t.item)
		s.workIn(p)
		s.sync()
	}
}

// addTab opens a tab, selected, in the pane worked in.
func (s *Shell) addTab(t *tab) {
	if t.band == nil { // every tab says its environment, and a lost connection (envband.go, lost.go)
		t.band, t.lost = newEnvBand(s, t), newLostBand(s, t)
		t.item.Content = container.NewBorder(container.NewVBox(t.band, t.lost), nil, nil, nil, t.item.Content)
	}
	s.showTabs(true)
	s.tabs.Append(t.item)
	s.tabs.Select(t.item)
}

// canSplit reports whether the active tab can go to a pane of its own:
// there is one pane, and it has another tab to keep.
func (s *Shell) canSplit() bool { return len(s.panes) == 1 && len(s.tabs.Items) > 1 }

// splitPane moves the active tab into a second pane, to the right or below.
func (s *Shell) splitPane(dir string) {
	t := s.activeTab()
	if !s.canSplit() || t == nil {
		return
	}
	s.panes = append(s.panes, s.newPane())
	s.splitDir = dir
	s.layoutPanes(0.5)
	s.moveTo(t, s.panes[1])
}

// layoutPanes shows the one pane, or the two in a split giving the first
// the share offset.
func (s *Shell) layoutPanes(offset float64) {
	s.paneSplit = nil
	var o fyne.CanvasObject = s.panes[0]
	if len(s.panes) == 2 {
		if s.splitDir == splitDown {
			s.paneSplit = container.NewVSplit(s.panes[0], s.panes[1])
		} else {
			s.paneSplit = container.NewHSplit(s.panes[0], s.panes[1])
		}
		s.paneSplit.Offset = offset
		o = s.paneSplit
	}
	s.paneBox.Objects = []fyne.CanvasObject{o}
	s.paneBox.Refresh()
}

// moveTo moves a tab into pane p, after the tabs of its kind there, and
// selects it. The pane it leaves closes if that was its last tab.
func (s *Shell) moveTo(t *tab, p *tabbar.Tabs) {
	from := s.paneOf(t.item)
	if from == nil || from == p {
		return
	}
	from.Remove(t.item)
	items := slices.Clone(p.Items)
	at := len(items)
	if t.pinned {
		at = s.pinnedAmong(items)
	}
	s.setTabs(p, slices.Insert(items, at, t.item), t.item)
	if len(from.Items) == 0 {
		s.closePane(from)
	}
}

// moveToOtherPane moves the active tab to the other pane.
func (s *Shell) moveToOtherPane() {
	t := s.activeTab()
	if t == nil || len(s.panes) < 2 {
		return
	}
	to := s.panes[0]
	if s.paneOf(t.item) == to {
		to = s.panes[1]
	}
	s.moveTo(t, to)
}

// closePane drops a pane left with no tabs; the other takes the room.
func (s *Shell) closePane(p *tabbar.Tabs) {
	if len(s.panes) < 2 {
		return
	}
	s.panes = slices.DeleteFunc(s.panes, func(o *tabbar.Tabs) bool { return o == p })
	if s.tabs == p {
		s.workIn(s.panes[0])
	}
	s.splitDir = ""
	s.layoutPanes(0)
	s.sync()
}

// joinPanes puts the second pane's tabs after the first's, pinned with the
// pinned, and closes it. The tab worked in stays selected.
func (s *Shell) joinPanes() {
	if len(s.panes) < 2 {
		return
	}
	first, second := s.panes[0], s.panes[1]
	sel := s.tabs.Selected()
	items := slices.Clone(first.Items)
	for _, it := range second.Items {
		at := len(items)
		if t := s.tabOf(it); t != nil && t.pinned {
			at = s.pinnedAmong(items)
		}
		items = slices.Insert(items, at, it)
	}
	second.SetItems(nil)
	s.closePane(second)
	s.setTabs(first, items, sel)
}

// resplit puts the second pane back as the session left it: its tabs moved
// to it in order, and each pane's front tab selected. A pane left empty, as
// when every tab of the first failed to reopen, closes as at any time.
func (s *Shell) resplit(last localdb.Session, opened []*tab) {
	if last.Split != splitRight && last.Split != splitDown {
		return
	}
	var second []*tab
	for i, st := range last.Tabs {
		if st.Pane == 1 && opened[i] != nil {
			second = append(second, opened[i])
		}
	}
	if len(second) == 0 {
		return
	}
	offset := last.SplitOffset
	if offset <= 0 || offset >= 1 {
		offset = 0.5
	}
	s.panes = append(s.panes, s.newPane())
	s.splitDir = last.Split
	s.layoutPanes(offset)
	for _, t := range second {
		s.moveTo(t, s.panes[1])
	}
	for i, st := range last.Tabs {
		if st.Front && opened[i] != nil {
			s.selectTab(opened[i])
		}
	}
}
