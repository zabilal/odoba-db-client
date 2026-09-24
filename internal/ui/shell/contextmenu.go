package shell

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"
)

// The context menu, from the keyboard (NFR-A1).
//
// Three menus in this window open on a right-click: a node's in the
// explorer, a column's on the grid's header, and a tab's. Everything in
// them is a command, and every command is in the palette and on the menu
// bar — so nothing here can only be done with a mouse. What could only be
// done with a mouse was asking for the menu itself, which is how somebody
// finds out what an object offers without knowing what to look for.
//
// So there is one key for it, where every desktop puts it, and it opens
// the menu for whatever the window is on.

// contextMenuAt is where a menu asked for by key appears: the top-left of
// whatever it is about, inset so that it does not cover it.
const contextMenuInset = 12

// canShowContextMenu reports whether there is anything to show a menu
// for. It asks the same three questions the menu does and builds nothing:
// making a node's menu selects the node and brings the window up to date,
// which is exactly what must not happen while working out whether to
// offer the command.
func (s *Shell) canShowContextMenu() bool {
	if s.explorerHasFocus() && s.Explorer.Selected() != "" {
		return true
	}
	// A grid is in a tab, so there is nothing to ask about a column that
	// the tab does not already answer.
	return s.tabs.Selected() != nil
}

// showContextMenu opens the menu for whatever the window is on.
func (s *Shell) showContextMenu() {
	m := s.contextMenu()
	if m == nil {
		return
	}
	widget.ShowPopUpMenuAtPosition(m, s.win.Canvas(), s.contextMenuAt())
}

// contextMenu is the menu for what the window is on: the explorer's
// selection while the explorer has the focus, then the column the grid's
// cursor is in, then the tab in front.
//
// The order is the focus first and the tabs last, because the tab menu is
// the one thing that is always there: taking it first would mean the
// explorer's own menu could never be asked for by key.
func (s *Shell) contextMenu() *fyne.Menu {
	if s.explorerHasFocus() {
		if id := s.Explorer.Selected(); id != "" {
			return s.explorerMenu(id)
		}
	}
	if t, g := s.activeTab(), s.activeGrid(); t != nil && g != nil {
		if col := g.SelectedColumn(); col >= 0 {
			return s.headerMenu(t, g, col, s.contextMenuAt())
		}
	}
	if it := s.tabs.Selected(); it != nil {
		return s.tabMenu(it)
	}
	return nil
}

// explorerHasFocus reports whether the keyboard is in the sidebar's tree.
func (s *Shell) explorerHasFocus() bool {
	return s.win.Canvas().Focused() == fyne.Focusable(s.Explorer.Tree)
}

// contextMenuAt is where the menu goes: beside what it is about, so that a
// menu asked for by key appears where the eye already is.
func (s *Shell) contextMenuAt() fyne.Position {
	var on fyne.CanvasObject = s.tabs
	switch {
	case s.explorerHasFocus():
		on = s.Explorer.Tree
	case s.activeGrid() != nil:
		on = s.activeGrid().View()
	}
	at := fyne.CurrentApp().Driver().AbsolutePositionForObject(on)
	return at.AddXY(contextMenuInset, contextMenuInset)
}
