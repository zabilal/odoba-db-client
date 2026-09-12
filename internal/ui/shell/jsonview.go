package shell

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// jsonView shows the rows as the documents they are, in the grid's place
// (FR-12.1, ADR-0065): a document store's rows are documents, and a nested
// one is a shape a grid cell can only hint at.
//
// It is read-only and shows a page at a time. Editing a document is T2.34.
type jsonView struct {
	s      *Shell
	t      *tab
	g      *grid.TableGrid
	holder *fyne.Container
	was    []fyne.CanvasObject
	box    fyne.CanvasObject

	where  *widget.Label
	note   *widget.Label // what the shape says, and what a save could not do
	text   *widget.Entry
	prev   *widget.Button
	next   *widget.Button
	edit   *widget.Button
	save   *widget.Button
	cancel *widget.Button

	from int64 // the first row shown
	seq  int   // numbers reads, so that only the latest lands

	// editing is true while one document is being edited, row is what it was
	// read as and at is where it is in the grid. warned is the type warning
	// already shown for what was typed, which a second Save writes past.
	editing bool
	row     model.Row
	at      int64
	warned  string
}

// jsonPage is how many documents are shown at once. Enough to read down,
// few enough to render while the eye is still on the page.
const jsonPage = 50

func (s *Shell) toggleJSON() {
	t, g := s.activeTab(), s.activeGrid()
	if t == nil || g == nil || t.holders[g] == nil {
		return
	}
	j := t.jsons[g]
	if j == nil {
		j = s.newJSON(t, g, t.holders[g])
		if t.jsons == nil {
			t.jsons = map[*grid.TableGrid]*jsonView{}
		}
		t.jsons[g] = j
	}
	if j.shown() {
		j.hide()
		return
	}
	j.show()
}

func (s *Shell) newJSON(t *tab, g *grid.TableGrid, holder *fyne.Container) *jsonView {
	j := &jsonView{s: s, t: t, g: g, holder: holder,
		where: widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})}
	j.where.Truncation = fyne.TextTruncateEllipsis
	j.text = widget.NewMultiLineEntry()
	j.text.TextStyle = fyne.TextStyle{Monospace: true}
	j.text.Wrapping = fyne.TextWrapOff
	j.note = widget.NewLabel("")
	j.note.Wrapping, j.note.Importance = fyne.TextWrapWord, widget.LowImportance
	j.prev = widget.NewButtonWithIcon("Previous Documents", fynetheme.MoveUpIcon(), func() { j.page(-1) })
	j.next = widget.NewButtonWithIcon("Next Documents", fynetheme.MoveDownIcon(), func() { j.page(1) })
	j.edit = widget.NewButtonWithIcon("Edit Document", fynetheme.DocumentCreateIcon(), j.editDocument)
	j.save = widget.NewButtonWithIcon("Save Document", fynetheme.ConfirmIcon(), j.saveDocument)
	j.cancel = widget.NewButtonWithIcon("Cancel", fynetheme.CancelIcon(), j.stopEditing)
	back := widget.NewButtonWithIcon("Show Grid", fynetheme.CancelIcon(), j.hide)
	for _, b := range []*widget.Button{j.prev, j.next, j.edit, j.cancel, back} {
		b.Importance = widget.LowImportance
	}
	j.save.Importance = widget.HighImportance
	head := container.NewBorder(nil, j.note, container.NewHBox(j.prev, j.next, j.edit, j.save, j.cancel), back, j.where)
	j.box = container.NewBorder(head, nil, nil, nil, j.text)
	j.buttons()
	return j
}

func (j *jsonView) shown() bool {
	return len(j.holder.Objects) == 1 && j.holder.Objects[0] == j.box
}

// show puts the documents in the grid's place, beginning at the active row so
// that the row being looked at is the document read first.
func (j *jsonView) show() {
	j.from = 0
	if c, ok := j.g.Selection().Active(); ok {
		j.from = int64(c.Row) / jsonPage * jsonPage
	}
	j.was = j.holder.Objects
	j.holder.Objects = []fyne.CanvasObject{j.box}
	j.holder.Refresh()
	j.editing = false
	j.buttons()
	j.read()
}

// hide gives the grid its place back, with its viewer if that was open.
func (j *jsonView) hide() {
	j.holder.Objects = j.was
	j.holder.Refresh()
}

// page moves a page forward or back, within what there is.
func (j *jsonView) page(by int64) {
	from := j.from + by*jsonPage
	if n, _ := j.g.Model().Extent(); from >= n {
		return
	}
	if from < 0 {
		from = 0
	}
	if from == j.from {
		return
	}
	j.from = from
	j.read()
}

// read fetches the page and renders it, off the UI goroutine: a page not in
// memory is a round trip, and the window keeps drawing meanwhile.
func (j *jsonView) read() {
	j.note.SetText("")
	j.seq++
	seq := j.seq
	j.where.SetText("Reading…")
	j.text.SetText("")
	m := j.g.Model()
	go func() {
		rows, err := m.Read(j.t.ctx, j.from, j.from+jsonPage)
		j.s.d.Run(func() {
			if seq != j.seq || j.t.ctx.Err() != nil {
				return // another page was asked for, or the tab closed
			}
			if err != nil {
				j.where.SetText("Could not read the documents: " + err.Error())
				return
			}
			j.set(rows)
		})
	}()
}

func (j *jsonView) set(rows []model.Row) {
	cols := j.g.Model().Columns()
	j.text.SetText(documentsJSON(cols, rows))
	switch {
	case len(rows) == 0:
		j.where.SetText("No documents")
	case len(rows) == 1:
		j.where.SetText(fmt.Sprintf("Document %d", j.from+1))
	default:
		j.where.SetText(fmt.Sprintf("Documents %d–%d", j.from+1, j.from+int64(len(rows))))
	}
	n, final := j.g.Model().Extent()
	j.prev.Disable()
	if j.from > 0 {
		j.prev.Enable()
	}
	j.next.Disable()
	if !final || j.from+jsonPage < n {
		j.next.Enable()
	}
}

// documentsJSON renders rows as a JSON array, a field for each column in the
// order the columns are in — which is the order the source gave them, and
// what a map would lose.
func documentsJSON(cols []model.ColumnDef, rows []model.Row) string {
	var b strings.Builder
	b.WriteString("[\n")
	for i, row := range rows {
		b.WriteString(indented(documentJSON(cols, row), "  "))
		if i < len(rows)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("]")
	return b.String()
}

// documentJSON renders one row as a JSON object, its fields in the columns'
// order.
func documentJSON(cols []model.ColumnDef, row model.Row) string {
	var b strings.Builder
	b.WriteString("{\n")
	for c := range cols {
		b.WriteString("  ")
		b.WriteString(quoted(cols[c].Name))
		b.WriteString(": ")
		var v any
		if c < len(row) {
			v = row[c]
		}
		b.WriteString(indented(valueJSON(v), "  ")[2:]) // the first line is already placed
		if c < len(cols)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("}")
	return b.String()
}

// indented puts a prefix in front of every line of a block.
func indented(text, prefix string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// valueJSON writes one value. A value of its own goes on one line; a
// document or a list is written out, and its caller indents it.
func valueJSON(v any) string {
	if _, ok := v.(model.Default); ok {
		// A new row's column the server will fill: it has no value yet.
		return "null"
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return quoted(fmt.Sprint(v))
	}
	return string(b)
}

func quoted(s string) string { return strconv.Quote(s) }
