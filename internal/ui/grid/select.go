package grid

import (
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// gridTable is the grid's widget.Table, taught what Fyne's leaves out: the
// modifier keys held on a click, ⇧ with the arrow keys, and the shortcuts a
// focused grid answers (⌘C, ⌘A).
type gridTable struct {
	widget.Table
	g        *TableGrid
	mod      fyne.KeyModifier // held at the last mouse-down
	shift    bool             // ⇧ is down, for the arrow keys
	stepping bool             // an arrow key is moving the highlight
}

func newGridTable(g *TableGrid) *gridTable {
	t := &gridTable{g: g}
	t.Length, t.CreateCell, t.UpdateCell = g.length, g.createCell, g.UpdateCell
	t.OnSelected = t.selected
	t.OnHighlighted = t.highlighted
	t.ExtendBaseWidget(t)
	return t
}

// MouseDown notes the modifiers: Fyne's tap, which selects, carries none.
func (t *gridTable) MouseDown(e *desktop.MouseEvent) {
	t.mod = e.Modifier
	t.Table.MouseDown(e)
}

// selected turns Fyne's one selected cell into the grid's selection, then
// lets Fyne's go, so that a second click on the same cell is heard too.
func (t *gridTable) selected(id widget.TableCellID) {
	c := CellID{id.Row, id.Col}
	switch {
	case t.mod&fyne.KeyModifierShift != 0:
		t.g.sel.Extend(c)
	case t.mod&fyne.KeyModifierShortcutDefault != 0:
		t.g.sel.Toggle(c)
	default:
		t.g.sel.Click(c)
	}
	t.mod = 0
	t.Unselect(id)
	t.g.selectionChanged()
}

func isShift(k fyne.KeyName) bool { return k == desktop.KeyShiftLeft || k == desktop.KeyShiftRight }

func (t *gridTable) KeyDown(e *fyne.KeyEvent) {
	if isShift(e.Name) {
		t.shift = true
	}
}

func (t *gridTable) KeyUp(e *fyne.KeyEvent) {
	if isShift(e.Name) {
		t.shift = false
	}
}

func (t *gridTable) FocusLost() {
	t.shift = false
	t.Table.FocusLost()
}

// TypedKey lets the arrow keys move the selection, as in a spreadsheet; with
// ⇧ held they stretch it.
func (t *gridTable) TypedKey(e *fyne.KeyEvent) {
	t.stepping = true
	t.Table.TypedKey(e)
	t.stepping = false
}

func (t *gridTable) highlighted(id widget.TableCellID) {
	if !t.stepping {
		return // the focus arriving, not a key
	}
	c := CellID{id.Row, id.Col}
	if t.shift {
		t.g.sel.Extend(c)
	} else {
		t.g.sel.Click(c)
	}
	t.g.selectionChanged()
}

// TypedShortcut answers the shortcuts a focused grid owns. ⌘C and ⌘A are the
// editor's too, so neither is on the menu bar, and each goes to whatever has
// the focus (view.Reserved).
func (t *gridTable) TypedShortcut(s fyne.Shortcut) {
	switch s.(type) {
	case *fyne.ShortcutCopy:
		if t.g.OnCopy != nil {
			t.g.OnCopy()
		}
	case *fyne.ShortcutSelectAll:
		t.g.SelectAll()
	}
}

// Selection is the selected cells (FR-3.7): a copy the grid will not change.
func (g *TableGrid) Selection() Selection {
	s := g.sel
	s.rects = slices.Clone(s.rects)
	return s
}

// Select selects the block from one cell to another, as a click and then a
// ⇧-click do.
func (g *TableGrid) Select(from, to CellID) {
	g.sel.Click(from)
	g.sel.Extend(to)
	g.selectionChanged()
}

// ToggleCell adds a cell to the selection, or takes it out, as ⌘-click does.
func (g *TableGrid) ToggleCell(c CellID) {
	g.sel.Toggle(c)
	g.selectionChanged()
}

// SelectAll selects every cell.
func (g *TableGrid) SelectAll() {
	g.sel.All(len(g.model.Columns()))
	g.selectionChanged()
}

// SelectRow selects every cell in the active cell's row.
func (g *TableGrid) SelectRow() {
	if c, ok := g.sel.Active(); ok {
		g.sel.Row(c.Row, len(g.model.Columns()))
		g.selectionChanged()
	}
}

// SelectColumn selects every cell in the active cell's column.
func (g *TableGrid) SelectColumn() {
	if c, ok := g.sel.Active(); ok {
		g.sel.Column(c.Col)
		g.selectionChanged()
	}
}

// Model is the rows the grid shows.
func (g *TableGrid) Model() *Model { return g.model }

func (g *TableGrid) selectionChanged() {
	g.refresh()
	if g.OnSelectCell != nil {
		g.OnSelectCell()
	}
}
