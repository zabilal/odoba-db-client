package shell

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// showExplorerMenu shows a node's context menu where it was right-clicked
// (FR-2.4, T1.44). The explorer has selected the node already, so the
// commands in the menu, which act on the selection, act on it.
func (s *Shell) showExplorerMenu(id string, at fyne.Position) {
	if m := s.explorerMenu(id); m != nil {
		widget.ShowPopUpMenuAtPosition(m, s.win.Canvas(), at)
	}
}

// explorerMenu is a node's context menu. It is made of commands, so its
// titles, shortcuts, ticks and disabled items are the menu bar's own. The
// commands act on the selection, so the node is selected first: the menu
// and what its commands would do then always agree, whoever asks for it.
func (s *Shell) explorerMenu(id string) *fyne.Menu {
	if _, ok := view.FolderOf(id); ok {
		s.Explorer.Tree.Select(id)
		s.sync()
		return fyne.NewMenu("", s.popupItems([]string{cmdConnNew, cmdFolderEdit, "", cmdFolderDelete})...)
	}
	conn, ok := view.ConnectionOf(id)
	if !ok {
		return nil
	}
	s.Explorer.Tree.Select(id)
	s.sync()
	ids := []string{cmdOpen, cmdStructure, cmdFavorite, "", cmdScriptSelect, cmdScriptInsert, cmdScriptUpdate, "", cmdRefresh}
	switch {
	case s.selectionIsTopic():
		// Everything a topic can be asked for: read from, written to, grown,
		// and taken away. New Topic is here too, because somebody looking at
		// a topic is already where topics are made.
		ids = []string{cmdOpen, cmdStructure, cmdFavorite, "", cmdProduce, "",
			cmdTopicNew, cmdTopicConfig, cmdTopicPartition, cmdTopicDelete, "", cmdRefresh}
	case s.selectionIsGroup():
		// A group holds no rows and is not written to. What can be done to
		// one is move where it reads from next.
		ids = []string{cmdOpen, cmdStructure, cmdFavorite, "", cmdGroupReset, "", cmdRefresh}
	case s.selectionProducible():
		// A topic is the only thing here that can be written to, and the menu
		// says so only where that is true. Scripting a SELECT of a log, or a
		// greyed "Write a Record" on every table, would each be an item about
		// something the object cannot do at all.
		ids = []string{cmdOpen, cmdStructure, cmdFavorite, "", cmdProduce, "", cmdRefresh}
	case s.selectionTopicAdmin():
		// A cluster, or the class its topics hang under: nothing to write to
		// here, but this is where a topic is made.
		ids = []string{cmdOpen, cmdStructure, cmdFavorite, "", cmdTopicNew, "", cmdRefresh}
	}
	if id == view.ConnectionID(conn) {
		ids = []string{cmdConnEdit, cmdConnDup, "", cmdRefresh, cmdReconnect, cmdDisconnect, "", cmdConnDelete}
	}
	return fyne.NewMenu("", s.popupItems(ids)...)
}

// popupItems makes menu items for commands, an empty ID a separator. They
// are not registered: the menu bar's own items are the ones sync keeps up
// to date, so a pop-up works its states out as it is made.
func (s *Shell) popupItems(ids []string) []*fyne.MenuItem {
	out := make([]*fyne.MenuItem, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			out = append(out, fyne.NewMenuItemSeparator())
			continue
		}
		c, ok := s.reg.Get(id)
		if !ok {
			panic("shell: a pop-up menu names an unregistered command: " + id)
		}
		it := fyne.NewMenuItem(c.Title, func() { s.run(id) })
		it.Shortcut = fyneShortcut(c.Shortcut)
		it.Disabled = c.Enabled != nil && !c.Enabled()
		it.Checked = s.checked(id)
		out = append(out, it)
	}
	return out
}
