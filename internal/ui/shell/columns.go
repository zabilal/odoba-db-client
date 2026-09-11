package shell

import (
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// hasColumn reports whether the grid in front has an active cell, whose
// column the column commands act on.
func (s *Shell) hasColumn() bool {
	g := s.activeGrid()
	return g != nil && g.SelectedColumn() >= 0
}

// onColumn runs a column command on the active cell's column.
func (s *Shell) onColumn(do func(g *grid.TableGrid, col int)) {
	if g := s.activeGrid(); g != nil {
		if col := g.SelectedColumn(); col >= 0 {
			do(g, col)
		}
	}
}

func (s *Shell) showHeaderMenu(t *tab, g *grid.TableGrid, col int, at fyne.Position) {
	widget.ShowPopUpMenuAtPosition(s.headerMenu(t, g, col, at), s.win.Canvas(), at)
}

// headerMenu is what a right-click on a column's title offers (UX principle
// 2): filtering by its values where the source can list them, then hiding,
// moving and freezing the column (FR-3.2).
func (s *Shell) headerMenu(t *tab, g *grid.TableGrid, col int, at fyne.Position) *fyne.Menu {
	var items []*fyne.MenuItem
	if t.query == nil && t.browse != nil && t.browse.CanListValues() {
		items = append(items, fyne.NewMenuItem("Filter by Values…", func() { s.showPicklist(t, col, at) }),
			fyne.NewMenuItemSeparator())
	}
	shown := g.Shown()
	i := slices.Index(shown, col)
	hide := fyne.NewMenuItem("Hide Column", func() { g.HideColumn(col) })
	left := fyne.NewMenuItem("Move Left", func() { g.MoveColumn(col, -1) })
	right := fyne.NewMenuItem("Move Right", func() { g.MoveColumn(col, 1) })
	hide.Disabled, left.Disabled, right.Disabled = len(shown) <= 1, i <= 0, i < 0 || i >= len(shown)-1
	items = append(items, hide, left, right, fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Freeze Through This Column", func() { g.FreezeThrough(col) }))
	if g.Frozen() > 0 {
		items = append(items, fyne.NewMenuItem("Unfreeze Columns", g.Unfreeze))
	}
	if g.Hidden() {
		items = append(items, fyne.NewMenuItemSeparator(), fyne.NewMenuItem("Show All Columns", g.ShowAllColumns))
	}
	return fyne.NewMenu("", items...)
}
