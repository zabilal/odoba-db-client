// Package query is a query somebody builds without writing it: the tables on
// a canvas, how they are joined, what comes out, and in what order (FR-9).
//
// It has no Fyne in it and no connection. A Design is data, and rendering one
// is a function of it and a dialect — which is what lets the whole of the
// SQL be tested on every engine without a server, and what keeps ARCH-2:
// statement text originates in a dialect and in code that goes through one,
// never in the window.
//
// One decision runs through all of it. A condition's right-hand side is text
// the person typed, in the database's own SQL, exactly as the grid's typed
// WHERE is (FR-3.6): a designer whose values were quoted strings could not
// express `CURRENT_DATE - 7`, and every engine here already has a way of
// saying what a person means by a condition — its own. So the rendered
// statement is classified before it is offered, and one that could change
// anything is refused rather than shown as a query.
package query

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// A Design is a query being built.
//
// Everything in it refers to a table by its place in Tables rather than by
// name, because two tables on one canvas may be the same table twice — a
// self-join is the commonest thing a designer is used for — and a name would
// not tell them apart.
type Design struct {
	// Distinct drops duplicate rows.
	Distinct bool

	// Tables are the tables on the canvas, in the order they were added.
	Tables []Table

	// Joins are how they are related. The first table needs none; every other
	// one needs a join reaching it, or the query is a cross product nobody
	// asked for, which Render refuses.
	Joins []Join

	// Outputs are what the query selects. None selects every column of every
	// table, which is what a canvas with nothing chosen on it means.
	Outputs []Output

	// Where are the conditions on the rows, joined by AND.
	Where []Condition

	// Group are the columns the rows are grouped by. Having are the
	// conditions on the groups.
	Group  []Column
	Having []Condition

	Order []Sort

	// Limit bounds the rows. Zero means the query says no limit, which is
	// what somebody writing SQL by hand would get; the editor bounds what it
	// reads either way (NFR-P11).
	Limit int64
}

// Table is one table on the canvas, and the name the query calls it by.
type Table struct {
	Ref model.ObjectRef

	// Alias is what the query refers to it as. Required: a designer writes
	// every column qualified, so that adding a second table never changes
	// what the first one's columns mean.
	Alias string
}

// JoinKind is how two tables are joined.
type JoinKind string

const (
	JoinInner JoinKind = "INNER JOIN"
	JoinLeft  JoinKind = "LEFT JOIN"
	JoinRight JoinKind = "RIGHT JOIN"
	JoinFull  JoinKind = "FULL JOIN"
	JoinCross JoinKind = "CROSS JOIN"
)

// joinKinds are the kinds a design may use, which is the whole set: a kind
// nothing recognises is refused rather than written into a statement.
var joinKinds = []JoinKind{JoinInner, JoinLeft, JoinRight, JoinFull, JoinCross}

// Join relates two tables on the canvas.
type Join struct {
	Kind JoinKind

	// Left and Right are places in Design.Tables. Right is the table being
	// brought in; Left is one already reached.
	Left, Right int

	// On are the column pairs the join matches, ANDed. A CROSS JOIN has none;
	// every other kind needs at least one.
	On []Pair

	// Inferred says this join came from a foreign key rather than from
	// somebody drawing it. It is kept so the designer can say which lines it
	// drew and which the person did: a join the catalogue implied is a
	// suggestion, and one somebody drew is a decision (FR-9.1).
	Inferred bool
}

// Pair is one pair of columns a join matches.
type Pair struct{ Left, Right string }

// Column addresses one column of one table on the canvas.
type Column struct {
	// Table is a place in Design.Tables.
	Table int
	Name  string
}

// Aggregate is a function over a group of rows.
type Aggregate string

const (
	AggregateNone  Aggregate = ""
	AggregateCount Aggregate = "COUNT"
	AggregateSum   Aggregate = "SUM"
	AggregateMin   Aggregate = "MIN"
	AggregateMax   Aggregate = "MAX"
	AggregateAvg   Aggregate = "AVG"
)

// aggregates are the ones a design may use. Every engine here has these five
// and spells them the same way; anything else is that engine's own and
// belongs in a query somebody writes.
var aggregates = []Aggregate{AggregateNone, AggregateCount, AggregateSum,
	AggregateMin, AggregateMax, AggregateAvg}

// Output is one thing the query selects.
type Output struct {
	// All selects every column of its table, as t.* — which is what a table
	// dragged onto the canvas with nothing chosen from it means.
	All bool

	// Column is the column, when All is false.
	Column Column

	// Aggregate wraps it, where there is one.
	Aggregate Aggregate

	// Alias names the result. Required on an aggregate of a column that is
	// also selected plainly, because two columns of one name is a result
	// nobody can address; the designer offers one and this does not invent it.
	Alias string
}

// Condition is one predicate on a column.
type Condition struct {
	Column Column
	Op     source.FilterOp

	// Aggregate wraps the column, which only a condition on a group can do:
	// HAVING SUM(total) > 100 is what HAVING is for, and an aggregate in a
	// WHERE is not legal SQL anywhere. A design that put one there is refused
	// here rather than at the server, which would answer about a statement the
	// person did not write.
	Aggregate Aggregate

	// Value is the right-hand side: what the person typed, in the database's
	// own SQL. A string needs its quotes, as it does in the grid's typed
	// WHERE, and an expression is allowed because that is what somebody
	// designing a query means half the time.
	//
	// Empty for the null tests, which have no right-hand side.
	Value string

	// Value2 is BETWEEN's upper bound.
	Value2 string
}

// Sort is one ordering term.
type Sort struct {
	Column    Column
	Aggregate Aggregate

	Descending bool
}

// conditionOps are the operators a design may use, and what each writes. The
// grid's own operators, so that a condition means the same thing in the
// designer as in the filter bar; a source's filter list is the same set.
var conditionOps = map[source.FilterOp]string{
	source.OpEqual: "=", source.OpNotEqual: "<>", source.OpLess: "<",
	source.OpLessEqual: "<=", source.OpGreater: ">", source.OpGreaterEqual: ">=",
	source.OpLike: "LIKE", source.OpNotLike: "NOT LIKE",
	source.OpIn: "IN", source.OpNotIn: "NOT IN",
	source.OpIsNull: "IS NULL", source.OpIsNotNull: "IS NOT NULL",
	source.OpBetween: "BETWEEN",
}

// Errors a design can have. Each is one a person can act on, and each is
// reported rather than rendered around: a designer that quietly dropped a
// table nothing joined would show SQL for a query nobody designed.
var (
	ErrNoTables         = errors.New("a query needs a table")
	ErrNoAlias          = errors.New("every table needs a name to be referred to by")
	ErrSameAlias        = errors.New("two tables cannot share a name")
	ErrUnreached        = errors.New("no join reaches this table")
	ErrJoinSelf         = errors.New("a join needs two different tables")
	ErrJoinEmpty        = errors.New("a join needs at least one pair of columns")
	ErrCrossOn          = errors.New("a cross join matches nothing, so it takes no columns")
	ErrUnknownJoin      = errors.New("that is not a kind of join")
	ErrUnknownOp        = errors.New("that is not a kind of condition")
	ErrUnknownAgg       = errors.New("that is not an aggregate")
	ErrNoValue          = errors.New("this condition needs a value")
	ErrValueGiven       = errors.New("this condition takes no value")
	ErrNoUpperBound     = errors.New("between needs both bounds")
	ErrNoColumn         = errors.New("this needs a column")
	ErrBadTable         = errors.New("that is not a table on the canvas")
	ErrMutating         = errors.New("this design would change the database rather than read it")
	ErrGroupNeeded      = errors.New("a query with an aggregate must group by every column that is not one")
	ErrAggregateInWhere = errors.New("a condition on rows cannot use an aggregate; a condition on groups can")
	ErrNegativeLimit    = errors.New("a limit cannot be negative")
)

// AliasFor is a name for a table that is not already taken.
//
// The table's own name where it is free, and the name with a number after it
// where it is not — which is what somebody joining a table to itself expects
// to see, and is short enough to read in a condition.
func AliasFor(taken []string, name string) string {
	if name == "" {
		name = "t"
	}
	if !slices.Contains(taken, name) {
		return name
	}
	for i := 2; ; i++ {
		try := name + strconv.Itoa(i)
		if !slices.Contains(taken, try) {
			return try
		}
	}
}

// Aliases are the names the tables are referred to by.
func (d *Design) Aliases() []string {
	out := make([]string, len(d.Tables))
	for i, t := range d.Tables {
		out[i] = t.Alias
	}
	return out
}

// Add puts a table on the canvas, named so as not to clash, and returns its
// place. It infers nothing: what a new table joins to is Infer's business,
// asked for separately so that adding a table and accepting what the
// catalogue suggests are two things a person can see happen.
func (d *Design) Add(ref model.ObjectRef) int {
	d.Tables = append(d.Tables, Table{Ref: ref, Alias: AliasFor(d.Aliases(), ref.Name())})
	return len(d.Tables) - 1
}

// Remove takes a table off the canvas, with everything that referred to it.
//
// Everything: a join reaching it, a column selected from it, a condition on
// one of its columns, a grouping and an ordering. A design that kept them
// would render a statement naming a table that is not in it.
func (d *Design) Remove(at int) {
	if at < 0 || at >= len(d.Tables) {
		return
	}
	d.Tables = slices.Delete(d.Tables, at, at+1)
	shift := func(i int) int {
		if i > at {
			return i - 1
		}
		return i
	}
	d.Joins = slices.DeleteFunc(d.Joins, func(j Join) bool { return j.Left == at || j.Right == at })
	for i := range d.Joins {
		d.Joins[i].Left, d.Joins[i].Right = shift(d.Joins[i].Left), shift(d.Joins[i].Right)
	}
	d.Outputs = slices.DeleteFunc(d.Outputs, func(o Output) bool { return o.Column.Table == at })
	for i := range d.Outputs {
		d.Outputs[i].Column.Table = shift(d.Outputs[i].Column.Table)
	}
	for _, conds := range []*[]Condition{&d.Where, &d.Having} {
		*conds = slices.DeleteFunc(*conds, func(c Condition) bool { return c.Column.Table == at })
		for i := range *conds {
			(*conds)[i].Column.Table = shift((*conds)[i].Column.Table)
		}
	}
	d.Group = slices.DeleteFunc(d.Group, func(c Column) bool { return c.Table == at })
	for i := range d.Group {
		d.Group[i].Table = shift(d.Group[i].Table)
	}
	d.Order = slices.DeleteFunc(d.Order, func(s Sort) bool { return s.Column.Table == at })
	for i := range d.Order {
		d.Order[i].Column.Table = shift(d.Order[i].Column.Table)
	}
}

// Validate says what is wrong with a design, or nothing. Render calls it, and
// the window calls it too so that it can say so before there is any SQL.
func (d *Design) Validate() error {
	if len(d.Tables) == 0 {
		return ErrNoTables
	}
	seen := map[string]bool{}
	for i, t := range d.Tables {
		if strings.TrimSpace(t.Alias) == "" {
			return fmt.Errorf("%s: %w", t.Ref.Name(), ErrNoAlias)
		}
		if seen[t.Alias] {
			return fmt.Errorf("%s: %w", t.Alias, ErrSameAlias)
		}
		seen[t.Alias] = true
		_ = i
	}
	if err := d.checkJoins(); err != nil {
		return err
	}
	if err := d.checkOutputs(); err != nil {
		return err
	}
	for _, c := range d.Where {
		if err := d.checkCondition(c, false); err != nil {
			return err
		}
	}
	for _, c := range d.Having {
		if err := d.checkCondition(c, true); err != nil {
			return err
		}
	}
	for _, c := range d.Group {
		if err := d.checkColumn(c); err != nil {
			return err
		}
	}
	for _, s := range d.Order {
		if !slices.Contains(aggregates, s.Aggregate) {
			return fmt.Errorf("%s: %w", s.Aggregate, ErrUnknownAgg)
		}
		if err := d.checkColumn(s.Column); err != nil {
			return err
		}
	}
	if d.Limit < 0 {
		return ErrNegativeLimit
	}
	return nil
}

// checkJoins holds every table to being reached: the first by being first,
// and every other by a join. A table nothing reaches would be a cross product
// with everything before it, which is a query nobody designs on purpose and
// which on two large tables is a server busy for a very long time.
func (d *Design) checkJoins() error {
	for _, j := range d.Joins {
		if !slices.Contains(joinKinds, j.Kind) {
			return fmt.Errorf("%s: %w", j.Kind, ErrUnknownJoin)
		}
		if j.Left < 0 || j.Left >= len(d.Tables) || j.Right < 0 || j.Right >= len(d.Tables) {
			return ErrBadTable
		}
		if j.Left == j.Right {
			return ErrJoinSelf
		}
		switch {
		case j.Kind == JoinCross && len(j.On) > 0:
			return ErrCrossOn
		case j.Kind != JoinCross && len(j.On) == 0:
			return fmt.Errorf("%s to %s: %w", d.Tables[j.Left].Alias, d.Tables[j.Right].Alias, ErrJoinEmpty)
		}
		for _, p := range j.On {
			if p.Left == "" || p.Right == "" {
				return fmt.Errorf("%s to %s: %w", d.Tables[j.Left].Alias, d.Tables[j.Right].Alias, ErrNoColumn)
			}
		}
	}
	for _, at := range d.unreached() {
		return fmt.Errorf("%s: %w", d.Tables[at].Alias, ErrUnreached)
	}
	return nil
}

// unreached are the tables no join reaches, in order. The first table is
// reached by being the one the FROM names.
func (d *Design) unreached() []int {
	reached := make([]bool, len(d.Tables))
	if len(reached) > 0 {
		reached[0] = true
	}
	// A join's own two ends may arrive in either order, and a join may reach
	// backwards, so this is repeated until nothing more is reached.
	for again := true; again; {
		again = false
		for _, j := range d.Joins {
			if j.Left < 0 || j.Left >= len(reached) || j.Right < 0 || j.Right >= len(reached) {
				continue
			}
			if reached[j.Left] && !reached[j.Right] {
				reached[j.Right], again = true, true
			}
			if reached[j.Right] && !reached[j.Left] {
				reached[j.Left], again = true, true
			}
		}
	}
	var out []int
	for i, ok := range reached {
		if !ok {
			out = append(out, i)
		}
	}
	return out
}

// checkOutputs holds the SELECT list to being something a server will take.
func (d *Design) checkOutputs() error {
	for _, o := range d.Outputs {
		if !slices.Contains(aggregates, o.Aggregate) {
			return fmt.Errorf("%s: %w", o.Aggregate, ErrUnknownAgg)
		}
		if o.All {
			if o.Column.Table < 0 || o.Column.Table >= len(d.Tables) {
				return ErrBadTable
			}
			continue
		}
		if err := d.checkColumn(o.Column); err != nil {
			return err
		}
	}
	return d.checkGrouping()
}

// checkGrouping holds the rule every SQL engine holds: a query with an
// aggregate in it groups by every output that is not one.
//
// It is checked here rather than left to the server because the server's
// answer is about a statement the person did not write, and because the
// designer can say which column is the problem.
func (d *Design) checkGrouping() error {
	aggregated := false
	for _, o := range d.Outputs {
		if o.Aggregate != AggregateNone {
			aggregated = true
		}
	}
	if !aggregated {
		return nil
	}
	grouped := make(map[Column]bool, len(d.Group))
	for _, c := range d.Group {
		grouped[c] = true
	}
	for _, o := range d.Outputs {
		if o.Aggregate != AggregateNone {
			continue
		}
		if o.All {
			return fmt.Errorf("%s.*: %w", d.Tables[o.Column.Table].Alias, ErrGroupNeeded)
		}
		if !grouped[o.Column] {
			return fmt.Errorf("%s.%s: %w", d.Tables[o.Column.Table].Alias, o.Column.Name, ErrGroupNeeded)
		}
	}
	return nil
}

// checkCondition holds one predicate to having what it needs and nothing it
// does not. grouped says it is a condition on a group rather than on a row,
// which is the only place an aggregate belongs.
func (d *Design) checkCondition(c Condition, grouped bool) error {
	if _, ok := conditionOps[c.Op]; !ok {
		return fmt.Errorf("%s: %w", c.Op, ErrUnknownOp)
	}
	if !slices.Contains(aggregates, c.Aggregate) {
		return fmt.Errorf("%s: %w", c.Aggregate, ErrUnknownAgg)
	}
	if err := d.checkColumn(c.Column); err != nil {
		return err
	}
	where := d.Tables[c.Column.Table].Alias + "." + c.Column.Name
	if c.Aggregate != AggregateNone && !grouped {
		return fmt.Errorf("%s(%s): %w", c.Aggregate, where, ErrAggregateInWhere)
	}
	switch c.Op {
	case source.OpIsNull, source.OpIsNotNull:
		if strings.TrimSpace(c.Value) != "" || strings.TrimSpace(c.Value2) != "" {
			return fmt.Errorf("%s: %w", where, ErrValueGiven)
		}
	case source.OpBetween:
		if strings.TrimSpace(c.Value) == "" || strings.TrimSpace(c.Value2) == "" {
			return fmt.Errorf("%s: %w", where, ErrNoUpperBound)
		}
	default:
		if strings.TrimSpace(c.Value) == "" {
			return fmt.Errorf("%s: %w", where, ErrNoValue)
		}
		if strings.TrimSpace(c.Value2) != "" {
			return fmt.Errorf("%s: %w", where, ErrValueGiven)
		}
	}
	return nil
}

func (d *Design) checkColumn(c Column) error {
	if c.Table < 0 || c.Table >= len(d.Tables) {
		return ErrBadTable
	}
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("%s: %w", d.Tables[c.Table].Alias, ErrNoColumn)
	}
	return nil
}
