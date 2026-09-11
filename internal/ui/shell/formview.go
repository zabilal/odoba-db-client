package shell

import (
	"fmt"
	"slices"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// formView shows one row laid out vertically, a field for each column shown,
// in the grid's place (FR-3.10, ADR-0039): a wide row is read down rather
// than across. It follows the grid's active row and moves it, and a field is
// typed into where the grid edits.
type formView struct {
	s      *Shell
	t      *tab
	g      *grid.TableGrid
	holder *fyne.Container
	was    []fyne.CanvasObject // what the holder showed: the grid, or the grid and its viewer
	box    fyne.CanvasObject
	where  *widget.Label // which row, and how it stands
	prev   *widget.Button
	next   *widget.Button
	form   *widget.Form
	cols   []int // the model columns shown, in order, a field each
	fields []*formField
	r      int
	row    model.Row
	has    bool
	seq    int // numbers reads, so that only the latest lands
}

// formField is one column's field: its value as text, typed into where the
// grid edits.
type formField struct {
	f     *formView
	mc    int
	item  *widget.FormItem
	text  *widget.Label
	entry *fieldEntry
	start string // what the entry started with, for this row
}

func (s *Shell) toggleForm() {
	if t, g := s.activeTab(), s.activeGrid(); t != nil && g != nil {
		s.toggleFormFor(t, g)
	}
}

// toggleFormFor shows a grid's form view in its place, or the grid again.
func (s *Shell) toggleFormFor(t *tab, g *grid.TableGrid) {
	holder := t.holders[g]
	if holder == nil {
		return
	}
	f := t.forms[g]
	if f == nil {
		f = s.newForm(t, g, holder)
		t.forms[g] = f
	}
	if f.shown() {
		f.hide()
		return
	}
	f.show()
}

func (s *Shell) newForm(t *tab, g *grid.TableGrid, holder *fyne.Container) *formView {
	f := &formView{s: s, t: t, g: g, holder: holder, form: widget.NewForm(),
		where: widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})}
	f.where.Truncation = fyne.TextTruncateEllipsis
	f.prev = widget.NewButtonWithIcon("Previous Row", fynetheme.MoveUpIcon(), func() { f.move(-1) })
	f.next = widget.NewButtonWithIcon("Next Row", fynetheme.MoveDownIcon(), func() { f.move(1) })
	back := widget.NewButtonWithIcon("Show Grid", fynetheme.CancelIcon(), f.hide)
	f.prev.Importance, f.next.Importance, back.Importance = widget.LowImportance, widget.LowImportance, widget.LowImportance
	head := container.NewBorder(nil, nil, container.NewHBox(f.prev, f.next), back, f.where)
	f.box = container.NewBorder(head, nil, nil, nil, container.NewVScroll(f.form))
	return f
}

func (f *formView) shown() bool {
	return len(f.holder.Objects) == 1 && f.holder.Objects[0] == f.box
}

// show puts the form in the grid's place, on the active row, or the first
// when none is active.
func (f *formView) show() {
	if _, ok := f.g.Selection().Active(); !ok {
		f.g.GoTo(grid.CellID{})
	}
	f.was = f.holder.Objects
	f.holder.Objects = []fyne.CanvasObject{f.box}
	f.holder.Refresh()
	f.follow()
}

// hide gives the grid its place back, with its viewer if that was open.
func (f *formView) hide() {
	f.writeAll()
	f.holder.Objects = f.was
	f.holder.Refresh()
}

// follow shows the active row, keeping first what was typed into the row
// shown. A row not yet in memory is read for it, off the UI goroutine, and
// only the latest read lands.
func (f *formView) follow() {
	f.writeAll() // what was typed is kept before the row changes
	f.seq++
	c, ok := f.g.Selection().Active()
	if !ok {
		f.has = false
		f.where.SetText("No row selected")
		return
	}
	m := f.g.Model()
	if row, loaded := m.Row(f.t.ctx, int64(c.Row)); loaded {
		f.set(c.Row, row)
		return
	}
	f.where.SetText("Loading…")
	seq := f.seq
	go func() {
		rows, err := m.Read(f.t.ctx, int64(c.Row), int64(c.Row)+1)
		f.s.d.Run(func() {
			switch {
			case seq != f.seq || f.t.ctx.Err() != nil:
			case err != nil:
				f.where.SetText("Could not read the row: " + err.Error())
			case len(rows) == 0:
				f.where.SetText("No row here")
			default:
				f.set(c.Row, rows[0])
			}
		})
	}()
}

// set shows row r of the grid: a field for each column shown, its value as
// the grid shows it, a pending change's if it has one.
func (f *formView) set(r int, row model.Row) {
	f.r, f.row, f.has = r, row, true
	f.build()
	e := editsFor(f.t, f.g)
	deleted := r >= f.g.Model().Added() && e.changes() > 0 && e.pending.State(row) == model.RowDeleted
	changed := false
	for _, ff := range f.fields {
		changed = ff.set(e, deleted) || changed
	}
	f.where.SetText(f.rowText(deleted, changed))
	f.form.Refresh()
	n, _ := f.g.Model().Extent()
	setEnabled(f.prev, r > 0)
	setEnabled(f.next, int64(r)+1 < n)
}

// build makes a field for each column the grid shows, when they change.
func (f *formView) build() {
	shown := f.g.Shown()
	if slices.Equal(shown, f.cols) {
		return
	}
	cols := f.g.Model().Columns()
	f.cols, f.fields, f.form.Items = shown, nil, nil
	for _, mc := range shown {
		ff := &formField{f: f, mc: mc, text: widget.NewLabel("")}
		ff.text.Wrapping, ff.text.Selectable = fyne.TextWrapWord, true
		ff.entry = newFieldEntry(ff)
		ff.item = widget.NewFormItem(cols[mc].Name, container.NewStack(ff.text, ff.entry))
		f.fields = append(f.fields, ff)
		f.form.AppendItem(ff.item)
	}
}

// rowText says which row is shown, and how it stands.
func (f *formView) rowText(deleted, changed bool) string {
	m := f.g.Model()
	k := m.Added()
	if f.r < k {
		return fmt.Sprintf("New row %d of %d", f.r+1, k)
	}
	n, final := m.Extent()
	total := group(n - int64(k))
	if !final {
		total += "+"
	}
	text := fmt.Sprintf("Row %s of %s", group(int64(f.r-k+1)), total)
	switch {
	case deleted:
		text += " · to be deleted"
	case changed:
		text += " · changed"
	}
	return text
}

// move moves the grid's active row by d, and the form with it, in the
// column that was active, or the last shown if that has gone. A row past
// either end is none: the grid selects no cell outside it.
func (f *formView) move(d int) {
	c, _ := f.g.Selection().Active()
	f.g.GoTo(grid.CellID{Row: f.r + d, Col: min(c.Col, len(f.g.Shown())-1)})
}

func (f *formView) writeAll() {
	for _, ff := range f.fields {
		ff.write()
	}
}

// set shows the field's value, typed into where the grid edits the row, and
// reports whether it has a pending change.
func (ff *formField) set(e *edits, deleted bool) bool {
	f := ff.f
	col := f.g.Model().Columns()[ff.mc]
	v := f.g.CellValue(f.r, f.row, ff.mc)
	changed := false
	if f.r >= f.g.Model().Added() && e.changes() > 0 {
		_, changed = e.pending.Value(f.row, ff.mc)
	}
	ff.item.HintText = col.Type.Native
	if changed {
		ff.item.HintText = join(ff.item.HintText, "changed")
	}
	if e != nil && e.pending != nil && f.g.OnEdit != nil && !deleted && grid.Editable(col) {
		ff.start = grid.EditText(v, col, time.Local)
		ff.entry.SetText(ff.start)
		ff.text.Hide()
		ff.entry.Show()
		return changed
	}
	c := grid.Format(v, col, time.Local)
	if c.Truncated {
		c.Text += "…"
	}
	ff.text.TextStyle.Italic = c.Kind == grid.CellNull
	ff.text.SetText(c.Text)
	ff.entry.Hide()
	ff.text.Show()
	return changed
}

// write keeps what was typed into the field as a pending change, as the
// column's type. Text left as it started is no edit. Text that cannot be
// read, or a value refused, is said in the field's hint, and stays typed.
func (ff *formField) write() {
	f := ff.f
	text := ff.entry.Text
	if !f.has || !ff.entry.Visible() || text == ff.start {
		return
	}
	col := f.g.Model().Columns()[ff.mc]
	v, err := grid.Parse(text, col, time.Local)
	if err == nil {
		err = f.g.SetValue(f.r, f.row, ff.mc, v)
	}
	if err != nil {
		ff.item.HintText = "Not written: " + err.Error()
		f.form.Refresh()
		return
	}
	f.g.Table.Refresh()
	if row, ok := f.g.Model().Row(f.t.ctx, int64(f.r)); ok {
		f.set(f.r, row) // a new row's values live in the model
	}
}

// join joins two words of a hint with a middle dot, either of which may be
// empty.
func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + " · " + b
}

func setEnabled(b *widget.Button, on bool) {
	if on {
		b.Enable()
	} else {
		b.Disable()
	}
}

// fieldEntry is a field's editor. Return, or leaving it, writes what was
// typed; Escape puts back what it started with.
type fieldEntry struct {
	widget.Entry
	ff *formField
}

func newFieldEntry(ff *formField) *fieldEntry {
	e := &fieldEntry{ff: ff}
	e.ExtendBaseWidget(e)
	e.OnSubmitted = func(string) { ff.write() }
	return e
}

func (e *fieldEntry) FocusLost() {
	e.Entry.FocusLost()
	e.ff.write()
}

func (e *fieldEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape {
		e.SetText(e.ff.start)
		return
	}
	e.Entry.TypedKey(k)
}
