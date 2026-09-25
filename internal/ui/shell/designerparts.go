package shell

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/query"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The parts of a query a designer edits (FR-9.2): what comes out, which rows,
// which groups, and in what order.
//
// Every one of them is a list beside the canvas with the same three buttons,
// because they are the same kind of thing: a few items, each of which can be
// added, changed or taken away. Nothing here is edited by dragging, which is
// what makes the whole designer reachable from a keyboard (NFR-A1).
//
// The dialogs share one piece: a table picker and a column picker that follows
// it. That is the only fiddly part of a designer — a column belongs to a table
// on the canvas, and picking a column before a table is picking from a list
// nobody can be sure of.

// picker is a table and one of its columns, chosen together.
type picker struct {
	table  *widget.Select
	column *widget.Select
}

// newPicker builds a table picker and a column picker that follows it, starting
// at a column if one is given.
//
// The table's name is what is shown, not the table's own name: two tables on one
// canvas may be the same table, and the alias is what tells them apart.
func (p *designerPanel) newPicker(at query.Column, withAll bool) *picker {
	k := &picker{}
	k.column = widget.NewSelect(nil, nil)
	k.table = widget.NewSelect(p.design.Aliases(), func(string) {
		k.column.Options = p.columnOptions(k.tableAt(p), withAll)
		if len(k.column.Options) > 0 {
			k.column.SetSelectedIndex(0)
		}
		k.column.Refresh()
	})
	if at.Table >= 0 && at.Table < len(p.design.Tables) {
		k.table.SetSelected(p.design.Tables[at.Table].Alias)
		if at.Name != "" {
			k.column.SetSelected(at.Name)
		}
	} else if len(p.design.Tables) > 0 {
		k.table.SetSelectedIndex(0)
	}
	return k
}

// everyColumn is what stands for a whole table in a column picker.
const everyColumn = "every column"

// columnOptions are the columns of a table on the canvas, with "every column"
// first where that is a thing to choose.
func (p *designerPanel) columnOptions(at int, withAll bool) []string {
	if at < 0 || at >= len(p.design.Tables) {
		return nil
	}
	cols := query.Columns(p.db, p.design.Tables[at].Ref)
	if withAll {
		return append([]string{everyColumn}, cols...)
	}
	return cols
}

// tableAt is the place on the canvas the picker's table is, or -1.
func (k *picker) tableAt(p *designerPanel) int {
	return slices.Index(p.design.Aliases(), k.table.Selected)
}

// chosen is the column the picker names, and whether it means the whole table.
func (k *picker) chosen(p *designerPanel) (query.Column, bool) {
	at := k.tableAt(p)
	if k.column.Selected == everyColumn {
		return query.Column{Table: at}, true
	}
	return query.Column{Table: at, Name: k.column.Selected}, false
}

// items adds the picker's two rows to a form.
func (k *picker) items(label string) []*widget.FormItem {
	return []*widget.FormItem{
		widget.NewFormItem(label, k.table),
		widget.NewFormItem("Column", k.column),
	}
}

// aggregateNames are the aggregates a designer offers, in words: "none" reads
// better at the top of a list than an empty row.
var aggregateNames = []string{"none", "COUNT", "SUM", "MIN", "MAX", "AVG"}

func aggregateOf(name string) query.Aggregate {
	if name == "none" || name == "" {
		return query.AggregateNone
	}
	return query.Aggregate(name)
}

func aggregateName(a query.Aggregate) string {
	if a == query.AggregateNone {
		return "none"
	}
	return string(a)
}

// operatorNames are the conditions a designer offers, in the grid's own words
// so that a condition means the same thing in both places.
var operatorNames = []struct {
	Label string
	Op    source.FilterOp
}{
	{"is", source.OpEqual}, {"is not", source.OpNotEqual},
	{"is less than", source.OpLess}, {"is at most", source.OpLessEqual},
	{"is more than", source.OpGreater}, {"is at least", source.OpGreaterEqual},
	{"is like", source.OpLike}, {"is not like", source.OpNotLike},
	{"is one of", source.OpIn}, {"is none of", source.OpNotIn},
	{"is between", source.OpBetween},
	{"is empty", source.OpIsNull}, {"is not empty", source.OpIsNotNull},
}

func operatorLabels() []string {
	out := make([]string, len(operatorNames))
	for i, o := range operatorNames {
		out[i] = o.Label
	}
	return out
}

func operatorFor(label string) source.FilterOp {
	for _, o := range operatorNames {
		if o.Label == label {
			return o.Op
		}
	}
	return source.OpEqual
}

func operatorLabel(op source.FilterOp) string {
	for _, o := range operatorNames {
		if o.Op == op {
			return o.Label
		}
	}
	return string(op)
}

// takesValues says how many values an operator needs: none for the empty
// tests, two for between, one for everything else.
func takesValues(op source.FilterOp) int {
	switch op {
	case source.OpIsNull, source.OpIsNotNull:
		return 0
	case source.OpBetween:
		return 2
	}
	return 1
}

// editList is the panel every part of a query is edited through: a heading, a
// list, and the three things that can be done to it.
type editList struct {
	list    *widget.List
	add     *widget.Button
	change  *widget.Button
	remove  *widget.Button
	chosen  int
	content fyne.CanvasObject
}

// newEditList builds one. count and label describe the items; add, change and
// remove do the work, change and remove being given the item chosen.
func newEditList(heading string, count func() int, label func(int) string,
	add func(), change func(int), remove func(int)) *editList {
	e := &editList{chosen: -1}
	e.list = widget.NewList(count,
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			o.(*widget.Label).SetText(label(i))
		})
	e.add = widget.NewButton("Add…", add)
	e.change = widget.NewButton("Change…", func() {
		if e.chosen >= 0 {
			change(e.chosen)
		}
	})
	e.remove = widget.NewButton("Remove", func() {
		if e.chosen >= 0 {
			remove(e.chosen)
		}
	})
	e.list.OnSelected = func(i widget.ListItemID) {
		e.chosen = i
		e.refresh()
	}
	e.list.OnUnselected = func(widget.ListItemID) {
		e.chosen = -1
		e.refresh()
	}
	e.refresh()
	head := widget.NewLabel(heading)
	head.TextStyle = fyne.TextStyle{Bold: true}
	e.content = container.NewBorder(head, container.NewHBox(e.add, e.change, e.remove), nil, nil,
		container.NewGridWrap(fyne.NewSize(380, 140), e.list))
	return e
}

// refresh turns the buttons on and off: nothing chosen is nothing to change or
// remove.
//
// Nothing here has to mind a list that lost the item that was chosen, because
// every change to what is in one rebuilds the whole panel and the new list
// starts with nothing chosen. A guard against it was here and came out: no test
// could tell its absence, which is what says it was never reached.
func (e *editList) refresh() {
	if e.chosen < 0 {
		e.change.Disable()
		e.remove.Disable()
		return
	}
	e.change.Enable()
	e.remove.Enable()
}

// form shows a dialog of items and calls done when it is accepted.
func (p *designerPanel) form(title, accept string, items []*widget.FormItem, done func()) {
	d := dialog.NewForm(title, accept, "Cancel", items, func(ok bool) {
		if ok {
			done()
		}
	}, p.s.win)
	d.Resize(fyne.NewSize(480, d.MinSize().Height))
	d.Show()
}

// --- what comes out ------------------------------------------------------

func (p *designerPanel) outputLabel(i int) string {
	if i < 0 || i >= len(p.design.Outputs) {
		return ""
	}
	o := p.design.Outputs[i]
	name := p.aliasAt(o.Column.Table) + "."
	if o.All {
		name += "*"
	} else {
		name += o.Column.Name
	}
	if o.Aggregate != query.AggregateNone {
		name = string(o.Aggregate) + "(" + name + ")"
	}
	if o.Alias != "" {
		name += " as " + o.Alias
	}
	return name
}

// editOutput adds a column to the select list, or changes one.
//
// at is -1 to add. An aggregate and a name of its own are offered together,
// because an aggregate almost always wants one: SUM(total) is a column called
// "sum" on most engines and nothing anybody can address on some.
func (p *designerPanel) editOutput(at int) {
	if len(p.design.Tables) == 0 {
		p.s.showError(formError("Add a table before choosing columns from it."))
		return
	}
	var was query.Output
	if at >= 0 && at < len(p.design.Outputs) {
		was = p.design.Outputs[at]
	} else {
		was.Column.Table = -1
	}
	k := p.newPicker(was.Column, true)
	if was.All {
		k.column.SetSelected(everyColumn)
	}
	agg := widget.NewSelect(aggregateNames, nil)
	agg.SetSelected(aggregateName(was.Aggregate))
	alias := widget.NewEntry()
	alias.SetPlaceHolder("optional")
	alias.SetText(was.Alias)

	items := k.items("Table")
	items = append(items,
		widget.NewFormItem("Summarise", agg),
		widget.NewFormItem("Call it", alias))
	p.form(titleFor(at, "Column"), acceptFor(at), items, func() {
		col, all := k.chosen(p)
		o := query.Output{All: all, Column: col,
			Aggregate: aggregateOf(agg.Selected), Alias: strings.TrimSpace(alias.Text)}
		if at >= 0 && at < len(p.design.Outputs) {
			p.design.Outputs[at] = o
		} else {
			p.design.Outputs = append(p.design.Outputs, o)
		}
		p.draw()
	})
}

func (p *designerPanel) removeOutput(at int) {
	if at < 0 || at >= len(p.design.Outputs) {
		return
	}
	p.design.Outputs = slices.Delete(p.design.Outputs, at, at+1)
	p.draw()
}

// --- which rows, and which groups ---------------------------------------

func conditionLabel(p *designerPanel, list []query.Condition, i int) string {
	if i < 0 || i >= len(list) {
		return ""
	}
	c := list[i]
	name := p.aliasAt(c.Column.Table) + "." + c.Column.Name
	if c.Aggregate != query.AggregateNone {
		name = string(c.Aggregate) + "(" + name + ")"
	}
	said := name + " " + operatorLabel(c.Op)
	switch takesValues(c.Op) {
	case 1:
		said += " " + c.Value
	case 2:
		said += " " + c.Value + " and " + c.Value2
	}
	return said
}

// editCondition adds a condition, or changes one. grouped says it is a
// condition on groups, which is the only place an aggregate belongs.
func (p *designerPanel) editCondition(list *[]query.Condition, at int, grouped bool) {
	if len(p.design.Tables) == 0 {
		p.s.showError(formError("Add a table before setting a condition on it."))
		return
	}
	var was query.Condition
	if at >= 0 && at < len(*list) {
		was = (*list)[at]
	} else {
		was.Column.Table = -1
		was.Op = source.OpEqual
	}
	k := p.newPicker(was.Column, false)
	op := widget.NewSelect(operatorLabels(), nil)
	op.SetSelected(operatorLabel(was.Op))
	first := widget.NewEntry()
	first.SetText(was.Value)
	second := widget.NewEntry()
	second.SetText(was.Value2)
	// A value is the person's own SQL, as the filter bar's typed condition is:
	// so a string needs its quotes, and an expression is allowed.
	first.SetPlaceHolder("a value or an expression, in this database's SQL")
	second.SetPlaceHolder("the upper bound")

	items := k.items("Table")
	// An aggregate is offered only on a condition on groups: an aggregate in a
	// WHERE is not legal SQL anywhere, and offering one there would be offering
	// a design the server will refuse.
	var agg *widget.Select
	if grouped {
		agg = widget.NewSelect(aggregateNames, nil)
		agg.SetSelected(aggregateName(was.Aggregate))
		items = append(items, widget.NewFormItem("Summarise", agg))
	}
	items = append(items,
		widget.NewFormItem("Condition", op),
		widget.NewFormItem("Value", first),
		widget.NewFormItem("And", second))
	p.form(titleFor(at, "Condition"), acceptFor(at), items, func() {
		col, _ := k.chosen(p)
		c := query.Condition{Column: col, Op: operatorFor(op.Selected),
			Value: strings.TrimSpace(first.Text), Value2: strings.TrimSpace(second.Text)}
		if agg != nil {
			c.Aggregate = aggregateOf(agg.Selected)
		}
		// A condition that takes no value is given none, whatever was typed:
		// the design would refuse it, and refusing what somebody can see is
		// empty would be pedantry rather than help.
		switch takesValues(c.Op) {
		case 0:
			c.Value, c.Value2 = "", ""
		case 1:
			c.Value2 = ""
		}
		if at >= 0 && at < len(*list) {
			(*list)[at] = c
		} else {
			*list = append(*list, c)
		}
		p.draw()
	})
}

func removeAt[T any](list *[]T, at int) bool {
	if at < 0 || at >= len(*list) {
		return false
	}
	*list = slices.Delete(*list, at, at+1)
	return true
}

// --- grouping and ordering ----------------------------------------------

func (p *designerPanel) groupLabel(i int) string {
	if i < 0 || i >= len(p.design.Group) {
		return ""
	}
	c := p.design.Group[i]
	return p.aliasAt(c.Table) + "." + c.Name
}

func (p *designerPanel) editGroup(at int) {
	if len(p.design.Tables) == 0 {
		p.s.showError(formError("Add a table before grouping by one of its columns."))
		return
	}
	was := query.Column{Table: -1}
	if at >= 0 && at < len(p.design.Group) {
		was = p.design.Group[at]
	}
	k := p.newPicker(was, false)
	p.form(titleFor(at, "Grouping"), acceptFor(at), k.items("Table"), func() {
		col, _ := k.chosen(p)
		if at >= 0 && at < len(p.design.Group) {
			p.design.Group[at] = col
		} else {
			p.design.Group = append(p.design.Group, col)
		}
		p.draw()
	})
}

func (p *designerPanel) orderLabel(i int) string {
	if i < 0 || i >= len(p.design.Order) {
		return ""
	}
	s := p.design.Order[i]
	name := p.aliasAt(s.Column.Table) + "." + s.Column.Name
	if s.Aggregate != query.AggregateNone {
		name = string(s.Aggregate) + "(" + name + ")"
	}
	if s.Descending {
		return name + ", largest first"
	}
	return name + ", smallest first"
}

func (p *designerPanel) editOrder(at int) {
	if len(p.design.Tables) == 0 {
		p.s.showError(formError("Add a table before ordering by one of its columns."))
		return
	}
	var was query.Sort
	if at >= 0 && at < len(p.design.Order) {
		was = p.design.Order[at]
	} else {
		was.Column.Table = -1
	}
	k := p.newPicker(was.Column, false)
	agg := widget.NewSelect(aggregateNames, nil)
	agg.SetSelected(aggregateName(was.Aggregate))
	down := widget.NewCheck("Largest first", nil)
	down.SetChecked(was.Descending)
	items := append(k.items("Table"),
		widget.NewFormItem("Summarise", agg),
		widget.NewFormItem("", down))
	p.form(titleFor(at, "Ordering"), acceptFor(at), items, func() {
		col, _ := k.chosen(p)
		s := query.Sort{Column: col, Aggregate: aggregateOf(agg.Selected), Descending: down.Checked}
		if at >= 0 && at < len(p.design.Order) {
			p.design.Order[at] = s
		} else {
			p.design.Order = append(p.design.Order, s)
		}
		p.draw()
	})
}

// titleFor and acceptFor say whether a dialog is adding or changing, so that
// one function serves both and the button says which.
func titleFor(at int, what string) string {
	if at < 0 {
		return "Add a " + strings.ToLower(what)
	}
	return "Change a " + strings.ToLower(what)
}

func acceptFor(at int) string {
	if at < 0 {
		return "Add"
	}
	return "Change"
}

// --- how many rows -------------------------------------------------------

// limitEntry is the bound on the rows, as a number somebody types.
//
// Empty means no bound, which is what somebody writing the SQL by hand would
// get; what the editor reads is bounded either way (NFR-P11). Anything that is
// not a number is left as it was rather than taken as zero.
func (p *designerPanel) limitEntry() *widget.Entry {
	e := widget.NewEntry()
	e.SetPlaceHolder("all of them")
	if p.design.Limit > 0 {
		e.SetText(strconv.FormatInt(p.design.Limit, 10))
	}
	e.Validator = func(text string) error {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return fmt.Errorf("that is not a number of rows")
		}
		if n < 0 {
			return fmt.Errorf("a number of rows cannot be negative")
		}
		return nil
	}
	e.OnChanged = func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			p.design.Limit = 0
			p.refresh()
			return
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || n < 0 {
			return // said by the validator; the design keeps what it had
		}
		p.design.Limit = n
		p.refresh()
	}
	return e
}
