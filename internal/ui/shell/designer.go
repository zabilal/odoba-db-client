package shell

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/query"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
	"github.com/ikigai-db/ikigai-db/internal/ui/diagram"
	"github.com/ikigai-db/ikigai-db/internal/ui/erd"
)

// The visual query designer (FR-9.1).
//
// A canvas of tables and the joins between them, over the same snapshot a
// diagram and a comparison are read from — so opening one costs what those
// cost and no more, and the columns it offers are the ones that were there
// when it opened rather than a second reading that might differ.
//
// The canvas shows the query and the panels beside it edit it, rather than the
// query being edited by dragging lines between columns. That is a deliberate
// choice about reach: everything here has to be doable from the keyboard
// (NFR-A1), and a join made by picking two columns from two lists is a join
// somebody using a screen reader can make. The lines are the picture of what
// they chose.

// designerPanel is a designer's state.
type designerPanel struct {
	s *Shell
	t *tab

	// db is the schema as it was read, kept so that every redraw is of the
	// same schema: a canvas drawn from a second reading could differ from the
	// one somebody was looking at.
	db *model.Database

	// design is the query being built. It is the whole of the state: the
	// canvas, the lists and the SQL are all drawn from it.
	design *query.Design

	w *diagram.Widget

	// chosen is the table the canvas has selected, by the name the query calls
	// it. Held here rather than read back from the widget because the canvas is
	// rebuilt on every change, and a choice that could not be put back would be
	// lost every time somebody added a table.
	chosenAlias string

	joins   *widget.List
	remove  *widget.Button
	suggest *widget.Button

	// The parts of the query beside the canvas (designerparts.go). Each is a
	// list with the same three buttons, because each is the same kind of thing.
	outputs, wheres, havings, groups, orders *editList

	// distinct and limit are the two parts of a query that are neither a list
	// nor a table: a tick and a number.
	distinct *widget.Check
	limit    *widget.Entry

	// sql is the statement the design has become, updated on every change
	// (FR-9.3). It is read-only: what edits SQL is the editor, and Open as a
	// Query is how a design gets there.
	sql *widget.Entry
}

func designerKey(connID string, ref model.ObjectRef) string {
	return "designer:" + connID + ":" + ref.String()
}

// canDesignQuery reports whether a query can be designed over the selection.
//
// The same question a diagram asks, and for the same reason: both are read
// from a snapshot, and a source that cannot be snapshotted has no schema to
// build a query over. A source with no SQL is refused too — there would be
// nothing to write at the end of it.
func (s *Shell) canDesignQuery() bool {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || !holdsAClass[n.Ref.Kind] {
		return false
	}
	live, open := s.d.WS.Get(conn)
	return open && canDesign(live.Source)
}

// canDesign reports whether a query can be designed over a source: its schema
// has to be readable in one pass, because that is what the canvas is drawn
// from, and it has to have SQL, because that is what a design becomes.
func canDesign(src source.Source) bool {
	if !app.CanCompare(src) {
		return false
	}
	_, hasSQL := src.(source.Dialect)
	return hasSQL
}

func (s *Shell) designQuerySelected() {
	if conn, n, ok := s.Explorer.SelectedNode(); ok && holdsAClass[n.Ref.Kind] {
		s.OpenDesigner(conn, n.Ref)
	}
}

// OpenDesigner opens a designer over a schema, or brings the tab already on it
// forward.
func (s *Shell) OpenDesigner(connID string, ref model.ObjectRef) *tab {
	key := designerKey(connID, ref)
	if t := s.tabFor(key); t != nil {
		s.selectTab(t)
		return t
	}
	ctx, cancel := context.WithCancel(s.ctx)
	t := &tab{key: key, connID: connID, ref: ref, label: ref.Name(), structure: true, ctx: ctx, cancel: cancel,
		body: container.NewStack(quiet("Reading the schema…")), footer: widget.NewLabel("")}
	t.footer.Importance = widget.LowImportance
	t.item = container.NewTabItem("Design: "+ref.Name(),
		container.NewBorder(nil, t.footer, nil, nil, t.body))
	s.open = append(s.open, t)
	s.addTab(t)
	s.sync()

	go func() {
		ctx, cancel := context.WithTimeout(ctx, diagramTimeout)
		defer cancel()
		live, err := s.d.WS.Connect(ctx, connID)
		var db *model.Database
		if err == nil {
			db, err = app.Snapshot(ctx, live.Source, databaseOf(ref))
		}
		s.d.Run(func() {
			if t.ctx.Err() != nil {
				return
			}
			if err != nil {
				s.tabFailed(t, fmt.Errorf("could not read the schema: %w", err))
				return
			}
			s.showDesigner(t, db)
		})
	}()
	return t
}

func (s *Shell) showDesigner(t *tab, db *model.Database) {
	p := &designerPanel{s: s, t: t, db: db, design: &query.Design{}}
	t.designer = p
	p.draw()
}

// draw builds the whole panel from the design. Everything is rebuilt rather
// than patched: a design is small, and a canvas that were patched would be the
// one place in the application where what is on screen and what is held could
// come apart.
func (p *designerPanel) draw() {
	d := erd.FromDesign(p.design, p.db)
	// Whatever has been arranged is kept across a redraw, so that adding a
	// table does not shuffle the ones already placed.
	if p.w != nil {
		keep(p.w.Graph(), d.Graph)
	}
	canvas.Layout(d.Graph, canvas.DefaultLayout())
	p.w = diagram.New(d.Graph, p.s.colours())
	p.w.OnSelect = func(id string) {
		p.chosenAlias = id
		p.refresh()
	}
	p.w.Select(p.chosenAlias)

	p.t.body.Objects = []fyne.CanvasObject{
		container.NewBorder(p.toolbar(), p.sqlView(), nil, p.side(), p.w),
	}
	p.t.body.Refresh()
	// Fitted every time, because every redraw follows a change to what is on
	// the canvas: a table added outside the view would otherwise be added
	// where nobody can see it.
	p.w.Fit()
	p.refresh()
}

// keep puts back where the boxes already on the canvas were, so that adding a
// table arranges the new one around them rather than everything afresh.
func keep(from, to *canvas.Graph) {
	for i := range to.Nodes {
		was := from.NodeByID(to.Nodes[i].ID)
		if was == nil {
			continue
		}
		to.Nodes[i].Pos, to.Nodes[i].Pinned = was.Pos, true
	}
}

// toolbar is what a designer can be told to do.
func (p *designerPanel) toolbar() fyne.CanvasObject {
	p.suggest = widget.NewButton("Suggest Joins", p.applySuggestions)
	p.remove = widget.NewButton("Remove", p.removeChosen)
	return container.NewHBox(
		widget.NewButton("Add a Table…", p.addTable),
		p.suggest,
		p.remove,
		widget.NewButton("Fit", func() { p.w.Fit() }),
		widget.NewButton("−", func() { p.w.Zoom(false) }),
		widget.NewButton("+", func() { p.w.Zoom(true) }),
		widget.NewButton("Open as a Query", p.openAsQuery),
	)
}

// side is the query beside the canvas: the joins, and every other part of a
// SELECT, each a list with the same three buttons so that all of it is
// reachable from a keyboard (NFR-A1).
func (p *designerPanel) side() fyne.CanvasObject {
	p.joins = widget.NewList(
		func() int { return len(p.design.Joins) },
		func() fyne.CanvasObject {
			return container.NewHBox(widget.NewLabel(""), widget.NewButton("Change…", nil),
				widget.NewButton("Remove", nil))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			row := o.(*fyne.Container)
			row.Objects[0].(*widget.Label).SetText(p.joinLabel(i))
			row.Objects[1].(*widget.Button).OnTapped = func() { p.editJoin(i) }
			row.Objects[2].(*widget.Button).OnTapped = func() { p.removeJoin(i) }
		})
	joinHead := widget.NewLabel("Joins")
	joinHead.TextStyle = fyne.TextStyle{Bold: true}
	joins := container.NewBorder(joinHead, nil, nil, nil,
		container.NewGridWrap(fyne.NewSize(380, 140), p.joins))

	p.outputs = newEditList("Columns",
		func() int { return len(p.design.Outputs) }, p.outputLabel,
		func() { p.editOutput(-1) }, p.editOutput, p.removeOutput)
	p.wheres = newEditList("Conditions on rows",
		func() int { return len(p.design.Where) },
		func(i int) string { return conditionLabel(p, p.design.Where, i) },
		func() { p.editCondition(&p.design.Where, -1, false) },
		func(i int) { p.editCondition(&p.design.Where, i, false) },
		func(i int) {
			if removeAt(&p.design.Where, i) {
				p.draw()
			}
		})
	p.havings = newEditList("Conditions on groups",
		func() int { return len(p.design.Having) },
		func(i int) string { return conditionLabel(p, p.design.Having, i) },
		func() { p.editCondition(&p.design.Having, -1, true) },
		func(i int) { p.editCondition(&p.design.Having, i, true) },
		func(i int) {
			if removeAt(&p.design.Having, i) {
				p.draw()
			}
		})
	p.groups = newEditList("Grouped by",
		func() int { return len(p.design.Group) }, p.groupLabel,
		func() { p.editGroup(-1) }, p.editGroup,
		func(i int) {
			if removeAt(&p.design.Group, i) {
				p.draw()
			}
		})
	p.orders = newEditList("Ordered by",
		func() int { return len(p.design.Order) }, p.orderLabel,
		func() { p.editOrder(-1) }, p.editOrder,
		func(i int) {
			if removeAt(&p.design.Order, i) {
				p.draw()
			}
		})

	p.distinct = widget.NewCheck("Only rows that differ", func(on bool) {
		p.design.Distinct = on
		p.refresh()
	})
	p.distinct.SetChecked(p.design.Distinct)
	p.limit = p.limitEntry()

	rows := container.NewVBox(p.distinct,
		container.NewBorder(nil, nil, widget.NewLabel("How many rows"), nil, p.limit))

	return container.NewGridWrap(fyne.NewSize(400, 620), container.NewVScroll(
		container.NewVBox(joins, p.outputs.content, p.wheres.content, p.havings.content,
			p.groups.content, p.orders.content, rows)))
}

// sqlView is the statement the design has become, under the canvas (FR-9.3).
//
// Live and one-way. It is written afresh on every change, which is what "live"
// means and costs nothing: rendering a design is a pure function of it. It is
// not editable, and that is the honest half of "bidirectional": reading SQL
// back into a design means parsing every dialect's SELECT, and a designer that
// accepted an edit and silently dropped what it could not understand would be
// worse than one that does not accept it. Open as a Query is the way out, and
// the editor is where SQL is edited.
func (p *designerPanel) sqlView() fyne.CanvasObject {
	p.sql = widget.NewMultiLineEntry()
	p.sql.Wrapping = fyne.TextWrapOff
	p.sql.TextStyle = fyne.TextStyle{Monospace: true}
	head := widget.NewLabel("SQL")
	head.TextStyle = fyne.TextStyle{Bold: true}
	p.showSQL()
	return container.NewGridWrap(fyne.NewSize(400, 180),
		container.NewBorder(head, nil, nil, nil, p.sql))
}

// showSQL writes what the design is now, or what is wrong with it.
//
// A design that is not a query yet says so in the same place the SQL goes,
// because that is where somebody is looking to find out what it is doing.
func (p *designerPanel) showSQL() {
	if p.sql == nil {
		return
	}
	text := "Add a table to begin."
	if len(p.design.Tables) > 0 {
		if sql, err := query.Render(p.design, p.dialect()); err == nil {
			text = sql
		} else {
			text = "Not a query yet: " + err.Error() + "."
		}
	}
	p.sql.SetText(text)
	// An entry nobody may type in still shows its text, and cannot be typed in.
	p.sql.Disable()
}

// dialect is the connection's, or nil where it has none or has closed.
func (p *designerPanel) dialect() source.Dialect {
	live, open := p.s.d.WS.Get(p.t.connID)
	if !open {
		return nil
	}
	dl, _ := live.Source.(source.Dialect)
	return dl
}

// joinLabel says what a join does, in one line: which tables, which kind, and
// which columns.
func (p *designerPanel) joinLabel(i int) string {
	if i < 0 || i >= len(p.design.Joins) {
		return ""
	}
	j := p.design.Joins[i]
	left, right := p.aliasAt(j.Left), p.aliasAt(j.Right)
	said := strings.TrimSuffix(string(j.Kind), " JOIN") + " " + left + " to " + right
	if len(j.On) > 0 {
		var pairs []string
		for _, pr := range j.On {
			pairs = append(pairs, left+"."+pr.Left+" = "+right+"."+pr.Right)
		}
		said += " on " + strings.Join(pairs, " and ")
	}
	if j.Inferred {
		said += " (from the schema)"
	}
	return said
}

func (p *designerPanel) aliasAt(i int) string {
	if i < 0 || i >= len(p.design.Tables) {
		return "?"
	}
	return p.design.Tables[i].Alias
}

// refresh turns the controls on and off and says what is there.
func (p *designerPanel) refresh() {
	// Nothing here refreshes a list. Every change to what is in one goes
	// through draw, which builds them afresh; a Refresh here would be a line
	// no test could tell the absence of, which is how it was found.
	if p.suggest != nil {
		if len(query.Suggest(p.design, p.db)) > 0 {
			p.suggest.Enable()
		} else {
			p.suggest.Disable()
		}
	}
	// The SQL is written afresh whenever anything changes, which is what
	// "live" means and costs nothing: rendering a design is a pure function
	// of it (FR-9.3).
	p.showSQL()
	if p.remove != nil {
		if p.chosen() >= 0 {
			p.remove.SetText("Remove " + p.aliasAt(p.chosen()))
			p.remove.Enable()
		} else {
			p.remove.SetText("Remove")
			p.remove.Disable()
		}
	}
	p.say()
}

// say tells the footer what the design is, and what is wrong with it if
// anything: a designer that showed nothing until the query was valid would
// leave somebody guessing what it was waiting for.
func (p *designerPanel) say() {
	said := fmt.Sprintf("%s, %s.", nounCount(len(p.design.Tables), "table"),
		nounCount(len(p.design.Joins), "join"))
	switch err := p.design.Validate(); {
	case len(p.design.Tables) == 0:
		said = "Add a table to begin."
	case err != nil:
		said += " " + upperFirst(err.Error()) + "."
	}
	p.t.footer.SetText(said)
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// chosen is the table the canvas has selected, as a place in the design, or -1.
func (p *designerPanel) chosen() int {
	return slices.Index(p.design.Aliases(), p.chosenAlias)
}

// addTable asks which table to add, and adds it.
//
// The list is every table the snapshot holds, with views: a query over a view
// is a query. It is a list rather than a drag from the explorer because the
// explorer may be showing another connection, and because a list can be
// filtered and reached by keyboard.
func (p *designerPanel) addTable() {
	refs := query.TableRefs(p.db, true)
	if len(refs) == 0 {
		p.s.showError(formError("This database has no tables to build a query over."))
		return
	}
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = strings.Join(r.Path[1:], ".")
	}
	pick := widget.NewSelect(names, nil)
	pick.SetSelectedIndex(0)
	d := dialog.NewForm("Add a Table", "Add", "Cancel",
		[]*widget.FormItem{widget.NewFormItem("Table", pick)}, func(ok bool) {
			if !ok {
				return
			}
			at := pick.SelectedIndex()
			if at < 0 || at >= len(refs) {
				return
			}
			p.design.Add(refs[at])
			// What the schema says about the new table is offered rather than
			// applied: a join the catalogue implied is a suggestion, and one
			// somebody accepted is a decision (FR-9.1).
			p.draw()
		}, p.s.win)
	d.Resize(fyne.NewSize(460, d.MinSize().Height))
	d.Show()
}

// applySuggestions takes the joins the foreign keys imply.
func (p *designerPanel) applySuggestions() {
	found := query.Suggest(p.design, p.db)
	if len(found) == 0 {
		return
	}
	query.Apply(p.design, found)
	p.draw()
}

// removeChosen takes the selected table off the canvas, with everything that
// referred to it.
func (p *designerPanel) removeChosen() {
	at := p.chosen()
	if at < 0 {
		return
	}
	p.design.Remove(at)
	p.draw()
}

func (p *designerPanel) removeJoin(i int) {
	if i < 0 || i >= len(p.design.Joins) {
		return
	}
	p.design.Joins = slices.Delete(p.design.Joins, i, i+1)
	p.draw()
}

// editJoin changes a join: its kind, and the columns it matches.
//
// One pair, which is what all but a handful of joins are. A join over two
// columns arrives from a foreign key over two, and this does not take it away:
// the dialog says how many pairs there are and changes the first.
func (p *designerPanel) editJoin(i int) {
	if i < 0 || i >= len(p.design.Joins) {
		return
	}
	j := p.design.Joins[i]
	kinds := []string{string(query.JoinInner), string(query.JoinLeft), string(query.JoinRight),
		string(query.JoinFull), string(query.JoinCross)}
	kind := widget.NewSelect(kinds, nil)
	kind.SetSelected(string(j.Kind))

	left := widget.NewSelect(query.Columns(p.db, p.design.Tables[j.Left].Ref), nil)
	right := widget.NewSelect(query.Columns(p.db, p.design.Tables[j.Right].Ref), nil)
	if len(j.On) > 0 {
		left.SetSelected(j.On[0].Left)
		right.SetSelected(j.On[0].Right)
	}
	items := []*widget.FormItem{
		widget.NewFormItem("Kind", kind),
		widget.NewFormItem(p.aliasAt(j.Left), left),
		widget.NewFormItem(p.aliasAt(j.Right), right),
	}
	if len(j.On) > 1 {
		items = append(items, widget.NewFormItem("",
			quiet(fmt.Sprintf("This join matches %s; the first is the one shown.",
				nounCount(len(j.On), "pair of columns")))))
	}
	d := dialog.NewForm("Change a Join", "Change", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		j := p.design.Joins[i]
		j.Kind = query.JoinKind(kind.Selected)
		// A join somebody has changed is theirs, not the schema's, and the
		// canvas says so: what the catalogue suggested and what a person
		// decided are different things.
		j.Inferred = false
		switch {
		case j.Kind == query.JoinCross:
			// A cross join matches nothing, so its columns go: keeping them
			// would render a statement no server takes.
			j.On = nil
		case left.Selected != "" && right.Selected != "":
			if len(j.On) == 0 {
				j.On = []query.Pair{{}}
			}
			j.On[0] = query.Pair{Left: left.Selected, Right: right.Selected}
		}
		p.design.Joins[i] = j
		p.draw()
	}, p.s.win)
	d.Resize(fyne.NewSize(460, d.MinSize().Height))
	d.Show()
}

// openAsQuery writes the design as SQL and opens it in a query tab.
//
// Unsaved and unrun, as every script this window writes is: a designer builds
// a statement to read, keep or edit (ADR-0011 §15).
func (p *designerPanel) openAsQuery() {
	live, open := p.s.d.WS.Get(p.t.connID)
	if !open {
		p.s.showError(formError("That connection is closed."))
		return
	}
	dl, ok := live.Source.(source.Dialect)
	if !ok {
		p.s.showError(formError("This source has no SQL to write."))
		return
	}
	sql, err := query.Render(p.design, dl)
	if err != nil {
		p.s.showError(fmt.Errorf("this design is not a query yet: %w", err))
		return
	}
	p.s.openScript(p.t.connID, sql+"\n")
}
