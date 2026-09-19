package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"image/color"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/cellview"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
	"github.com/ikigai-db/ikigai-db/internal/value"
)

// cellViewer shows the active cell's whole value beside the grid (FR-3.9).
// It is a panel in a split, not a dialog, so the rows stay in view (UX
// principle 4), and it follows the selection.
type cellViewer struct {
	s       *Shell
	ctx     context.Context
	g       *grid.TableGrid
	holder  *fyne.Container // where the grid's view lives; the split, while shown
	split   *container.Split
	title   *widget.Label
	meta    *widget.Label
	text    *widget.Label
	textBox fyne.CanvasObject
	code    *widget.TextGrid
	codeBox fyne.CanvasObject
	value   any
	col     model.ColumnDef
	has     bool
	seq     int // numbers reads, so that only the latest lands

	// Edit (FR-4.1, ADR-0029 §9) opens the value at length in an editor,
	// with a calendar for a date or an instant. While editing, the viewer
	// stays on its cell: r is the cell's grid row, row the row there, and mc
	// its model column.
	editBtn *widget.Button
	editor  *valueEntry
	calBox  *fyne.Container
	editBox fyne.CanvasObject
	editing bool
	r       int
	row     model.Row
	mc      int
	start   string
}

// hold records where a grid's view lives, so the viewer can open beside it.
func (t *tab) hold(g *grid.TableGrid, holder *fyne.Container) {
	if t.holders == nil {
		t.holders, t.viewers = map[*grid.TableGrid]*fyne.Container{}, map[*grid.TableGrid]*cellViewer{}
		t.forms = map[*grid.TableGrid]*formView{}
	}
	t.holders[g] = holder
}

func (s *Shell) toggleViewer() {
	if t, g := s.activeTab(), s.activeGrid(); t != nil && g != nil {
		s.toggleViewerFor(t, g)
	}
}

// toggleViewerFor shows a grid's viewer, or hides it.
func (s *Shell) toggleViewerFor(t *tab, g *grid.TableGrid) {
	holder := t.holders[g]
	if holder == nil {
		return
	}
	if f := t.forms[g]; f != nil && f.shown() {
		f.hide() // the viewer takes the form's place, beside the grid
		if v := t.viewers[g]; v != nil && v.shown() {
			return // it was open under the form
		}
	}
	v := t.viewers[g]
	if v == nil {
		v = s.newViewer(t.ctx, g, holder)
		t.viewers[g] = v
	}
	if v.shown() {
		v.hide()
		return
	}
	v.show()
}

// viewerFollow brings a grid's viewer to the selected cell, and its form
// view and a table's detail panel to the selected row, if they are open.
func (t *tab) viewerFollow(g *grid.TableGrid) {
	if v := t.viewers[g]; v != nil && v.shown() {
		v.follow()
	}
	if f := t.forms[g]; f != nil && f.shown() {
		f.follow()
	}
	if r := t.records[g]; r != nil && r.shown() {
		r.follow() // recordview.go
	}
	if d := t.detail; d != nil && g == t.grid {
		d.follow() // detail.go
	}
}

func (s *Shell) newViewer(ctx context.Context, g *grid.TableGrid, holder *fyne.Container) *cellViewer {
	v := &cellViewer{s: s, ctx: ctx, g: g, holder: holder,
		title: widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		meta:  widget.NewLabel(""), text: widget.NewLabel(""), code: widget.NewTextGrid()}
	v.title.Truncation, v.meta.Truncation = fyne.TextTruncateEllipsis, fyne.TextTruncateEllipsis
	v.meta.Importance = widget.LowImportance
	v.text.Wrapping = fyne.TextWrapWord
	v.text.Selectable = true
	v.textBox = container.NewVScroll(v.text)
	v.code.ShowLineNumbers = true
	v.codeBox = container.NewScroll(v.code)
	copyIt := widget.NewButtonWithIcon("Copy Value", fynetheme.ContentCopyIcon(), v.copyValue)
	closeIt := widget.NewButtonWithIcon("Close", fynetheme.CancelIcon(), v.hide)
	copyIt.Importance, closeIt.Importance = widget.LowImportance, widget.LowImportance
	v.editBtn = widget.NewButtonWithIcon("Edit", fynetheme.DocumentCreateIcon(), v.beginEdit)
	v.editBtn.Importance = widget.LowImportance
	v.editBtn.Disable()
	v.editor = newValueEntry(v)
	v.calBox = container.NewVBox()
	done := widget.NewButton("Done", v.finishEdit)
	done.Importance = widget.HighImportance
	cancel := widget.NewButton("Cancel", v.cancelEdit)
	v.editBox = container.NewBorder(v.calBox, container.NewHBox(layout.NewSpacer(), cancel, done), nil, nil, v.editor)
	v.editBox.Hide()
	head := container.NewBorder(nil, nil, nil, container.NewHBox(v.editBtn, copyIt, closeIt), container.NewVBox(v.title, v.meta))
	panel := container.NewBorder(head, nil, nil, nil, container.NewStack(v.textBox, v.codeBox, v.editBox))
	v.split = container.NewHSplit(g.View(), panel)
	v.split.Offset = 0.62
	return v
}

func (v *cellViewer) shown() bool {
	return len(v.holder.Objects) == 1 && v.holder.Objects[0] == fyne.CanvasObject(v.split)
}

func (v *cellViewer) show() {
	v.holder.Objects = []fyne.CanvasObject{v.split}
	v.holder.Refresh()
	v.follow()
}

func (v *cellViewer) hide() {
	v.holder.Objects = []fyne.CanvasObject{v.g.View()}
	v.holder.Refresh()
}

// follow shows the active cell's value. A row not yet in memory is read for
// it, off the UI goroutine, and only the latest read lands.
func (v *cellViewer) follow() {
	if v.editing {
		return // it stays on the cell it edits
	}
	v.seq++
	c, ok := v.g.Selection().Active()
	cols := v.g.Model().Columns()
	mc := v.g.ColumnAt(c.Col)
	if !ok || mc < 0 || mc >= len(cols) {
		v.has = false
		v.title.SetText("No cell selected")
		v.meta.SetText("Select a cell to see its whole value.")
		v.render(cellview.View{Kind: cellview.KindText})
		v.editBtn.Disable()
		return
	}
	col := cols[mc]
	m := v.g.Model()
	if row, loaded := m.Row(v.ctx, int64(c.Row)); loaded {
		v.set(c.Row, col, row, mc)
		return
	}
	v.title.SetText(col.Name)
	v.meta.SetText("Loading…")
	seq := v.seq
	go func() {
		rows, err := m.Read(v.ctx, int64(c.Row), int64(c.Row)+1)
		v.s.d.Run(func() {
			if seq != v.seq || v.ctx.Err() != nil {
				return
			}
			if err != nil {
				v.meta.SetText("Could not read the row: " + err.Error())
				return
			}
			var row model.Row
			if len(rows) > 0 {
				row = rows[0]
			}
			v.set(c.Row, col, row, mc)
		})
	}()
}

func (v *cellViewer) set(r int, col model.ColumnDef, row model.Row, i int) {
	val := v.g.CellValue(r, row, i) // a pending change, if it has one
	v.value, v.col, v.has, v.r, v.row, v.mc = val, col, true, r, row, i
	view := cellview.Prepare(val, col, time.Local)
	var meta []string
	if col.Type.Native != "" {
		meta = append(meta, col.Type.Native)
	}
	if view.Size != "" {
		meta = append(meta, view.Size)
	}
	if view.Cut {
		meta = append(meta, "the start is shown; Copy Value copies it all")
	}
	v.title.SetText(col.Name)
	v.meta.SetText(strings.Join(meta, " · "))
	v.render(view)
	if v.g.CanEditCell() {
		v.editBtn.Enable()
	} else {
		v.editBtn.Disable()
	}
}

// beginEdit opens the value at length for editing, JSON laid out on lines,
// with a calendar for a date or an instant.
func (v *cellViewer) beginEdit() {
	if v.editing {
		v.focus()
		return
	}
	if !v.has || !v.g.CanEditCell() {
		return
	}
	v.seq++ // a read still on its way must not move the viewer off this cell
	text := value.EditText(v.value, v.col, time.Local)
	if v.col.Type.Class == model.TypeJSON {
		var b bytes.Buffer
		if json.Indent(&b, []byte(text), "", "  ") == nil {
			text = b.String()
		}
	}
	v.start, v.editing = text, true
	v.editor.TextStyle.Monospace = v.col.Type.Class == model.TypeJSON || v.col.Type.Class == model.TypeXML
	v.editor.SetText(text)
	v.calBox.Objects = nil
	if c := v.col.Type.Class; c == model.TypeDate || c == model.TypeTimestamp {
		at := time.Now()
		if t, ok := v.value.(time.Time); ok {
			at = t
			if v.col.Type.TimeZone {
				at = t.In(time.Local)
			}
		}
		v.calBox.Objects = []fyne.CanvasObject{widget.NewCalendar(at, v.pickDate)}
	}
	v.calBox.Refresh()
	v.title.SetText("Editing " + v.col.Name)
	v.meta.SetText("Done keeps it as a pending change; Cancel leaves it as it was.")
	v.textBox.Hide()
	v.codeBox.Hide()
	v.editBox.Show()
	v.editBtn.Disable()
	v.focus()
}

func (v *cellViewer) focus() {
	if c := fyne.CurrentApp().Driver().CanvasForObject(v.g.Table); c != nil {
		c.Focus(v.editor)
	}
}

// pickDate puts the day picked on the calendar into the editor, keeping the
// time of day typed there.
func (v *cellViewer) pickDate(d time.Time) { v.editor.SetText(withDate(v.editor.Text, d)) }

var dayRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// withDate is text with its date made d's: before the time of day it has,
// or alone.
func withDate(text string, d time.Time) string {
	day := d.Format("2006-01-02")
	if len(text) >= 10 && dayRE.MatchString(text[:10]) {
		return day + text[10:]
	}
	return day
}

// finishEdit writes the text as the column's type, JSON made compact again.
// Text left as it started is no edit. Text that cannot be read, or a value
// refused, is said, and the editor stays.
func (v *cellViewer) finishEdit() {
	if !v.editing {
		return
	}
	if text := v.editor.Text; text != v.start {
		val, err := value.Parse(text, v.col, time.Local)
		if j, ok := val.(model.JSON); ok && err == nil {
			var b bytes.Buffer
			if json.Compact(&b, j) == nil {
				val = model.JSON(b.String())
			}
		}
		if err == nil {
			err = v.g.SetValue(v.r, v.row, v.mc, val)
		}
		if err != nil {
			v.meta.SetText("Not written: " + v.col.Name + ": " + err.Error())
			return
		}
		v.g.Table.Refresh()
	}
	v.leaveEdit()
}

func (v *cellViewer) cancelEdit() {
	if v.editing {
		v.leaveEdit()
	}
}

// leaveEdit shows the value again, following the selection once more.
func (v *cellViewer) leaveEdit() {
	v.editing = false
	v.editBox.Hide()
	v.calBox.Objects = nil
	v.follow()
}

// valueEntry is the viewer's editor. Escape gives the edit up.
type valueEntry struct {
	widget.Entry
	v *cellViewer
}

func newValueEntry(v *cellViewer) *valueEntry {
	e := &valueEntry{v: v}
	e.MultiLine, e.Wrapping = true, fyne.TextWrapWord
	e.ExtendBaseWidget(e)
	return e
}

func (e *valueEntry) TypedKey(k *fyne.KeyEvent) {
	if k.Name == fyne.KeyEscape {
		e.v.cancelEdit()
		return
	}
	e.Entry.TypedKey(k)
}

func (v *cellViewer) render(view cellview.View) {
	if view.Kind == cellview.KindCode {
		v.code.SetText(strings.Join(view.Lines, "\n"))
		pal := v.s.colours()
		for _, sp := range view.Spans {
			line := view.Lines[sp.Line]
			style := &widget.CustomTextGridStyle{FGColor: roleColour(pal, sp.Role)}
			from, to := utf8.RuneCountInString(line[:sp.Start]), utf8.RuneCountInString(line[:sp.End])
			for c := from; c < to; c++ { // spans are bytes; the grid counts characters
				v.code.SetStyle(sp.Line, c, style)
			}
		}
		v.textBox.Hide()
		v.codeBox.Show()
		return
	}
	v.text.TextStyle = fyne.TextStyle{Italic: view.Kind == cellview.KindNull}
	v.text.SetText(view.Text)
	v.codeBox.Hide()
	v.textBox.Show()
}

func roleColour(p uitheme.Palette, r cellview.Role) color.Color {
	switch r {
	case cellview.RoleKey:
		return p.SyntaxFunction
	case cellview.RoleString:
		return p.SyntaxString
	case cellview.RoleNumber:
		return p.SyntaxNumber
	case cellview.RoleLiteral:
		return p.SyntaxKeyword
	}
	return p.Label
}

// copyValue copies the whole value, not what the viewer shows of it.
func (v *cellViewer) copyValue() {
	if !v.has {
		return
	}
	v.s.app.Clipboard().SetContent(export.Text(v.value, v.col))
	v.s.status.SetText("Copied the value of " + v.col.Name)
}
