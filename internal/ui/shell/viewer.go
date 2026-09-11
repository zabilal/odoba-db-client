package shell

import (
	"context"
	"image/color"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/export"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/cellview"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
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
}

// hold records where a grid's view lives, so the viewer can open beside it.
func (t *tab) hold(g *grid.TableGrid, holder *fyne.Container) {
	if t.holders == nil {
		t.holders, t.viewers = map[*grid.TableGrid]*fyne.Container{}, map[*grid.TableGrid]*cellViewer{}
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

// viewerFollow brings a grid's viewer to the selected cell, if it is open.
func (t *tab) viewerFollow(g *grid.TableGrid) {
	if v := t.viewers[g]; v != nil && v.shown() {
		v.follow()
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
	head := container.NewBorder(nil, nil, nil, container.NewHBox(copyIt, closeIt), container.NewVBox(v.title, v.meta))
	panel := container.NewBorder(head, nil, nil, nil, container.NewStack(v.textBox, v.codeBox))
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
	v.seq++
	c, ok := v.g.Selection().Active()
	cols := v.g.Model().Columns()
	mc := v.g.ColumnAt(c.Col)
	if !ok || mc < 0 || mc >= len(cols) {
		v.has = false
		v.title.SetText("No cell selected")
		v.meta.SetText("Select a cell to see its whole value.")
		v.render(cellview.View{Kind: cellview.KindText})
		return
	}
	col := cols[mc]
	m := v.g.Model()
	if row, loaded := m.Row(v.ctx, int64(c.Row)); loaded {
		v.set(col, row, mc)
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
			v.set(col, row, mc)
		})
	}()
}

func (v *cellViewer) set(col model.ColumnDef, row model.Row, i int) {
	var val any
	if i < len(row) {
		val = row[i]
	}
	v.value, v.col, v.has = val, col, true
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
