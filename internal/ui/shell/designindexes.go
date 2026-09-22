package shell

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A table's indexes in the designer (FR-6.3).
//
// Drawn like its constraints, and typed like them: the columns comma
// separated, each optionally followed by DESC, because an index's order is
// part of what it answers. What an index is over, where an expression, is
// kept as it was typed — it is the engine's language, not this one's.

// indexRows draws every index on the design.
func (p *designPanel) indexRows() fyne.CanvasObject {
	box := container.NewVBox(heading("Indexes"))
	for _, idx := range p.design.Indexes() {
		box.Add(p.indexRow(idx))
	}
	box.Add(widget.NewButton("Add Index", func() {
		if p.did(p.design.AddIndex(model.Index{Columns: indexOn(p.firstColumn())})) {
			p.draw()
		}
	}))
	return box
}

func (p *designPanel) indexRow(idx model.Index) fyne.CanvasObject {
	name := entryWith(idx.Name, "named by the engine")
	cols := entryWith(indexColumnText(idx.Columns), "columns, each optionally DESC")
	include := entryWith(strings.Join(idx.Include, ", "), "columns carried but not indexed")
	method := entryWith(idx.Method, "the engine's default")
	predicate := entryWith(idx.Predicate, "indexes every row")
	unique := widget.NewCheck("Unique", nil)
	unique.SetChecked(idx.Unique)

	was := idx.Name
	apply := func(string) {
		if p.did(p.design.ChangeIndex(was, model.Index{
			Name: name.Text, Columns: parseIndexColumns(cols.Text),
			Include: columnList(include.Text), Method: method.Text,
			Predicate: predicate.Text, Unique: unique.Checked,
		})) {
			was = strings.TrimSpace(name.Text)
		}
	}
	name.OnChanged, cols.OnChanged = apply, apply
	include.OnChanged, method.OnChanged, predicate.OnChanged = apply, apply, apply
	unique.OnChanged = func(bool) { apply("") }

	return container.NewVBox(
		container.NewGridWithColumns(3, cols, name, container.NewHBox(unique)),
		container.NewGridWithColumns(3, method, predicate, include),
		container.NewHBox(widget.NewButton("Remove", func() {
			if p.did(p.design.DropIndex(was)) {
				p.draw()
			}
		})),
		widget.NewSeparator(),
	)
}

// indexOn is a new index's columns, from the table's first column.
func indexOn(names []string) []model.IndexColumn {
	out := make([]model.IndexColumn, len(names))
	for i, n := range names {
		out[i] = model.IndexColumn{Name: n}
	}
	return out
}

// indexColumnText writes an index's columns the way they are typed.
func indexColumnText(cols []model.IndexColumn) string {
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		text := c.Name
		if c.Expression != "" {
			text = c.Expression
		}
		if c.Descending {
			text += " DESC"
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, ", ")
}

// parseIndexColumns reads them back.
//
// A part in parentheses is an expression and is kept whole; anything else is
// a column's name, optionally followed by ASC or DESC. Nothing else is read
// out of it: an index's options beyond these are the engine's own, and this
// does not pretend to know them.
func parseIndexColumns(text string) []model.IndexColumn {
	var out []model.IndexColumn
	for _, part := range strings.Split(text, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var col model.IndexColumn
		if upper := strings.ToUpper(part); strings.HasSuffix(upper, " DESC") {
			col.Descending, part = true, strings.TrimSpace(part[:len(part)-len(" DESC")])
		} else if strings.HasSuffix(upper, " ASC") {
			part = strings.TrimSpace(part[:len(part)-len(" ASC")])
		}
		if strings.HasPrefix(part, "(") {
			col.Expression = part
		} else {
			col.Name = part
		}
		out = append(out, col)
	}
	return out
}
