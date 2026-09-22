package shell

import (
	"context"
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The column editor (FR-6.1).
//
// It changes a table on paper and nothing on the server. What the change is
// rendered as, and the rule that nobody runs one without reading it first,
// is FR-6.4 and comes with the preview; until then a design is a thing
// somebody can build and look at, and the footer says what they have built.
//
// Every field is a plain box rather than a list of types to pick from. A
// type is the engine's own word for it — varchar(40), numeric(10,2),
// timestamptz — and the seven engines here do not agree on those. A chooser
// would have to be wrong somewhere, and the box is right everywhere; what a
// dialect will not accept, the preview will say before it runs.

// designKey names a table's design tab, so that designing the same table
// twice brings the first one forward.
func designKey(connID string, ref model.ObjectRef) string {
	return "design:" + connID + ":" + ref.String()
}

// designPanel is the column editor's state: the design itself, and the rows
// drawn for it.
type designPanel struct {
	s      *Shell
	t      *tab
	design *app.Design

	rows *fyne.Container
	add  *widget.Button
	undo *widget.Button
}

// canDesign reports whether the selection is a table this can design.
func (s *Shell) canDesign() bool {
	_, n, ok := s.structureTarget()
	return ok && n.Ref.Kind == model.KindTable
}

func (s *Shell) designSelected() {
	if conn, n, ok := s.structureTarget(); ok && n.Ref.Kind == model.KindTable {
		s.OpenDesign(conn, n)
	}
}

// OpenDesign opens a table's columns for editing, or brings the tab already
// open on it forward. Like every other tab it never blocks: the table is
// read in the background.
func (s *Shell) OpenDesign(connID string, n model.Node) *tab {
	key := designKey(connID, n.Ref)
	if t := s.tabFor(key); t != nil {
		s.selectTab(t)
		return t
	}
	ctx, cancel := context.WithCancel(s.ctx)
	t := &tab{key: key, connID: connID, ref: n.Ref, label: n.Label, structure: true, ctx: ctx, cancel: cancel,
		body: container.NewStack(quiet("Reading the table…")), footer: widget.NewLabel("")}
	t.footer.Importance = widget.LowImportance
	t.item = container.NewTabItem("Design: "+n.Label, container.NewBorder(nil, t.footer, nil, nil, t.body))
	s.open = append(s.open, t)
	s.addTab(t)
	s.sync()

	go func() {
		live, err := s.d.WS.Connect(ctx, connID)
		var desc any
		if err == nil {
			desc, err = app.Describe(ctx, live.Source, n.Ref)
		}
		s.d.Run(func() {
			if ctx.Err() != nil {
				return // the tab closed while it was reading
			}
			if err != nil {
				s.tabFailed(t, fmt.Errorf("could not read the table: %w", err))
				s.crashed(connID, err)
				return
			}
			table, ok := desc.(*model.Table)
			if !ok {
				s.tabFailed(t, fmt.Errorf("%s is not a table", n.Label))
				return
			}
			s.showDesign(t, table)
		})
	}()
	return t
}

// showDesign draws the editor over a table that has been read.
func (s *Shell) showDesign(t *tab, table *model.Table) {
	p := &designPanel{s: s, t: t, design: app.NewDesign(t.ref, table), rows: container.NewVBox()}
	t.design = p
	p.add = widget.NewButton("Add Column", p.addColumn)
	p.undo = widget.NewButton("Revert All", func() {
		p.design.RevertAll()
		p.draw()
	})
	head := container.NewHBox(p.add, p.undo)
	t.body.Objects = []fyne.CanvasObject{
		container.NewBorder(head, nil, nil, nil, container.NewVScroll(p.rows)),
	}
	p.draw()
	t.body.Refresh()
}

// draw lays the columns out again, one row each, and says what has changed.
func (p *designPanel) draw() {
	p.rows.Objects = nil
	for _, c := range p.design.Columns() {
		p.rows.Add(p.row(c))
	}
	p.rows.Refresh()
	p.say()
}

// row is one column's fields. Each one writes its change as it is made, so
// that what the footer says is always what the design holds.
func (p *designPanel) row(c model.Column) fyne.CanvasObject {
	name := widget.NewEntry()
	name.SetText(c.Name)
	name.Validator = notBlank("a column needs a name")

	kind := widget.NewEntry()
	kind.SetText(typeText(c.Type))
	kind.Validator = notBlank("a column needs a type")

	null := widget.NewCheck("Nullable", nil)
	null.SetChecked(c.Type.Nullable)

	def := widget.NewEntry()
	def.SetPlaceHolder("no default")
	def.SetText(c.Default)

	identity := widget.NewCheck("Identity", nil)
	identity.SetChecked(c.Identity || c.AutoIncrement)

	comment := widget.NewEntry()
	comment.SetPlaceHolder("no comment")
	comment.SetText(c.Comment)

	was := c.Name
	apply := func(string) {
		edited := c
		edited.Name = name.Text
		edited.Type = typeOf(kind.Text, c.Type)
		edited.Type.Nullable = null.Checked
		edited.Default, edited.HasDefault = def.Text, strings.TrimSpace(def.Text) != ""
		edited.Identity = identity.Checked
		if c.AutoIncrement {
			edited.AutoIncrement = identity.Checked
			edited.Identity = c.Identity
		}
		edited.Comment = comment.Text
		if err := p.design.ChangeColumn(was, edited); err != nil {
			p.t.footer.SetText(err.Error())
			return
		}
		was = strings.TrimSpace(name.Text)
		p.say()
	}
	name.OnChanged, kind.OnChanged, def.OnChanged, comment.OnChanged = apply, apply, apply, apply
	null.OnChanged = func(bool) { apply("") }
	identity.OnChanged = func(bool) { apply("") }

	drop := widget.NewButton("Remove", func() {
		if err := p.design.DropColumn(was); err != nil {
			p.t.footer.SetText(err.Error())
			return
		}
		p.draw()
	})
	revert := widget.NewButton("Revert", func() {
		if p.design.Revert(was) {
			p.draw()
		}
	})

	return container.NewVBox(
		container.NewGridWithColumns(3, name, kind, container.NewHBox(null, identity)),
		container.NewGridWithColumns(3, def, comment, container.NewHBox(revert, drop)),
		widget.NewSeparator(),
	)
}

// addColumn puts a new nullable text column at the end, which is the one
// shape every engine here has: somebody renames it and gives it a type.
func (p *designPanel) addColumn() {
	name := fmt.Sprintf("column_%d", len(p.design.Columns())+1)
	err := p.design.AddColumn(model.Column{Name: name, Type: model.DataType{
		Class: model.TypeString, Native: "text", Nullable: true, Length: -1}})
	if err != nil {
		p.t.footer.SetText(err.Error())
		return
	}
	p.draw()
}

// say writes what the design holds into the footer, which is the only place
// a change is visible until the preview exists.
func (p *designPanel) say() {
	changes := p.design.Changes()
	p.undo.Enable()
	if len(changes) == 0 {
		p.undo.Disable()
		p.t.footer.SetText("No changes yet.")
		return
	}
	var said []string
	for _, c := range changes {
		switch {
		case c.Renamed():
			said = append(said, fmt.Sprintf("%s renamed to %s", c.Name, c.To.Name))
		default:
			said = append(said, fmt.Sprintf("%s %s", c.Name, c.Kind))
		}
	}
	p.t.footer.SetText(fmt.Sprintf("%s: %s", nounCount(len(changes), "change"), strings.Join(said, ", ")))
}

// typeText is a column's type as somebody would type it, which is the
// engine's own word for it.
func typeText(t model.DataType) string {
	if n := strings.TrimSpace(t.Native); n != "" {
		return n
	}
	return t.Class.String()
}

// typeOf reads a typed type back. The class is kept from what was read
// wherever the word did not change, because a lexer here would be guessing
// at seven engines' type systems; where it did change, the class is unknown
// until the server says otherwise, and the preview is where that is settled.
func typeOf(text string, was model.DataType) model.DataType {
	out := was
	out.Native = strings.TrimSpace(text)
	if !strings.EqualFold(out.Native, strings.TrimSpace(was.Native)) {
		out.Length, out.Precision, out.Scale = -1, 0, 0
	}
	return out
}

// notBlank refuses an empty box, which is what makes the field say so rather
// than the footer saying it after the fact.
func notBlank(why string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("%s", why)
		}
		return nil
	}
}
