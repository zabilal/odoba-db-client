package grid

import "slices"

// Columns can be hidden, moved and frozen (FR-3.2). The model's columns keep
// their numbers, and the grid keeps the order they are shown in. Sorts,
// filters and SelectedColumn speak of model columns, since they are about
// the data. The selection speaks of shown columns, since a block is what a
// person sees; ColumnAt turns one into the other.

// ColumnAt is the model column shown at a display position, or -1.
func (g *TableGrid) ColumnAt(display int) int {
	if display < 0 || display >= len(g.order) {
		return -1
	}
	return g.order[display]
}

// Shown is the model columns, in the order they are shown.
func (g *TableGrid) Shown() []int { return slices.Clone(g.order) }

// Hidden reports whether any column is hidden.
func (g *TableGrid) Hidden() bool { return len(g.order) < len(g.model.Columns()) }

// Frozen is how many columns, from the left, stay in view as the rest
// scroll sideways.
func (g *TableGrid) Frozen() int { return g.frozen }

// HideColumn hides a model column. The last column shown stays.
func (g *TableGrid) HideColumn(col int) {
	i := slices.Index(g.order, col)
	if i < 0 || len(g.order) == 1 {
		return
	}
	g.order = slices.Delete(g.order, i, i+1)
	if i < g.frozen {
		g.frozen--
	}
	g.relayout(true)
}

// ShowAllColumns shows the hidden columns again, each after the shown column
// that comes before it in the model.
func (g *TableGrid) ShowAllColumns() {
	for col := range len(g.model.Columns()) {
		if slices.Contains(g.order, col) {
			continue
		}
		at := 0
		for i, shown := range g.order {
			if shown < col {
				at = i + 1
			}
		}
		g.order = slices.Insert(g.order, at, col)
		if at < g.frozen {
			g.frozen++
		}
	}
	g.relayout(true)
}

// MoveColumn moves a model column by delta places among those shown. The
// active cell goes with it, so a column can be moved again and again.
func (g *TableGrid) MoveColumn(col, delta int) {
	i := slices.Index(g.order, col)
	j := i + delta
	if i < 0 || j < 0 || j >= len(g.order) {
		return
	}
	row, active := -1, false
	if c, ok := g.sel.Active(); ok {
		row, active = c.Row, true
	}
	g.order = slices.Insert(slices.Delete(g.order, i, i+1), j, col)
	g.relayout(true)
	if active {
		g.sel.Click(CellID{row, j})
		g.selectionChanged()
	}
}

// FreezeThrough keeps the shown columns up to and including col in view as
// the rest scroll sideways.
func (g *TableGrid) FreezeThrough(col int) {
	if i := slices.Index(g.order, col); i >= 0 {
		g.frozen = i + 1
		g.relayout(false)
	}
}

// Unfreeze lets every column scroll.
func (g *TableGrid) Unfreeze() {
	g.frozen = 0
	g.relayout(false)
}

// relayout applies the column order: each width goes with its column, and
// the frozen count follows. A selection names shown columns, so when the
// order changes it no longer means what it did and is cleared.
func (g *TableGrid) relayout(clearSelection bool) {
	g.Table.StickyColumnCount = min(g.frozen, len(g.order))
	for dc, mc := range g.order {
		g.Table.SetColumnWidth(dc, g.widths[mc])
	}
	g.fillerWidth = -1 // the filler has moved: size it afresh
	g.fitFiller()
	if clearSelection {
		g.sel.Clear()
	}
	g.Table.Refresh()
	if clearSelection {
		g.selectionChanged()
	}
}
