// Package diff compares two schemas and says what differs (FR-7.1, FR-7.2).
//
// Nothing here reads a server or writes a statement. It takes two models and
// answers a tree of differences, so the comparison can be exercised in full
// without either database being reachable — which is what RISK-8 asks for,
// since this is the code a sync script is generated from and a sync script
// that is wrong destroys data.
//
// Two rules govern the whole package.
//
// Everything is matched by name. Two snapshots cannot say which column
// became which: a rename and a drop-with-an-add look identical, and only
// something that watched the change happen can tell them apart. A designer
// can, because it kept each column's origin (ADR-0114); a comparison of two
// databases cannot, and guessing would be the one mistake that loses data.
// So a rename reads here as a removal and an addition, which is the truth
// about what this can see.
//
// What is unknown is not a difference. A model read from a file has no row
// counts; one read from a server may not either. Comparing a statistic
// against an absence would report a change in every table, and a report that
// is noisy everywhere is read nowhere.
package diff

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Status is what happened to one thing between the two models.
type Status string

const (
	Same    Status = "same"
	Added   Status = "added"
	Removed Status = "removed"
	Changed Status = "changed"
)

// Node is one thing compared, and the things inside it.
//
// Everything compared appears, including what is identical, because FR-7.2
// asks for a tree that can show identical objects and a tree holding only
// differences could not be filtered into one.
type Node struct {
	Kind   model.ObjectKind
	Name   string
	Status Status

	// Detail is what differs, on a node that changed in itself rather than
	// through its children. Both values are carried so that whatever draws
	// this does not have to look them up again.
	Detail []Field

	Children []Node
}

// Field is one property that differs, with both of its values.
type Field struct {
	Name string
	From string
	To   string
}

// Counts is how many of each status are in a tree, including the root.
type Counts map[Status]int

// Count walks the tree and tallies it, for the line that says how much there
// is before somebody reads the rest.
func (n Node) Count() Counts {
	c := Counts{}
	var walk func(Node)
	walk = func(n Node) {
		c[n.Status]++
		for _, k := range n.Children {
			walk(k)
		}
	}
	walk(n)
	return c
}

// Differs reports whether anything at all differs.
func (n Node) Differs() bool { return n.Status != Same }

// Compare answers what differs between two databases.
//
// from is what is there and to is what is wanted, so Added means "in to and
// not in from" — the thing a sync script would create.
func Compare(from, to *model.Database) Node { return CompareWith(from, to, Options{}) }

// CompareWith is Compare with some differences left out (FR-7.5).
//
// The rules are applied while the comparison is made, so what is left out is
// not a difference at all: it does not make its table read as changed, it is
// not counted, and it cannot be chosen for a sync script.
func CompareWith(from, to *model.Database, opt Options) Node {
	if from == nil {
		from = &model.Database{}
	}
	if to == nil {
		to = &model.Database{}
	}
	c := comparer{opt: opt}
	n := Node{Kind: model.KindDatabase, Name: pick(to.Name, from.Name)}
	n.Detail = fields(
		field("charset", c.collation(from.Charset), c.collation(to.Charset)),
		field("collation", c.collation(from.Collate), c.collation(to.Collate)),
		field("comment", opt.comment(from.Comment), opt.comment(to.Comment)),
	)
	n.Children = match(from.Schemas, to.Schemas, fixed[model.Schema](model.KindSchema),
		func(s model.Schema) string { return s.Name }, c.compareSchema,
		func(name string) bool { return opt.skipSchema(name) })
	return settle(n)
}

// comparer carries what a comparison was told to leave out, so that every
// comparison below it is made the same way.
type comparer struct{ opt Options }

// collation is a charset or a collation, or nothing where they are left out.
func (c comparer) collation(s string) string {
	if c.opt.Collation {
		return ""
	}
	return s
}

// skipObject is the rule a name pattern makes, for the lists of objects
// inside a schema.
func (c comparer) skipObject(name string) bool { return c.opt.skipName(name) }

func (c comparer) compareSchema(from, to model.Schema) Node {
	n := Node{Kind: model.KindSchema, Name: to.Name}
	n.Detail = fields(
		field("owner", from.Owner, to.Owner),
		field("comment", c.opt.comment(from.Comment), c.opt.comment(to.Comment)),
	)
	n.Detail = append(n.Detail, attrFields(c.opt.attrs(from.Attrs), c.opt.attrs(to.Attrs))...)
	n.Children = slices.Concat(
		match(from.Tables, to.Tables, fixed[model.Table](model.KindTable), func(t model.Table) string { return t.Name }, c.compareTable, c.skipObject),
		match(from.Views, to.Views, viewKind, func(v model.View) string { return v.Name }, c.compareView, c.skipObject),
		match(from.Routines, to.Routines, fixed[model.Routine](model.KindRoutine), routineName, c.compareRoutine, c.skipObject),
		match(from.Sequences, to.Sequences, fixed[model.Sequence](model.KindSequence), func(s model.Sequence) string { return s.Name }, c.compareSequence, c.skipObject),
		match(from.UserTypes, to.UserTypes, fixed[model.UserType](model.KindUserType), func(t model.UserType) string { return t.Name }, c.compareUserType, c.skipObject),
	)
	return settle(n)
}

// routineName is a routine's name with its parameters, because two routines
// of the same name and different arguments are two routines.
func routineName(r model.Routine) string {
	parts := make([]string, len(r.Parameters))
	for i, p := range r.Parameters {
		parts[i] = strings.TrimSpace(p.Mode + " " + p.Type.Native)
	}
	return r.Name + "(" + strings.Join(parts, ", ") + ")"
}

func (c comparer) compareTable(from, to model.Table) Node {
	n := Node{Kind: model.KindTable, Name: to.Name}
	// RowsEstimate is deliberately absent: it is a statistic, it is -1 when
	// unknown, and a model read from a file never has one.
	n.Detail = fields(field("comment", c.opt.comment(from.Comment), c.opt.comment(to.Comment)))
	n.Detail = append(n.Detail, attrFields(c.opt.attrs(from.Attrs), c.opt.attrs(to.Attrs))...)
	n.Children = slices.Concat(
		match(from.Columns, to.Columns, fixed[model.Column](model.KindColumn), func(c model.Column) string { return c.Name }, c.compareColumn),
		matchOne(from.PrimaryKey, to.PrimaryKey, model.KindPrimaryKey, keyName, c.comparePrimaryKey),
		match(from.Uniques, to.Uniques, fixed[model.UniqueConstraint](model.KindUnique), func(u model.UniqueConstraint) string { return u.Name }, c.compareUnique),
		match(from.ForeignKeys, to.ForeignKeys, fixed[model.ForeignKey](model.KindForeignKey), func(f model.ForeignKey) string { return f.Name }, c.compareForeignKey),
		match(from.Checks, to.Checks, fixed[model.CheckConstraint](model.KindCheck), func(c model.CheckConstraint) string { return c.Name }, c.compareCheck),
		match(from.Indexes, to.Indexes, fixed[model.Index](model.KindIndex), func(i model.Index) string { return i.Name }, c.compareIndex),
		match(from.Triggers, to.Triggers, fixed[model.Trigger](model.KindTrigger), func(t model.Trigger) string { return t.Name }, c.compareTrigger),
	)
	return settle(n)
}

// compareColumn compares a column.
//
// The type is compared as the engine's own word for it and nothing else.
// Length, precision, scale and time-zone-ness are the driver's reading of
// that same word — varchar(40) is where the 40 came from — so comparing them
// as well would report one difference twice, and where they disagreed with
// the word it would be a defect in the driver rather than a difference
// between two schemas.
func (c comparer) compareColumn(from, to model.Column) Node {
	n := Node{Kind: model.KindColumn, Name: to.Name}
	n.Detail = fields(
		field("type", from.Type.Native, to.Type.Native),
		field("nullable", yes(from.Type.Nullable), yes(to.Type.Nullable)),
		field("position", fmt.Sprint(from.Position), fmt.Sprint(to.Position)),
		field("default", defaultOf(from), defaultOf(to)),
		field("identity", yes(from.Identity), yes(to.Identity)),
		field("auto-increment", yes(from.AutoIncrement), yes(to.AutoIncrement)),
		field("generated", c.opt.text(from.Generated), c.opt.text(to.Generated)),
		field("comment", c.opt.comment(from.Comment), c.opt.comment(to.Comment)),
	)
	n.Detail = append(n.Detail, attrFields(c.opt.attrs(from.Attrs), c.opt.attrs(to.Attrs))...)
	return settle(n)
}

// defaultOf tells a column with no default from one whose default is the
// empty string, which are different things and would otherwise read alike.
func defaultOf(c model.Column) string {
	if !c.HasDefault {
		return ""
	}
	return "= " + c.Default
}

func keyName(k *model.PrimaryKey) string { return k.Name }

func (c comparer) comparePrimaryKey(from, to *model.PrimaryKey) Node {
	n := Node{Kind: model.KindPrimaryKey, Name: to.Name}
	n.Detail = fields(field("columns", list(from.Columns), list(to.Columns)))
	return settle(n)
}

func (c comparer) compareUnique(from, to model.UniqueConstraint) Node {
	n := Node{Kind: model.KindUnique, Name: to.Name}
	n.Detail = fields(field("columns", list(from.Columns), list(to.Columns)))
	return settle(n)
}

func (c comparer) compareCheck(from, to model.CheckConstraint) Node {
	n := Node{Kind: model.KindCheck, Name: to.Name}
	n.Detail = fields(field("expression", c.opt.text(from.Expression), c.opt.text(to.Expression)))
	return settle(n)
}

func (c comparer) compareForeignKey(from, to model.ForeignKey) Node {
	n := Node{Kind: model.KindForeignKey, Name: to.Name}
	n.Detail = fields(
		field("columns", list(from.Columns), list(to.Columns)),
		field("references", refOf(from), refOf(to)),
		field("on delete", string(from.OnDelete), string(to.OnDelete)),
		field("on update", string(from.OnUpdate), string(to.OnUpdate)),
	)
	return settle(n)
}

func refOf(f model.ForeignKey) string {
	name := f.RefTable
	if f.RefSchema != "" {
		name = f.RefSchema + "." + name
	}
	return name + " (" + list(f.RefColumns) + ")"
}

func (c comparer) compareIndex(from, to model.Index) Node {
	n := Node{Kind: model.KindIndex, Name: to.Name}
	n.Detail = fields(
		field("columns", indexColumns(from.Columns), indexColumns(to.Columns)),
		field("unique", yes(from.Unique), yes(to.Unique)),
		field("method", from.Method, to.Method),
		field("predicate", c.opt.text(from.Predicate), c.opt.text(to.Predicate)),
		field("include", list(from.Include), list(to.Include)),
	)
	n.Detail = append(n.Detail, attrFields(c.opt.attrs(from.Attrs), c.opt.attrs(to.Attrs))...)
	return settle(n)
}

// indexColumns keeps the direction with the column, because an index turned
// round answers a different question and is a different index.
func indexColumns(cols []model.IndexColumn) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		name := c.Name
		if c.Expression != "" {
			name = "(" + c.Expression + ")"
		}
		if c.Descending {
			name += " DESC"
		}
		parts[i] = name
	}
	return strings.Join(parts, ", ")
}

func (c comparer) compareView(from, to model.View) Node {
	n := Node{Kind: viewKind(to), Name: to.Name}
	n.Detail = fields(
		field("materialized", yes(from.Materialized), yes(to.Materialized)),
		// Text, compared as text, unless a rule says to read its
		// whitespace as one space: two servers print the same view
		// differently, and FR-7.5's rules are where that is answered.
		field("definition", c.opt.text(from.Definition), c.opt.text(to.Definition)),
		field("comment", c.opt.comment(from.Comment), c.opt.comment(to.Comment)),
	)
	n.Children = slices.Concat(
		match(from.Columns, to.Columns, fixed[model.Column](model.KindColumn), func(c model.Column) string { return c.Name }, c.compareColumn),
		match(from.Indexes, to.Indexes, fixed[model.Index](model.KindIndex), func(i model.Index) string { return i.Name }, c.compareIndex),
	)
	return settle(n)
}

// viewKind is what a view is, which for a node in a comparison is what the
// target says it is.
//
// A plain view on one side and a materialized one on the other therefore
// reads as a materialized view that differs, not as a view that does. That
// is deliberate and it is the same rule the root's name follows: a
// comparison describes what the object is wanted as, and a sync script has
// to create that. It does mean a node's kind, and so its name in a
// selection, depends on which way round the two models were given — which is
// harmless, because a script is written from the comparison it was chosen
// in and no comparison is turned round while it is being read.
func viewKind(v model.View) model.ObjectKind {
	if v.Materialized {
		return model.KindMaterializedView
	}
	return model.KindView
}

func (c comparer) compareRoutine(from, to model.Routine) Node {
	n := Node{Kind: model.KindRoutine, Name: routineName(to)}
	n.Detail = fields(
		field("kind", string(from.Kind), string(to.Kind)),
		field("language", from.Language, to.Language),
		field("returns", returnOf(from), returnOf(to)),
		field("definition", c.opt.text(from.Definition), c.opt.text(to.Definition)),
		field("comment", c.opt.comment(from.Comment), c.opt.comment(to.Comment)),
	)
	return settle(n)
}

func returnOf(r model.Routine) string {
	if r.Returns == nil {
		return ""
	}
	return r.Returns.Native
}

func (c comparer) compareSequence(from, to model.Sequence) Node {
	n := Node{Kind: model.KindSequence, Name: to.Name}
	n.Detail = fields(
		field("type", from.DataType, to.DataType),
		field("start", fmt.Sprint(from.Start), fmt.Sprint(to.Start)),
		field("increment", fmt.Sprint(from.Increment), fmt.Sprint(to.Increment)),
		field("minimum", bound(from.MinValue), bound(to.MinValue)),
		field("maximum", bound(from.MaxValue), bound(to.MaxValue)),
		field("cycle", yes(from.Cycle), yes(to.Cycle)),
		field("comment", c.opt.comment(from.Comment), c.opt.comment(to.Comment)),
	)
	return settle(n)
}

// bound tells a sequence with no bound from one bounded at zero.
func bound(v *int64) string {
	if v == nil {
		return "none"
	}
	return fmt.Sprint(*v)
}

func (c comparer) compareTrigger(from, to model.Trigger) Node {
	n := Node{Kind: model.KindTrigger, Name: to.Name}
	n.Detail = fields(
		field("timing", from.Timing, to.Timing),
		field("events", list(from.Events), list(to.Events)),
		field("for each row", yes(from.ForEachRow), yes(to.ForEachRow)),
		field("condition", c.opt.text(from.Condition), c.opt.text(to.Condition)),
		field("definition", c.opt.text(from.Definition), c.opt.text(to.Definition)),
	)
	return settle(n)
}

func (c comparer) compareUserType(from, to model.UserType) Node {
	n := Node{Kind: model.KindUserType, Name: to.Name}
	n.Detail = fields(
		field("category", from.Category, to.Category),
		// An enum's values are ordered and the order is part of the type.
		field("values", list(from.EnumValues), list(to.EnumValues)),
		field("fields", fieldDefs(from.Fields), fieldDefs(to.Fields)),
		field("base type", from.BaseType, to.BaseType),
		field("comment", c.opt.comment(from.Comment), c.opt.comment(to.Comment)),
	)
	return settle(n)
}

func fieldDefs(fs []model.FieldDef) string {
	parts := make([]string, len(fs))
	for i, f := range fs {
		parts[i] = f.Name + " " + f.Type.Native
	}
	return strings.Join(parts, ", ")
}

// Naming a node, so that a selection means the same thing to whatever draws
// the tree and whatever writes a script from it (FR-7.3).
//
// A comparison has no identifiers of its own: it is two models and neither
// of them has one either. What a node has is its place — the kinds and names
// down to it — and two things in the same place are the same thing. The kind
// is in the path because a view and a table can share a name.

// ID is what a node is known by under its parent.
func ID(parent string, n Node) string {
	return parent + "/" + string(n.Kind) + ":" + n.Name
}

// Walk visits every node with the id it is known by, parents before
// children, so that whatever collects them sees a node before anything
// under it.
func (n Node) Walk(fn func(id string, n Node)) {
	var visit func(Node, string)
	visit = func(n Node, parent string) {
		id := ID(parent, n)
		fn(id, n)
		for _, c := range n.Children {
			visit(c, id)
		}
	}
	visit(n, "")
}

// Find answers the node an id names, and whether there is one.
func (n Node) Find(id string) (Node, bool) {
	var found Node
	ok := false
	n.Walk(func(at string, node Node) {
		if at == id {
			found, ok = node, true
		}
	})
	return found, ok
}
