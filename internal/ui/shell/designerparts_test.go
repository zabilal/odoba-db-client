package shell

import (
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/query"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The parts of a query a designer edits (FR-9.2), and the SQL it becomes as
// they change (FR-9.3).

// twoTables is a designer over orders joined to items, which is the smallest
// design with something to choose from.
func twoTables(t *testing.T) (*fixture, *tab, *designerPanel) {
	t.Helper()
	fx, tb, p := designingQuery(t)
	addTable(t, fx, p, "main.orders")
	addTable(t, fx, p, "main.items")
	test.Tap(p.suggest)
	return fx, tb, p
}

// top is the dialog on screen, which must be there.
func top(t *testing.T, fx *fixture) fyne.CanvasObject {
	t.Helper()
	o := fx.s.win.Canvas().Overlays().Top()
	if o == nil {
		t.Fatal("it asked nothing")
	}
	return o
}

// A column chosen is in the select list, and in the SQL.
func TestAColumnChosenIsInTheSQL(t *testing.T) {
	fx, _, p := twoTables(t)
	p.editOutput(-1)
	o := top(t, fx)
	picks := selectsIn(o)
	if len(picks) < 3 {
		t.Fatalf("it offered %d pickers", len(picks))
	}
	picks[0].SetSelected("items") // the table
	picks[1].SetSelected("name")  // the column
	test.Tap(findButton(o, "Add"))

	if len(p.design.Outputs) != 1 {
		t.Fatalf("it selects %+v", p.design.Outputs)
	}
	if got := p.outputLabel(0); got != "items.name" {
		t.Errorf("the list says %q", got)
	}
	if sql := p.sql.Text; !strings.Contains(sql, `SELECT "items"."name"`) {
		t.Errorf("the SQL reads\n%s", sql)
	}
}

// Picking a table changes what columns are on offer: a column belongs to a
// table, and offering another table's would offer a query no server takes.
func TestTheColumnsOfferedFollowTheTable(t *testing.T) {
	fx, _, p := twoTables(t)
	p.editOutput(-1)
	o := top(t, fx)
	picks := selectsIn(o)
	picks[0].SetSelected("orders")
	orders := append([]string(nil), picks[1].Options...)
	picks[0].SetSelected("items")
	items := append([]string(nil), picks[1].Options...)
	if strings.Join(orders, ",") == strings.Join(items, ",") {
		t.Errorf("both tables offer %v", orders)
	}
	// Every column is first, because a table with nothing picked from it means
	// all of them.
	if items[0] != everyColumn {
		t.Errorf("the list starts %q", items[0])
	}
}

// Every column of one table is t.*, which is what a table with nothing picked
// from it means when another has columns picked.
func TestEveryColumnOfATableCanBeChosen(t *testing.T) {
	fx, _, p := twoTables(t)
	p.editOutput(-1)
	o := top(t, fx)
	picks := selectsIn(o)
	picks[0].SetSelected("items")
	picks[1].SetSelected(everyColumn)
	test.Tap(findButton(o, "Add"))
	if len(p.design.Outputs) != 1 || !p.design.Outputs[0].All {
		t.Fatalf("it selects %+v", p.design.Outputs)
	}
	if got := p.outputLabel(0); got != "items.*" {
		t.Errorf("the list says %q", got)
	}
	if !strings.Contains(p.sql.Text, `"items".*`) {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}
}

// An aggregate and a name of its own go together, because an aggregate almost
// always wants one.
func TestAnAggregateIsChosenWithAName(t *testing.T) {
	fx, _, p := twoTables(t)
	p.editOutput(-1)
	o := top(t, fx)
	picks := selectsIn(o)
	picks[0].SetSelected("orders")
	picks[1].SetSelected("item")
	picks[2].SetSelected("COUNT")
	entriesIn(o)[0].SetText("how many")
	test.Tap(findButton(o, "Add"))

	if got := p.outputLabel(0); got != "COUNT(orders.item) as how many" {
		t.Errorf("the list says %q", got)
	}
	if !strings.Contains(p.sql.Text, `COUNT("orders"."item") AS "how many"`) {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}
}

// A query with an aggregate needs its grouping, and the designer says so in the
// place the SQL goes rather than leaving it to the server.
func TestAnAggregateWithoutItsGroupingSaysSo(t *testing.T) {
	_, _, p := twoTables(t)
	p.design.Outputs = []query.Output{
		{Column: query.Column{Table: 0, Name: "id"}},
		{Column: query.Column{Table: 0, Name: "item"}, Aggregate: query.AggregateCount},
	}
	p.draw()
	if !strings.Contains(p.sql.Text, "Not a query yet") {
		t.Fatalf("the SQL reads\n%s", p.sql.Text)
	}
	if !strings.Contains(p.sql.Text, "orders.id") {
		t.Errorf("it does not name the column: %q", p.sql.Text)
	}
	// Grouped, it is a query.
	p.design.Group = []query.Column{{Table: 0, Name: "id"}}
	p.draw()
	if !strings.Contains(p.sql.Text, `GROUP BY "orders"."id"`) {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}
}

// A condition on rows, chosen through the same picker.
func TestAConditionOnRows(t *testing.T) {
	fx, _, p := twoTables(t)
	p.editCondition(&p.design.Where, -1, false)
	o := top(t, fx)
	picks := selectsIn(o)
	picks[0].SetSelected("orders")
	picks[1].SetSelected("id")
	picks[2].SetSelected("is more than")
	entriesIn(o)[0].SetText("10")
	test.Tap(findButton(o, "Add"))

	if len(p.design.Where) != 1 {
		t.Fatalf("its conditions are %+v", p.design.Where)
	}
	if got := conditionLabel(p, p.design.Where, 0); got != "orders.id is more than 10" {
		t.Errorf("the list says %q", got)
	}
	if !strings.Contains(p.sql.Text, `WHERE "orders"."id" > 10`) {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}
}

// A condition that takes no value is given none, whatever was typed: refusing
// what somebody can see is empty would be pedantry rather than help.
func TestAConditionThatTakesNoValueIsGivenNone(t *testing.T) {
	fx, _, p := twoTables(t)
	p.editCondition(&p.design.Where, -1, false)
	o := top(t, fx)
	picks := selectsIn(o)
	picks[0].SetSelected("orders")
	picks[1].SetSelected("id")
	picks[2].SetSelected("is empty")
	entriesIn(o)[0].SetText("ignored")
	entriesIn(o)[1].SetText("also ignored")
	test.Tap(findButton(o, "Add"))
	if len(p.design.Where) != 1 {
		t.Fatalf("its conditions are %+v", p.design.Where)
	}
	if c := p.design.Where[0]; c.Value != "" || c.Value2 != "" {
		t.Errorf("it kept %q and %q", c.Value, c.Value2)
	}
	if !strings.Contains(p.sql.Text, `"orders"."id" IS NULL`) {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}
}

// Between keeps both bounds, and one value keeps only the first.
func TestHowManyValuesEachConditionKeeps(t *testing.T) {
	fx, _, p := twoTables(t)
	for name, c := range map[string]struct {
		op            string
		first, second string
		want          string
	}{
		"between": {"is between", "1", "9", `BETWEEN 1 AND 9`},
		"one":     {"is more than", "1", "9", `> 1`},
		"one of":  {"is one of", "1, 2", "9", `IN (1, 2)`},
	} {
		t.Run(name, func(t *testing.T) {
			p.design.Where = nil
			p.editCondition(&p.design.Where, -1, false)
			o := top(t, fx)
			picks := selectsIn(o)
			picks[0].SetSelected("orders")
			picks[1].SetSelected("id")
			picks[2].SetSelected(c.op)
			entriesIn(o)[0].SetText(c.first)
			entriesIn(o)[1].SetText(c.second)
			test.Tap(findButton(o, "Add"))
			if !strings.Contains(p.sql.Text, c.want) {
				t.Errorf("the SQL reads\n%s\nwhich lacks %q", p.sql.Text, c.want)
			}
		})
	}
}

// A condition on groups may summarise; one on rows may not, because an
// aggregate in a WHERE is not legal SQL anywhere.
func TestOnlyAConditionOnGroupsIsOfferedAnAggregate(t *testing.T) {
	fx, _, p := twoTables(t)
	p.editCondition(&p.design.Where, -1, false)
	onRows := len(selectsIn(top(t, fx)))
	test.Tap(findButton(top(t, fx), "Cancel"))
	p.editCondition(&p.design.Having, -1, true)
	onGroups := len(selectsIn(top(t, fx)))
	if onGroups != onRows+1 {
		t.Errorf("a condition on rows offers %d pickers and one on groups %d", onRows, onGroups)
	}
}

// A condition on groups, which is what HAVING is for.
func TestAConditionOnGroups(t *testing.T) {
	fx, _, p := twoTables(t)
	p.design.Outputs = []query.Output{
		{Column: query.Column{Table: 0, Name: "item"}},
		{Column: query.Column{Table: 0, Name: "id"}, Aggregate: query.AggregateCount},
	}
	p.design.Group = []query.Column{{Table: 0, Name: "item"}}
	p.draw()
	p.editCondition(&p.design.Having, -1, true)
	o := top(t, fx)
	picks := selectsIn(o)
	picks[0].SetSelected("orders")
	picks[1].SetSelected("id")
	picks[2].SetSelected("COUNT")
	picks[3].SetSelected("is more than")
	entriesIn(o)[0].SetText("2")
	test.Tap(findButton(o, "Add"))
	if !strings.Contains(p.sql.Text, `HAVING COUNT("orders"."id") > 2`) {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}
	if got := conditionLabel(p, p.design.Having, 0); got != "COUNT(orders.id) is more than 2" {
		t.Errorf("the list says %q", got)
	}
}

// Grouping and ordering, each through the same picker.
func TestGroupingAndOrdering(t *testing.T) {
	fx, _, p := twoTables(t)
	p.editGroup(-1)
	o := top(t, fx)
	picks := selectsIn(o)
	picks[0].SetSelected("orders")
	picks[1].SetSelected("item")
	test.Tap(findButton(o, "Add"))
	if got := p.groupLabel(0); got != "orders.item" {
		t.Errorf("the list says %q", got)
	}
	if !strings.Contains(p.sql.Text, `GROUP BY "orders"."item"`) {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}

	p.editOrder(-1)
	o = top(t, fx)
	picks = selectsIn(o)
	picks[0].SetSelected("orders")
	picks[1].SetSelected("id")
	findCheck(o, "Largest first").SetChecked(true)
	test.Tap(findButton(o, "Add"))
	if got := p.orderLabel(0); got != "orders.id, largest first" {
		t.Errorf("the list says %q", got)
	}
	if !strings.Contains(p.sql.Text, `ORDER BY "orders"."id" DESC`) {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}
}

// Only rows that differ, which is DISTINCT.
func TestOnlyRowsThatDiffer(t *testing.T) {
	_, _, p := twoTables(t)
	p.distinct.SetChecked(true)
	if !strings.Contains(p.sql.Text, "SELECT DISTINCT") {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}
	p.distinct.SetChecked(false)
	if strings.Contains(p.sql.Text, "DISTINCT") {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}
}

// How many rows, as a number somebody types. Empty is no bound, which is what
// writing the SQL by hand would give; anything that is not a number leaves the
// design as it was.
func TestHowManyRows(t *testing.T) {
	_, _, p := twoTables(t)
	p.limit.SetText("25")
	if p.design.Limit != 25 {
		t.Fatalf("it kept %d", p.design.Limit)
	}
	if !strings.Contains(p.sql.Text, "LIMIT 25") {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}
	p.limit.SetText("not a number")
	if p.design.Limit != 25 {
		t.Errorf("text became %d", p.design.Limit)
	}
	if err := p.limit.Validate(); err == nil {
		t.Error("it says nothing about text where a number goes")
	}
	p.limit.SetText("-1")
	if p.design.Limit != 25 {
		t.Errorf("a negative number became %d", p.design.Limit)
	}
	if err := p.limit.Validate(); err == nil {
		t.Error("it says nothing about a negative number of rows")
	}
	p.limit.SetText("")
	if p.design.Limit != 0 {
		t.Errorf("empty left %d", p.design.Limit)
	}
	if strings.Contains(p.sql.Text, "LIMIT") {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}
}

// Anything chosen can be changed and taken away, and the buttons are off until
// something is chosen.
func TestEveryPartCanBeChangedAndRemoved(t *testing.T) {
	fx, _, p := twoTables(t)
	// One of each.
	p.design.Outputs = []query.Output{{Column: query.Column{Table: 0, Name: "id"}}}
	p.design.Where = []query.Condition{{Column: query.Column{Table: 0, Name: "id"},
		Op: source.OpEqual, Value: "1"}}
	p.design.Having = []query.Condition{{Column: query.Column{Table: 0, Name: "id"},
		Op: source.OpEqual, Aggregate: query.AggregateCount, Value: "1"}}
	p.design.Group = []query.Column{{Table: 0, Name: "id"}}
	p.design.Order = []query.Sort{{Column: query.Column{Table: 0, Name: "id"}}}
	p.draw()

	for name, e := range map[string]*editList{
		"columns":              p.outputs,
		"conditions on rows":   p.wheres,
		"conditions on groups": p.havings,
		"grouped by":           p.groups,
		"ordered by":           p.orders,
	} {
		t.Run(name, func(t *testing.T) {
			if !e.change.Disabled() || !e.remove.Disabled() {
				t.Fatal("nothing is chosen and the buttons are on")
			}
			e.list.Select(0)
			if e.change.Disabled() || e.remove.Disabled() {
				t.Fatal("something is chosen and the buttons are off")
			}
			test.Tap(e.remove)
		})
	}
	if len(p.design.Outputs) != 0 || len(p.design.Where) != 0 || len(p.design.Having) != 0 ||
		len(p.design.Group) != 0 || len(p.design.Order) != 0 {
		t.Errorf("something survived: %+v", p.design)
	}
	// Removing what is not there changes nothing.
	p.removeOutput(-1)
	p.removeOutput(9)
	if removeAt(&p.design.Where, 0) {
		t.Error("it removed a condition from an empty list")
	}
	_ = fx
}

// Changing a column keeps its place in the list rather than adding another.
func TestChangingAColumnKeepsItsPlace(t *testing.T) {
	fx, _, p := twoTables(t)
	p.design.Outputs = []query.Output{
		{Column: query.Column{Table: 0, Name: "id"}},
		{Column: query.Column{Table: 0, Name: "item"}},
	}
	p.draw()
	p.editOutput(0)
	o := top(t, fx)
	picks := selectsIn(o)
	picks[1].SetSelected("item")
	test.Tap(findButton(o, "Change"))
	if len(p.design.Outputs) != 2 {
		t.Fatalf("it selects %+v", p.design.Outputs)
	}
	if p.design.Outputs[0].Column.Name != "item" {
		t.Errorf("the first is %q", p.design.Outputs[0].Column.Name)
	}
}

// Nothing can be chosen before there is a table to choose from, and the
// designer says so rather than offering an empty picker.
func TestNothingIsChosenBeforeThereIsATable(t *testing.T) {
	fx, _, p := designingQuery(t)
	for name, open := range map[string]func(){
		"a column":    func() { p.editOutput(-1) },
		"a condition": func() { p.editCondition(&p.design.Where, -1, false) },
		"a grouping":  func() { p.editGroup(-1) },
		"an ordering": func() { p.editOrder(-1) },
	} {
		t.Run(name, func(t *testing.T) {
			fx.s.errors.dismiss()
			open()
			if fx.s.win.Canvas().Overlays().Top() != nil {
				t.Error("it offered a picker with nothing to pick from")
			}
			if !fx.s.errors.shown() {
				t.Error("it said nothing")
			}
		})
	}
}

// The SQL is read-only: what edits SQL is the editor, and Open as a Query is
// how a design gets there.
func TestTheSQLViewIsNotEdited(t *testing.T) {
	_, _, p := twoTables(t)
	if !p.sql.Disabled() {
		t.Error("the SQL can be typed in, and nothing reads it back")
	}
}

// A dialog opens on something: the first table and its first column when there
// is nothing to change, and the current ones when there is. A picker that
// opened on nothing would make every edit two more clicks than it needs.
func TestADialogOpensOnSomething(t *testing.T) {
	fx, _, p := twoTables(t)
	p.editOutput(-1)
	o := top(t, fx)
	picks := selectsIn(o)
	if picks[0].Selected != "orders" {
		t.Errorf("it opened on the table %q", picks[0].Selected)
	}
	if picks[1].Selected == "" {
		t.Error("it opened on no column")
	}
	// An output with no aggregate opens on "none" rather than on nothing.
	if picks[2].Selected != "none" {
		t.Errorf("it opened on the aggregate %q", picks[2].Selected)
	}
	test.Tap(findButton(o, "Cancel"))

	// Changing one opens on what it is.
	p.design.Outputs = []query.Output{{Column: query.Column{Table: 1, Name: "name"},
		Aggregate: query.AggregateCount, Alias: "how many"}}
	p.draw()
	p.editOutput(0)
	o = top(t, fx)
	picks = selectsIn(o)
	if picks[0].Selected != "items" || picks[1].Selected != "name" {
		t.Errorf("it opened on %q.%q", picks[0].Selected, picks[1].Selected)
	}
	if picks[2].Selected != "COUNT" {
		t.Errorf("it opened on the aggregate %q", picks[2].Selected)
	}
	if got := entriesIn(o)[0].Text; got != "how many" {
		t.Errorf("it opened on the name %q", got)
	}
}

// "none" is not an aggregate, however it is spelt in the list.
func TestNoneIsNotAnAggregate(t *testing.T) {
	fx, _, p := twoTables(t)
	p.editOutput(-1)
	o := top(t, fx)
	picks := selectsIn(o)
	picks[0].SetSelected("orders")
	picks[1].SetSelected("id")
	picks[2].SetSelected("none")
	test.Tap(findButton(o, "Add"))
	if len(p.design.Outputs) != 1 {
		t.Fatalf("it selects %+v", p.design.Outputs)
	}
	if got := p.design.Outputs[0].Aggregate; got != query.AggregateNone {
		t.Errorf("it kept the aggregate %q", got)
	}
	if strings.Contains(p.sql.Text, "none(") {
		t.Errorf("the SQL reads\n%s", p.sql.Text)
	}
}

// Only a column picker that may mean a whole table offers one: a condition on
// "every column" is not a condition, and a grouping by one is not a grouping.
func TestOnlyASelectListOffersAWholeTable(t *testing.T) {
	fx, _, p := twoTables(t)
	for name, open := range map[string]func(){
		"a condition": func() { p.editCondition(&p.design.Where, -1, false) },
		"a grouping":  func() { p.editGroup(-1) },
		"an ordering": func() { p.editOrder(-1) },
	} {
		t.Run(name, func(t *testing.T) {
			open()
			o := top(t, fx)
			if slices.Contains(selectsIn(o)[1].Options, everyColumn) {
				t.Errorf("it offers %v", selectsIn(o)[1].Options)
			}
			test.Tap(findButton(o, "Cancel"))
		})
	}
}

// A table that is not on the canvas has no columns to offer.
func TestATableOffTheCanvasOffersNoColumns(t *testing.T) {
	_, _, p := twoTables(t)
	for _, at := range []int{-1, 2, 99} {
		if got := p.columnOptions(at, true); got != nil {
			t.Errorf("table %d offers %v", at, got)
		}
	}
	if got := p.columnOptions(0, false); len(got) == 0 {
		t.Error("a table on the canvas offers no columns")
	}
}

// A name of its own is trimmed: a column called " spent " is a column nobody
// can address by the name they typed.
func TestANameOfItsOwnIsTrimmed(t *testing.T) {
	fx, _, p := twoTables(t)
	p.editOutput(-1)
	o := top(t, fx)
	picks := selectsIn(o)
	picks[0].SetSelected("orders")
	picks[1].SetSelected("id")
	entriesIn(o)[0].SetText("  spent  ")
	test.Tap(findButton(o, "Add"))
	if got := p.design.Outputs[0].Alias; got != "spent" {
		t.Errorf("it is called %q", got)
	}
}

// Changing any part keeps its place rather than adding another.
func TestChangingAnyPartKeepsItsPlace(t *testing.T) {
	fx, _, p := twoTables(t)
	id := query.Column{Table: 0, Name: "id"}
	item := query.Column{Table: 0, Name: "item"}
	p.design.Where = []query.Condition{{Column: id, Op: source.OpEqual, Value: "1"},
		{Column: item, Op: source.OpEqual, Value: "2"}}
	p.design.Group = []query.Column{id, item}
	p.design.Order = []query.Sort{{Column: id}, {Column: item}}
	p.draw()

	for name, c := range map[string]struct {
		open func()
		want func() int
	}{
		"a condition": {func() { p.editCondition(&p.design.Where, 0, false) },
			func() int { return len(p.design.Where) }},
		"a grouping":  {func() { p.editGroup(0) }, func() int { return len(p.design.Group) }},
		"an ordering": {func() { p.editOrder(0) }, func() int { return len(p.design.Order) }},
	} {
		t.Run(name, func(t *testing.T) {
			before := c.want()
			c.open()
			o := top(t, fx)
			selectsIn(o)[1].SetSelected("item")
			test.Tap(findButton(o, "Change"))
			if after := c.want(); after != before {
				t.Errorf("it went from %d to %d", before, after)
			}
		})
	}
	if p.design.Group[0].Name != "item" {
		t.Errorf("the grouping is %+v", p.design.Group)
	}
	if p.design.Order[0].Column.Name != "item" {
		t.Errorf("the ordering is %+v", p.design.Order)
	}
	if p.design.Where[0].Column.Name != "item" {
		t.Errorf("the condition is %+v", p.design.Where)
	}
}

// An ordering by an aggregate says so, which is how somebody sorting by a total
// sees that it is the total.
func TestAnOrderingByAnAggregateSaysSo(t *testing.T) {
	_, _, p := twoTables(t)
	p.design.Order = []query.Sort{{Column: query.Column{Table: 0, Name: "id"},
		Aggregate: query.AggregateSum, Descending: true}}
	p.draw()
	if got := p.orderLabel(0); got != "SUM(orders.id), largest first" {
		t.Errorf("the list says %q", got)
	}
}

// A condition between two bounds says both of them.
func TestABetweenSaysBothBounds(t *testing.T) {
	_, _, p := twoTables(t)
	p.design.Where = []query.Condition{{Column: query.Column{Table: 0, Name: "id"},
		Op: source.OpBetween, Value: "1", Value2: "9"}}
	p.draw()
	if got := conditionLabel(p, p.design.Where, 0); got != "orders.id is between 1 and 9" {
		t.Errorf("the list says %q", got)
	}
}

// A list that loses the item that was chosen has nothing chosen, and its
// buttons go off: acting on an item that has gone is acting on another one.
func TestALostItemIsNoLongerChosen(t *testing.T) {
	_, _, p := twoTables(t)
	id := query.Column{Table: 0, Name: "id"}
	p.design.Group = []query.Column{id, {Table: 0, Name: "item"}}
	p.draw()
	p.groups.list.Select(1)
	if p.groups.remove.Disabled() {
		t.Fatal("something is chosen and Remove is off")
	}
	test.Tap(p.groups.remove)
	if !p.groups.remove.Disabled() || p.groups.chosen != -1 {
		t.Errorf("after removing the last item, chosen is %d", p.groups.chosen)
	}
	// Unselecting it has the same effect, which is what a list does when
	// somebody clicks away.
	p.groups.list.Select(0)
	p.groups.list.UnselectAll()
	if !p.groups.remove.Disabled() || p.groups.chosen != -1 {
		t.Errorf("after unselecting, chosen is %d", p.groups.chosen)
	}
}

// Change and Remove do nothing with nothing chosen. The buttons are off, so a
// tap cannot reach them; the guard behind them is checked by calling what the
// button calls, which is the only way an accident could get in.
func TestChangeAndRemoveDoNothingWithNothingChosen(t *testing.T) {
	fx, _, p := twoTables(t)
	p.design.Group = []query.Column{{Table: 0, Name: "id"}}
	p.draw()
	if p.groups.chosen != -1 {
		t.Fatalf("something is chosen: %d", p.groups.chosen)
	}
	p.groups.change.OnTapped()
	if fx.s.win.Canvas().Overlays().Top() != nil {
		t.Error("it opened a dialog with nothing chosen")
	}
	p.groups.remove.OnTapped()
	if len(p.design.Group) != 1 {
		t.Errorf("it removed something: %+v", p.design.Group)
	}
}

// An empty designer says what to do, in the place the SQL goes as well as in
// the footer: an empty pane would read as a failure.
func TestAnEmptyDesignerSaysWhatToDoInTheSQL(t *testing.T) {
	_, _, p := designingQuery(t)
	if !strings.Contains(p.sql.Text, "Add a table") {
		t.Errorf("the SQL pane reads %q", p.sql.Text)
	}
}
