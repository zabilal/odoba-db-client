package grid

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Editing a cell in place (FR-4.1, ADR-0029). Return on the focused grid, or
// typing, opens an editor on the active cell. The editor is drawn inside the
// cell, so it scrolls with it; the table recycles cells as it scrolls, so
// each cell drawn takes the editor if it is at the editor's place and gives
// it up if not. What is written goes to OnEdit, which holds it as a change
// not yet committed.

// edit is a cell being edited: its place, the row it edits, and its editor.
type edit struct {
	at    widget.TableCellID
	row   model.Row
	col   int    // the model's
	start string // the text the editor started with
	entry *editEntry
	box   fyne.CanvasObject // the entry at a small control's size; nil for a choice
	menu  *widget.PopUpMenu // a choice's values
	items []*fyne.MenuItem  // and its items
	cell  *cellWidget       // the cell that last showed it
	done  bool
}

// CanEditCell reports whether the active cell can be edited.
func (g *TableGrid) CanEditCell() bool {
	_, _, ok := g.editable()
	return ok
}

// editable is the active cell's row and model column, if it can be edited:
// the grid edits, the column's values are typed, and the row is loaded and
// not to be deleted.
func (g *TableGrid) editable() (model.Row, int, bool) {
	a, ok := g.sel.Active()
	if !ok || g.OnEdit == nil {
		return nil, -1, false
	}
	cols := g.model.Columns()
	mc := g.ColumnAt(a.Col)
	if mc < 0 || mc >= len(cols) || !Editable(cols[mc]) {
		return nil, -1, false
	}
	row, _, state := g.rowAt(a.Row)
	if row == nil || state == model.RowDeleted {
		return nil, -1, false
	}
	return row, mc, true
}

// EditCell opens an editor on the active cell, as Return does on the
// focused grid. first, when not empty, is what was typed to open it, and
// takes the value's place. A cell whose values are picked, true and false or
// an enum's labels, shows them in a menu instead. It reports whether an
// editor opened. An editor already open is written and closed first; one
// whose value cannot be written stays open, and no other opens.
func (g *TableGrid) EditCell(first string) bool {
	if e := g.editing; e != nil && !g.finishEdit(0, 0) {
		g.Table.ScrollTo(e.at)
		g.focus(e.entry)
		return false
	}
	row, mc, ok := g.editable()
	if !ok {
		return false
	}
	a, _ := g.sel.Active()
	col := g.model.Columns()[mc]
	var v any
	if mc < len(row) {
		v = row[mc]
	}
	if g.changes != nil {
		if nv, ok := g.changes.Value(row, mc); ok {
			v = nv
		}
	}
	e := &edit{at: widget.TableCellID{Row: a.Row, Col: a.Col}, row: row, col: mc, start: EditText(v, col, g.loc)}
	g.editing = e
	// An editor is drawn only in a cell on screen, and only one drawn can
	// take the keyboard: Edit Cell may come from the menu with the cell
	// scrolled away.
	g.Table.ScrollTo(e.at)
	if opts := choices(col); opts != nil {
		g.Table.Refresh() // draws the cell, which the menu opens under
		g.showChoices(e, col, v, opts)
		return true
	}
	e.entry = newEditEntry(g, e)
	if v == nil {
		e.entry.PlaceHolder = NullText
	}
	text := e.start
	if first != "" {
		text = first
	}
	e.entry.SetText(text)
	e.entry.CursorColumn = len([]rune(text))
	e.box = container.NewThemeOverride(e.entry, compact{})
	g.Table.Refresh()
	g.focus(e.entry)
	return true
}

// host shows the open editor in the cell at its place, and takes it from a
// cell the table has moved elsewhere.
func (g *TableGrid) host(id widget.TableCellID, cell *cellWidget) {
	var box fyne.CanvasObject
	if e := g.editing; e != nil && e.at == id {
		e.cell, box = cell, e.box
	}
	cell.setEditor(box)
}

// finishEdit writes the open editor's value, and when it is taken closes
// the editor and moves the selection by dx columns and dy rows.
func (g *TableGrid) finishEdit(dx, dy int) bool {
	e := g.editing
	if e == nil {
		return true
	}
	if e.entry != nil && !g.write(e, e.entry.Text) {
		return false
	}
	g.closeEdit(e, true)
	g.step(e.at, dx, dy)
	return true
}

// CancelEdit closes the open editor, leaving the cell as it was.
func (g *TableGrid) CancelEdit() {
	if e := g.editing; e != nil {
		g.closeEdit(e, true)
	}
}

// write hands typed text to OnEdit as the column's type. Text unchanged
// from where the editor started is no edit: NULL opened and closed stays
// NULL, and a value is never rewritten only by being read back. Text that
// cannot be read, or a value OnEdit refuses, is said under the cell.
func (g *TableGrid) write(e *edit, text string) bool {
	if text == e.start {
		return true
	}
	col := g.model.Columns()[e.col]
	v, err := Parse(text, col, g.loc)
	if err == nil {
		err = g.OnEdit(e.row, e.col, v)
	}
	if err != nil {
		g.refuse(e, col.Name+": "+err.Error())
		return false
	}
	return true
}

// refuse says why a value was not written, under the cell.
func (g *TableGrid) refuse(e *edit, why string) {
	at := fyne.Position{}
	if e.cell != nil {
		at = fyne.CurrentApp().Driver().AbsolutePositionForObject(e.cell)
	}
	g.tipSeq++
	g.showTip(why, at)
}

// closeEdit closes an editor and draws its cell's value again. With
// refocus, the grid takes the focus back; not when the editor closes
// because the focus has already gone elsewhere.
func (g *TableGrid) closeEdit(e *edit, refocus bool) {
	e.done = true
	if g.editing == e {
		g.editing = nil
	}
	g.hint("", fyne.Position{})
	g.Table.Refresh()
	if refocus {
		g.focus(g.table)
	}
}

func (g *TableGrid) focus(o fyne.Focusable) {
	if c := fyne.CurrentApp().Driver().CanvasForObject(g.table); c != nil && o != nil {
		c.Focus(o)
	}
}

// step moves the selection from a cell by dx columns and dy rows, as Return
// and Tab do after writing, staying within the columns shown and the rows.
func (g *TableGrid) step(from widget.TableCellID, dx, dy int) {
	if dx == 0 && dy == 0 {
		return
	}
	to := widget.TableCellID{Row: from.Row + dy, Col: from.Col + dx}
	rows, _ := g.length()
	if to.Row < 0 || to.Row >= rows || to.Col < 0 || to.Col >= len(g.order) {
		return
	}
	g.table.Select(to)
}

// choices are the values a cell is picked from rather than typed: true and
// false, or an enum's labels, with NULL where the column allows it.
func choices(col model.ColumnDef) []any {
	var out []any
	switch col.Type.Class {
	case model.TypeBool:
		out = []any{true, false}
	case model.TypeEnum:
		for _, l := range col.Type.EnumValues {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return nil
	}
	if col.Type.Nullable {
		out = append(out, nil)
	}
	return out
}

// showChoices opens a menu of a cell's values under it, the value it has
// ticked. Picking one writes it; dismissing the menu leaves the cell as it
// was. The menu takes the keyboard: the arrow keys move, Return picks and
// Escape dismisses.
func (g *TableGrid) showChoices(e *edit, col model.ColumnDef, current any, opts []any) {
	c := fyne.CurrentApp().Driver().CanvasForObject(g.table)
	if c == nil || e.cell == nil {
		g.closeEdit(e, false)
		return
	}
	items := make([]*fyne.MenuItem, len(opts))
	for i, v := range opts {
		items[i] = fyne.NewMenuItem(shown(Format(v, col, g.loc)), func() { g.choose(e, v) })
		items[i].Checked = v == current
	}
	m := widget.NewPopUpMenu(fyne.NewMenu("", items...), c)
	m.OnDismiss = func() {
		m.Hide()
		if !e.done {
			g.closeEdit(e, true)
		}
	}
	e.menu, e.items = m, items
	d := fyne.CurrentApp().Driver()
	m.ShowAtPosition(d.AbsolutePositionForObject(e.cell).Add(fyne.NewPos(0, e.cell.Size().Height)))
}

// choose writes a value picked from a cell's menu. Fyne dismisses the menu,
// which closes the editor, before it runs the pick, so the cell is drawn
// again here, with the value picked.
func (g *TableGrid) choose(e *edit, v any) {
	if err := g.OnEdit(e.row, e.col, v); err != nil {
		g.refuse(e, g.model.Columns()[e.col].Name+": "+err.Error())
	}
	g.Table.Refresh()
}

// editEntry edits a cell's value as text. Return writes it and moves down;
// Tab writes it and moves right, ⇧Tab left; Escape leaves the cell as it
// was. Leaving it writes it, as leaving a spreadsheet's cell does; if it
// cannot be written, the editor stays open, saying why.
type editEntry struct {
	widget.Entry
	g     *TableGrid
	e     *edit
	shift bool
}

func newEditEntry(g *TableGrid, e *edit) *editEntry {
	en := &editEntry{g: g, e: e}
	en.ExtendBaseWidget(en)
	return en
}

// AcceptsTab keeps Tab from moving the focus: it moves to the next cell.
func (en *editEntry) AcceptsTab() bool { return true }

func (en *editEntry) KeyDown(k *fyne.KeyEvent) {
	if isShift(k.Name) {
		en.shift = true
	}
	en.Entry.KeyDown(k)
}

func (en *editEntry) KeyUp(k *fyne.KeyEvent) {
	if isShift(k.Name) {
		en.shift = false
	}
	en.Entry.KeyUp(k)
}

func (en *editEntry) TypedKey(k *fyne.KeyEvent) {
	switch k.Name {
	case fyne.KeyEscape:
		en.g.closeEdit(en.e, true)
	case fyne.KeyReturn, fyne.KeyEnter:
		en.g.finishEdit(0, 1)
	case fyne.KeyTab:
		dx := 1
		if en.shift {
			dx = -1
		}
		en.g.finishEdit(dx, 0)
	default:
		en.Entry.TypedKey(k)
	}
}

func (en *editEntry) FocusLost() {
	en.Entry.FocusLost()
	if !en.e.done && en.g.write(en.e, en.Text) {
		en.g.closeEdit(en.e, false)
	}
}
