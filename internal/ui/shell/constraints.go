package shell

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A table's keys and constraints in the designer (FR-6.2).
//
// They are drawn under the columns, in the same shape: a row of boxes, a
// Remove beside it, and an Add under the lot. Nothing here runs anything
// either — a design is a table on paper until FR-6.4's preview (ADR-0114).
//
// Columns are named by typing them, comma separated, rather than by ticking
// them off a list. A key's columns are ordered, and their order is part of
// what the key is: (a, b) and (b, a) index differently and read differently.
// A list of tick boxes has no order in it.

// actions are what a foreign key can do when the row it refers to goes or
// changes. The empty one leaves the engine to its own default.
var actions = []string{"", string(model.ActionNoAction), string(model.ActionRestrict),
	string(model.ActionCascade), string(model.ActionSetNull), string(model.ActionSetDefault)}

// constraintRows draws every key and constraint on the design.
func (p *designPanel) constraintRows() fyne.CanvasObject {
	pk, uniques, keys, checks := p.design.Constraints()
	box := container.NewVBox(heading("Primary Key"), p.primaryKeyRow(pk))

	box.Add(heading("Unique Constraints"))
	for _, u := range uniques {
		box.Add(p.uniqueRow(u))
	}
	box.Add(widget.NewButton("Add Unique Constraint", func() {
		if p.did(p.design.AddUnique(model.UniqueConstraint{Columns: p.firstColumn()})) {
			p.draw()
		}
	}))

	box.Add(heading("Foreign Keys"))
	for _, f := range keys {
		box.Add(p.foreignKeyRow(f))
	}
	box.Add(widget.NewButton("Add Foreign Key", func() {
		if p.did(p.design.AddForeignKey(model.ForeignKey{Columns: p.firstColumn(),
			RefTable: "another_table", RefColumns: []string{"id"}})) {
			p.draw()
		}
	}))

	box.Add(heading("Check Constraints"))
	for _, c := range checks {
		box.Add(p.checkRow(c))
	}
	box.Add(widget.NewButton("Add Check Constraint", func() {
		if p.did(p.design.AddCheck(model.CheckConstraint{Expression: "true"})) {
			p.draw()
		}
	}))
	return box
}

// primaryKeyRow is the one constraint a table has at most one of, so it is
// drawn as itself rather than as a list.
func (p *designPanel) primaryKeyRow(pk *model.PrimaryKey) fyne.CanvasObject {
	name, cols := widget.NewEntry(), widget.NewEntry()
	name.SetPlaceHolder("named by the engine")
	cols.SetPlaceHolder("no primary key")
	if pk != nil {
		name.SetText(pk.Name)
		cols.SetText(strings.Join(pk.Columns, ", "))
	}
	apply := func(string) {
		if strings.TrimSpace(cols.Text) == "" {
			p.did(nil)
			p.design.DropPrimaryKey()
			p.say()
			return
		}
		p.did(p.design.SetPrimaryKey(name.Text, columnList(cols.Text)))
	}
	name.OnChanged, cols.OnChanged = apply, apply

	drop := widget.NewButton("Remove", func() {
		if p.design.DropPrimaryKey() {
			p.draw()
		}
	})
	return container.NewGridWithColumns(3, cols, name, container.NewHBox(drop))
}

func (p *designPanel) uniqueRow(u model.UniqueConstraint) fyne.CanvasObject {
	name, cols := entryWith(u.Name, "named by the engine"), entryWith(strings.Join(u.Columns, ", "), "columns")
	was := u.Name
	apply := func(string) {
		if p.did(p.design.ChangeUnique(was, model.UniqueConstraint{
			Name: name.Text, Columns: columnList(cols.Text)})) {
			was = strings.TrimSpace(name.Text)
		}
	}
	name.OnChanged, cols.OnChanged = apply, apply
	return container.NewGridWithColumns(3, cols, name,
		container.NewHBox(widget.NewButton("Remove", func() {
			if p.did(p.design.DropUnique(was)) {
				p.draw()
			}
		})))
}

func (p *designPanel) foreignKeyRow(f model.ForeignKey) fyne.CanvasObject {
	name := entryWith(f.Name, "named by the engine")
	cols := entryWith(strings.Join(f.Columns, ", "), "columns")
	refTable := entryWith(qualify(f.RefSchema, f.RefTable), "table it refers to")
	refCols := entryWith(strings.Join(f.RefColumns, ", "), "columns it refers to")
	onDelete := widget.NewSelect(actions, nil)
	onDelete.SetSelected(string(f.OnDelete))
	onUpdate := widget.NewSelect(actions, nil)
	onUpdate.SetSelected(string(f.OnUpdate))

	was := f.Name
	apply := func(string) {
		schema, table := split(refTable.Text)
		if p.did(p.design.ChangeForeignKey(was, model.ForeignKey{
			Name: name.Text, Columns: columnList(cols.Text),
			RefSchema: schema, RefTable: table, RefColumns: columnList(refCols.Text),
			OnDelete: model.ReferentialAction(onDelete.Selected),
			OnUpdate: model.ReferentialAction(onUpdate.Selected),
		})) {
			was = strings.TrimSpace(name.Text)
		}
	}
	name.OnChanged, cols.OnChanged, refTable.OnChanged, refCols.OnChanged = apply, apply, apply, apply
	onDelete.OnChanged = func(string) { apply("") }
	onUpdate.OnChanged = func(string) { apply("") }

	return container.NewVBox(
		container.NewGridWithColumns(3, cols, refTable, refCols),
		container.NewGridWithColumns(3, name,
			container.NewHBox(widget.NewLabel("on delete"), onDelete),
			container.NewHBox(widget.NewLabel("on update"), onUpdate)),
		container.NewHBox(widget.NewButton("Remove", func() {
			if p.did(p.design.DropForeignKey(was)) {
				p.draw()
			}
		})),
		widget.NewSeparator(),
	)
}

func (p *designPanel) checkRow(c model.CheckConstraint) fyne.CanvasObject {
	name := entryWith(c.Name, "named by the engine")
	expr := entryWith(c.Expression, "an expression every row must satisfy")
	was := c.Name
	apply := func(string) {
		if p.did(p.design.ChangeCheck(was, model.CheckConstraint{Name: name.Text, Expression: expr.Text})) {
			was = strings.TrimSpace(name.Text)
		}
	}
	name.OnChanged, expr.OnChanged = apply, apply
	return container.NewGridWithColumns(3, expr, name,
		container.NewHBox(widget.NewButton("Remove", func() {
			if p.did(p.design.DropCheck(was)) {
				p.draw()
			}
		})))
}

// did says what an edit came to: nothing, and the footer says what the
// design holds; or a refusal, and the footer says that instead. It answers
// whether the edit went through, so that a row only follows a rename it
// actually made.
func (p *designPanel) did(err error) bool {
	if err != nil {
		p.t.footer.SetText(err.Error())
		return false
	}
	p.say()
	return true
}

// firstColumn is what a new constraint is offered on: the table's first
// column, because a constraint on no columns cannot be made and something
// has to be there to edit.
func (p *designPanel) firstColumn() []string {
	cols := p.design.Columns()
	if len(cols) == 0 {
		return nil
	}
	return []string{cols[0].Name}
}

// columnList reads a typed list of columns. Empty names are dropped, so a
// trailing comma is not a column called nothing.
func columnList(text string) []string {
	var out []string
	for _, part := range strings.Split(text, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// split reads a table written as schema.table, or as a table alone.
func split(text string) (schema, table string) {
	text = strings.TrimSpace(text)
	if at := strings.LastIndex(text, "."); at >= 0 {
		return text[:at], text[at+1:]
	}
	return "", text
}

func qualify(schema, table string) string {
	if schema == "" {
		return table
	}
	return schema + "." + table
}

func entryWith(text, placeholder string) *widget.Entry {
	e := widget.NewEntry()
	e.SetPlaceHolder(placeholder)
	e.SetText(text)
	return e
}

func heading(text string) fyne.CanvasObject {
	return widget.NewLabelWithStyle(text, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

// sayConstraints adds what the keys and constraints come to, for the footer.
func sayConstraints(changes []app.ConstraintChange) []string {
	var out []string
	for _, c := range changes {
		switch {
		case c.Name != "":
			out = append(out, fmt.Sprintf("%s %s %s", c.Kind, c.Name, c.Change))
		default:
			out = append(out, fmt.Sprintf("%s %s", c.Kind, c.Change))
		}
	}
	return out
}
