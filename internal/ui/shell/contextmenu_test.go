package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// The context menu, from the keyboard (NFR-A1).

// menuTitles are the items of the menu the window would show now.
func menuTitles(s *Shell) []string {
	m := s.contextMenu()
	if m == nil {
		return nil
	}
	var out []string
	for _, it := range m.Items {
		out = append(out, it.Label)
	}
	return out
}

// With the explorer focused, the key asks for the selected node's menu:
// the same menu a right-click gives.
func TestTheKeyboardAsksForTheExplorersMenu(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	fx.s.win.Canvas().Focus(fx.s.Explorer.Tree)
	fx.s.sync()
	got := menuTitles(fx.s)
	if !has(got, titleOf(fx, cmdOpen)) || !has(got, titleOf(fx, cmdCopyRows)) {
		t.Errorf("the menu is %v", got)
	}
	// And it is the node's, not the tab's: selecting another node changes
	// what it offers.
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	if got := menuTitles(fx.s); has(got, titleOf(fx, cmdCopyRows)) && !has(got, titleOf(fx, cmdRefresh)) {
		t.Errorf("a database's menu is %v", got)
	}
	// Running it puts the menu on the screen.
	fx.s.run(cmdContextMenu)
	if top := fx.s.win.Canvas().Overlays().Top(); top == nil {
		t.Error("the menu did not open")
	}
}

// With a grid in front, the key asks for the column's menu.
func TestTheKeyboardAsksForTheColumnsMenu(t *testing.T) {
	fx, tb := openItems(t)
	// A node selected in the explorer, and the focus somewhere else: the
	// menu follows the focus, not whatever was last clicked in the tree.
	fx.s.Explorer.Tree.Select(view.NodeID(tb.connID, itemsNode.Ref))
	fx.s.win.Canvas().Unfocus()
	pump(t, fx.q, func() bool { return tb.grid != nil })
	fx.q.Run(func() { tb.grid.Select(grid.CellID{Row: 0, Col: 1}, grid.CellID{Row: 0, Col: 1}) })
	pump(t, fx.q, func() bool { return tb.grid.SelectedColumn() >= 0 })
	fx.s.sync()
	got := menuTitles(fx.s)
	if !has(got, titleOf(fx, cmdHideColumn)) {
		t.Errorf("the menu is %v", got)
	}
	if has(got, titleOf(fx, cmdOpen)) {
		t.Errorf("it gave the explorer's menu while the focus was elsewhere: %v", got)
	}
}

// With nothing else, the key asks for the tab's menu, which is the one
// thing always there.
func TestTheKeyboardAsksForTheTabsMenu(t *testing.T) {
	fx := newFixture(t)
	openQuery(t, fx, "")
	fx.s.win.Canvas().Unfocus()
	fx.s.sync()
	if got := menuTitles(fx.s); !has(got, titleOf(fx, cmdTabClose)) {
		t.Errorf("the menu is %v", got)
	}
	// A query tab has no grid, and the menu is offered all the same.
	if fx.s.activeGrid() != nil {
		t.Fatal("a query with no result has a grid")
	}
	if fx.s.menuItems[cmdContextMenu].Disabled {
		t.Error("a tab was offered no menu")
	}
}

// With nothing open at all there is no menu, and the command says so.
func TestNothingOpenHasNoMenu(t *testing.T) {
	fx := newFixture(t)
	fx.s.sync()
	if m := fx.s.contextMenu(); m != nil {
		t.Errorf("an empty window offers %+v", m.Items)
	}
	if !fx.s.menuItems[cmdContextMenu].Disabled {
		t.Error("an empty window offers a menu for nothing")
	}
	// And asking anyway does nothing, rather than showing an empty menu
	// or falling over.
	fx.s.showContextMenu()
	if top := fx.s.win.Canvas().Overlays().Top(); top != nil {
		t.Errorf("it put %T on the screen", top)
	}
}

// The menu appears beside what it is about, so that one asked for by key
// is where the eye already is.
func TestTheMenuAppearsBesideWhatItIsAbout(t *testing.T) {
	fx, tb := openItems(t)
	fx.s.win.Resize(fyne.NewSize(900, 600))
	fx.q.Flush()
	fx.s.win.Canvas().Focus(fx.s.Explorer.Tree)
	tree := fyne.CurrentApp().Driver().AbsolutePositionForObject(fx.s.Explorer.Tree)
	// Beside it and not over it: a menu drawn exactly on the row covers
	// the thing it is about.
	if got := fx.s.contextMenuAt(); got.X <= tree.X || got.Y <= tree.Y {
		t.Errorf("the explorer's menu would open at %v, over the tree at %v", got, tree)
	}
	fx.s.win.Canvas().Unfocus()
	at := fyne.CurrentApp().Driver().AbsolutePositionForObject(tb.grid.View())
	if got := fx.s.contextMenuAt(); got.X <= at.X || got.Y <= at.Y {
		t.Errorf("the grid's menu would open at %v, over the grid at %v", got, at)
	}
}

// Every command this window has is in the palette, which is what makes
// the keyboard enough on its own.
func TestEveryCommandIsInThePalette(t *testing.T) {
	fx := newFixture(t)
	all := fx.s.Commands().All()
	if len(all) < 50 {
		t.Fatalf("the window has %d commands", len(all))
	}
	for _, c := range all {
		if strings.TrimSpace(c.Title) == "" {
			t.Errorf("%s has no title to find it by", c.ID)
		}
		if c.Category == "" {
			t.Errorf("%s is in no category", c.ID)
		}
	}
}
