package shell

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/cellview"
	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// recordView shows one record whole (FR-13.8, ADR-0098): where it sat, when
// it arrived, what it carried, and the headers that came with it.
//
// A record is not a row, whatever the grid makes of it. Its key and value are
// bytes, and the same bytes are text to one person, JSON to another, and a
// hex dump to whoever has to know exactly what was written — so each is shown
// in a form the reader chooses, and only the forms those bytes admit are
// offered (cellview.Forms). Its headers are a list rather than a map, because
// Kafka lets a name repeat and a table that folded them together would throw
// away the difference between a header sent once and one sent twice.
//
// Decoding beyond what the bytes say of themselves — Avro, Protobuf, a schema
// from a registry — is T2.68, and lands behind this same chooser.
type recordView struct {
	s      *Shell
	t      *tab
	g      *grid.TableGrid
	holder *fyne.Container
	was    []fyne.CanvasObject
	box    fyne.CanvasObject

	where   *widget.Label // which record, by partition and offset
	when    *widget.Label // its timestamp
	headers *fyne.Container

	key   *formPicker
	value *formPicker

	at  int
	seq int // numbers reads, so that only the latest lands
}

// formPicker is one of a record's two byte fields: a chooser over the forms
// its bytes admit, and the form chosen.
type formPicker struct {
	s     *Shell
	title string
	// field is which of a record's two byte fields this is, and topic and
	// connID are where it was read from: together they say whose choice of
	// decoder is being remembered (FR-13.7).
	field  string
	topic  string
	connID string
	// restoring is true while set is putting back the form this topic was
	// last read in. What is remembered is what somebody chose, and putting a
	// choice back is not choosing it again.
	restoring bool
	pick      *widget.Select
	meta      *widget.Label
	text      *widget.Label
	code      *widget.TextGrid
	box       *fyne.Container

	forms []cellview.Form
	at    int

	// decoder is what the connection's schema registry says these bytes are,
	// where it says anything (T2.70). Resolving it is a round trip, so it is
	// done once when the view opens rather than for every record drawn.
	decoder source.Decoder
}

func (s *Shell) canShowRecord() bool {
	t, g := s.activeTab(), s.activeGrid()
	// Records, and nothing else: every other object's rows are rows, and the
	// form view already reads one of those down.
	return t != nil && g != nil && t.holders[g] != nil && t.ref.Kind == model.KindTopic
}

// toggleRecord shows the active record in the grid's place, or takes it away.
func (s *Shell) toggleRecord() {
	if !s.canShowRecord() {
		return
	}
	t, g := s.activeTab(), s.activeGrid()
	r := t.records[g]
	if r == nil {
		r = s.newRecord(t, g, t.holders[g])
		if t.records == nil {
			t.records = map[*grid.TableGrid]*recordView{}
		}
		t.records[g] = r
	}
	if r.shown() {
		r.hide()
		return
	}
	r.show()
}

func (s *Shell) newRecord(t *tab, g *grid.TableGrid, holder *fyne.Container) *recordView {
	r := &recordView{s: s, t: t, g: g, holder: holder,
		where: widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})}
	r.where.Truncation = fyne.TextTruncateEllipsis
	r.when = widget.NewLabel("")
	r.when.Importance = widget.LowImportance
	r.headers = container.NewVBox()
	r.key = newFormPicker(s, "Key", app.DecodeKey)
	r.value = newFormPicker(s, "Value", app.DecodeValue)
	r.key.connID, r.value.connID = t.connID, t.connID
	r.key.topic, r.value.topic = t.ref.Name(), t.ref.Name()

	prev := widget.NewButtonWithIcon("Previous Record", fynetheme.MoveUpIcon(), func() { r.move(-1) })
	next := widget.NewButtonWithIcon("Next Record", fynetheme.MoveDownIcon(), func() { r.move(1) })
	back := widget.NewButtonWithIcon("Show Grid", fynetheme.CancelIcon(), r.hide)
	for _, b := range []*widget.Button{prev, next, back} {
		b.Importance = widget.LowImportance
	}
	s.resolveDecoders(t, r)
	head := container.NewBorder(nil, r.when, container.NewHBox(prev, next), back, r.where)
	body := container.NewVBox(r.key.box, r.value.box, r.headers)
	r.box = container.NewBorder(head, nil, nil, nil, container.NewVScroll(body))
	return r
}

func newFormPicker(s *Shell, title, field string) *formPicker {
	p := &formPicker{s: s, title: title, field: field}
	p.meta = widget.NewLabel("")
	p.meta.Importance = widget.LowImportance
	p.text = widget.NewLabel("")
	p.text.Wrapping, p.text.Selectable = fyne.TextWrapWord, true
	p.code = widget.NewTextGrid()
	p.pick = widget.NewSelect(nil, p.choose)
	copyIt := widget.NewButtonWithIcon("Copy", fynetheme.ContentCopyIcon(), p.copy)
	copyIt.Importance = widget.LowImportance
	head := container.NewBorder(nil, nil, container.NewHBox(bold(title), p.pick), copyIt, p.meta)
	p.box = container.NewVBox(head, p.text, p.code, widget.NewSeparator())
	return p
}

// choose shows the form a person picked, by name.
func (p *formPicker) choose(name string) {
	for i, f := range p.forms {
		if f.Name == name {
			p.at = i
			p.render(f.View)
			if !p.restoring {
				p.s.rememberDecoder(p.connID, p.field, p.topic, name)
			}
			return
		}
	}
}

// set gives the picker a value, offering the forms those bytes admit. The
// form chosen is kept by name where the new value still admits it, so that
// reading down a topic in hex does not jump back to text at every record.
func (p *formPicker) set(v any, col model.ColumnDef) {
	was := ""
	if p.at < len(p.forms) {
		was = p.forms[p.at].Name
	}
	if was == "" {
		// Nothing chosen yet in this view: the topic reads as it did the last
		// time somebody read it (FR-13.7).
		was = p.s.rememberedDecoder(p.connID, p.field, p.topic)
	}
	p.forms = cellview.Forms(v, col, time.Local, p.decoder)
	names := make([]string, len(p.forms))
	for i, f := range p.forms {
		names[i] = f.Name
	}
	p.at = 0
	for i, n := range names {
		if n == was {
			p.at = i
		}
	}
	p.pick.Options = names
	if v == nil {
		// Nothing was written, which is not the same as empty bytes: a record
		// with no key is how a producer says it has no key.
		p.pick.Options = nil
		p.meta.SetText("none")
		p.render(cellview.View{Kind: cellview.KindNull, Text: "No " + strings.ToLower(p.title)})
		p.pick.Refresh()
		return
	}
	p.restoring = true
	p.pick.SetSelectedIndex(p.at)
	p.restoring = false
	p.pick.Refresh()
	view := p.forms[p.at].View
	meta := []string{}
	if view.Size != "" {
		meta = append(meta, view.Size)
	}
	if view.Cut {
		meta = append(meta, "the start is shown; Copy copies it all")
	}
	p.meta.SetText(strings.Join(meta, " · "))
	p.render(view)
}

// render draws one form, the same way the cell viewer draws one value.
func (p *formPicker) render(view cellview.View) {
	if view.Kind == cellview.KindCode {
		p.code.SetText(strings.Join(view.Lines, "\n"))
		pal := p.s.colours()
		for _, sp := range view.Spans {
			line := view.Lines[sp.Line]
			style := &widget.CustomTextGridStyle{FGColor: roleColour(pal, sp.Role)}
			from, to := utf8.RuneCountInString(line[:sp.Start]), utf8.RuneCountInString(line[:sp.End])
			for c := from; c < to; c++ { // spans are bytes; the grid counts characters
				p.code.SetStyle(sp.Line, c, style)
			}
		}
		p.text.Hide()
		p.code.Show()
		return
	}
	p.text.TextStyle = fyne.TextStyle{Italic: view.Kind == cellview.KindNull}
	p.text.SetText(view.Text)
	p.code.Hide()
	p.text.Show()
}

// copy puts the form on screen on the clipboard, whole. What is copied is
// what is being looked at: somebody reading a record as hex who presses Copy
// means the hex.
func (p *formPicker) copy() {
	if p.at >= len(p.forms) {
		return
	}
	view := p.forms[p.at].View
	text := view.Text
	if view.Kind == cellview.KindCode {
		text = strings.Join(view.Lines, "\n")
	}
	p.s.app.Clipboard().SetContent(text)
	p.s.status.SetText("Copied the " + strings.ToLower(p.title) + " as " + p.forms[p.at].Name)
}

func (r *recordView) shown() bool {
	return len(r.holder.Objects) == 1 && r.holder.Objects[0] == r.box
}

func (r *recordView) show() {
	r.was = r.holder.Objects
	r.holder.Objects = []fyne.CanvasObject{r.box}
	r.holder.Refresh()
	r.follow()
}

func (r *recordView) hide() {
	r.holder.Objects = r.was
	r.holder.Refresh()
}

// move goes to the record before or after this one, keeping the grid's
// selection with it so that leaving the view lands where the reader is.
func (r *recordView) move(d int) {
	at := r.at + d
	if at < 0 {
		return
	}
	if n, final := r.g.Model().Extent(); final && int64(at) >= n {
		return
	}
	r.g.GoTo(grid.CellID{Row: at, Col: 0})
	r.follow()
}

// follow shows the record the grid is on, reading it if it is not in memory.
func (r *recordView) follow() {
	r.seq++
	c, ok := r.g.Selection().Active()
	if !ok {
		r.where.SetText("No record selected")
		r.when.SetText("Select a record to read it whole.")
		return
	}
	m := r.g.Model()
	if row, loaded := m.Row(r.t.ctx, int64(c.Row)); loaded {
		r.set(c.Row, row)
		return
	}
	r.where.SetText("Reading…")
	seq := r.seq
	go func() {
		rows, err := m.Read(r.t.ctx, int64(c.Row), int64(c.Row)+1)
		r.s.d.Run(func() {
			if seq != r.seq || r.t.ctx.Err() != nil {
				return // another record was asked for, or the tab closed
			}
			if err != nil {
				r.where.SetText("Could not read the record: " + err.Error())
				return
			}
			if len(rows) == 0 {
				r.where.SetText("No record there")
				return
			}
			r.set(c.Row, rows[0])
		})
	}()
}

// set draws one record: where it sat and when, then what it carried.
func (r *recordView) set(at int, row model.Row) {
	cols := r.g.Model().Columns()
	r.at = at
	by := func(name string) (any, model.ColumnDef) {
		for i, c := range cols {
			if c.Name == name && i < len(row) {
				return row[i], c
			}
		}
		return nil, model.ColumnDef{}
	}

	partition, _ := by("partition")
	offset, _ := by("offset")
	r.where.SetText(recordWhere(partition, offset))

	when, whenCol := by("timestamp")
	r.when.SetText(cellview.Prepare(when, whenCol, time.Local).Text)

	key, keyCol := by("key")
	r.key.set(key, keyCol)
	value, valueCol := by("value")
	r.value.set(value, valueCol)

	headers, _ := by("headers")
	r.headers.Objects = nil
	if rows := cellview.HeaderRows(headers); rows != nil {
		r.headers.Add(section("Headers", rows))
	} else {
		r.headers.Add(quietLabel("No headers"))
	}
	r.headers.Refresh()
}

// recordWhere names a record by where it sits: a log and a place in it, which
// is the only name a record has.
func recordWhere(partition, offset any) string {
	p, okP := partition.(int64)
	o, okO := offset.(int64)
	if !okP || !okO {
		return "Record"
	}
	return "Partition " + strconv.FormatInt(p, 10) + ", offset " + strconv.FormatInt(o, 10)
}

// decoderTimeout bounds reading or keeping a decoder choice. It is a local
// file, and a record is being drawn: waiting longer would be worse than
// reading the topic the way it reads by default.
const decoderTimeout = 2 * time.Second

// rememberedDecoder is the decoder this topic's field was last read with, or
// empty where none was kept. A choice that cannot be read is simply not one.
func (s *Shell) rememberedDecoder(connID, field, topic string) string {
	if s.d.Decoders == nil || connID == "" || topic == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), decoderTimeout)
	defer cancel()
	c, ok, err := s.d.Decoders.Decoder(ctx, connID, field, topic)
	if err != nil {
		s.d.Log.Warn("reading how a topic was last read", "err", err)
		return ""
	}
	if !ok {
		return ""
	}
	return c.Name
}

// rememberDecoder keeps how somebody chose to read a topic's field. A choice
// that cannot be kept only means the next reading starts where the first did.
func (s *Shell) rememberDecoder(connID, field, topic, name string) {
	if s.d.Decoders == nil || connID == "" || topic == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), decoderTimeout)
	defer cancel()
	if err := s.d.Decoders.PutDecoder(ctx, connID, field, topic, localdb.DecoderChoice{Name: name}); err != nil {
		s.d.Log.Warn("keeping how a topic is read", "err", err)
	}
}

// resolveDecoders asks the connection's schema registry what this topic's
// records are, where it has one (FR-13.7, T2.70).
//
// A topic with no schema is the ordinary case, and so is a connection with no
// registry: both leave the record readable in the forms its bytes admit, which
// is what the view does without any of this. Only a decoder that was found is
// added, and it is found once — asking per record would be a round trip per
// row drawn.
func (s *Shell) resolveDecoders(t *tab, r *recordView) {
	if t.connID == "" || t.ref.Name() == "" {
		return
	}
	topic := t.ref.Name()
	go func() {
		live, err := s.d.WS.Connect(t.ctx, t.connID)
		if err != nil {
			return
		}
		reg, ok := live.Source.(source.SchemaRegistry)
		if !ok || !live.Source.Capabilities().Stream.SchemaRegistry {
			return
		}
		ctx, cancel := context.WithTimeout(t.ctx, decoderTimeout)
		defer cancel()
		key, _ := reg.Decoder(ctx, topic+"-key")
		value, _ := reg.Decoder(ctx, topic+"-value")
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			// Nil is the ordinary answer: most topics have no schema, and a
			// picker with no decoder offers what the bytes say for themselves.
			r.key.decoder, r.value.decoder = key, value
			if r.shown() {
				r.follow() // draw it again, now that there is more to offer
			}
		})
	}()
}
