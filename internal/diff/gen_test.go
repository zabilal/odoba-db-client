package diff

import (
	"fmt"
	"slices"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Building a model out of a stream of bytes, so that a property can be put
// to thousands of schemas nobody would sit down and write (RISK-8).
//
// It is deterministic: the same bytes make the same model, which is what
// lets a fuzzing failure be kept as a seed and run again. It runs off the
// end rather than round it, so a short stream makes a small model and a long
// one a larger — and every model it makes is one this program could really
// have read, because a comparison of nonsense proves nothing about a
// comparison of schemas.

// gen hands out numbers, names and flags from a stream of bytes.
type gen struct {
	b []byte
	i int
}

// byteOf is the next byte, or zero past the end. Running out makes the rest
// of a model empty rather than making the generator wrap, which would put
// the same values round again and quietly narrow what is generated.
func (g *gen) byteOf() byte {
	if g.i >= len(g.b) {
		return 0
	}
	v := g.b[g.i]
	g.i++
	return v
}

// upTo is a number from 0 to n-1.
func (g *gen) upTo(n int) int {
	if n <= 0 {
		return 0
	}
	return int(g.byteOf()) % n
}

func (g *gen) flag() bool { return g.byteOf()%2 == 1 }

// names are drawn from a small pool so that two models collide often: a
// comparison of two models with nothing in common exercises only its
// outermost branch.
var pool = []string{
	"id", "name", "note", "total", "people", "orders", "audit", "recent",
	"a", "B", "a b", "a.b", "Ünïcødé", "", "con",
}

func (g *gen) name() string { return pool[g.upTo(len(pool))] }

// typeName is the engine's own word for a type, which is what a comparison
// reads.
var typeNames = []string{"integer", "text", "varchar(40)", "numeric(10,2)", "timestamptz", ""}

func (g *gen) typeOf() model.DataType {
	return model.DataType{
		Class:    model.TypeString,
		Native:   typeNames[g.upTo(len(typeNames))],
		Nullable: g.flag(),
		Length:   -1,
	}
}

// unique keeps the first of anything sharing a name.
//
// A real catalogue cannot hold two tables of one name in a schema, and this
// only generates models a server could have answered: a comparison of
// something impossible proves nothing, and a property that had to allow for
// it would be weaker everywhere else. What a duplicate does is settled by a
// test of its own, written by hand.
func unique[T any](list []T, name func(T) string) []T {
	seen := map[string]bool{}
	out := list[:0]
	for _, v := range list {
		if n := name(v); !seen[n] {
			seen[n] = true
			out = append(out, v)
		}
	}
	return out
}

// database builds a whole model.
func (g *gen) database() *model.Database {
	db := &model.Database{
		Name:    g.name(),
		Charset: g.name(),
		Collate: g.name(),
		Comment: g.name(),
	}
	for n := g.upTo(4); n > 0; n-- {
		db.Schemas = append(db.Schemas, g.schema())
	}
	db.Schemas = unique(db.Schemas, func(s model.Schema) string { return s.Name })
	return db
}

func (g *gen) schema() model.Schema {
	s := model.Schema{Name: g.name(), Owner: g.name(), Comment: g.name(), Attrs: g.attrs()}
	for n := g.upTo(4); n > 0; n-- {
		s.Tables = append(s.Tables, g.table())
	}
	for n := g.upTo(3); n > 0; n-- {
		s.Views = append(s.Views, g.view())
	}
	for n := g.upTo(3); n > 0; n-- {
		s.Routines = append(s.Routines, g.routine())
	}
	for n := g.upTo(3); n > 0; n-- {
		s.Sequences = append(s.Sequences, g.sequence())
	}
	for n := g.upTo(2); n > 0; n-- {
		s.UserTypes = append(s.UserTypes, g.userType())
	}
	s.Tables = unique(s.Tables, func(t model.Table) string { return t.Name })
	s.Views = unique(s.Views, func(v model.View) string { return v.Name })
	s.Routines = unique(s.Routines, routineName)
	s.Sequences = unique(s.Sequences, func(q model.Sequence) string { return q.Name })
	s.UserTypes = unique(s.UserTypes, func(u model.UserType) string { return u.Name })
	return s
}

func (g *gen) table() model.Table {
	t := model.Table{Name: g.name(), Comment: g.name(), RowsEstimate: -1, Attrs: g.attrs()}
	for n := g.upTo(5); n > 0; n-- {
		t.Columns = append(t.Columns, g.column())
	}
	if g.flag() {
		t.PrimaryKey = &model.PrimaryKey{Name: g.name(), Columns: g.names()}
	}
	for n := g.upTo(2); n > 0; n-- {
		t.Uniques = append(t.Uniques, model.UniqueConstraint{Name: g.name(), Columns: g.names()})
	}
	for n := g.upTo(2); n > 0; n-- {
		t.Checks = append(t.Checks, model.CheckConstraint{Name: g.name(), Expression: g.text()})
	}
	for n := g.upTo(2); n > 0; n-- {
		t.ForeignKeys = append(t.ForeignKeys, model.ForeignKey{
			Name: g.name(), Columns: g.names(), RefSchema: g.name(), RefTable: g.name(),
			RefColumns: g.names(), OnDelete: g.action(), OnUpdate: g.action()})
	}
	for n := g.upTo(2); n > 0; n-- {
		t.Indexes = append(t.Indexes, g.index())
	}
	for n := g.upTo(2); n > 0; n-- {
		t.Triggers = append(t.Triggers, g.trigger())
	}
	t.Columns = unique(t.Columns, func(c model.Column) string { return c.Name })
	t.Indexes = unique(t.Indexes, func(i model.Index) string { return i.Name })
	t.Triggers = unique(t.Triggers, func(g model.Trigger) string { return g.Name })
	// The three kinds of constraint share one namespace, so a name used by
	// one is not free for another.
	named := map[string]bool{}
	if t.PrimaryKey != nil {
		named[t.PrimaryKey.Name] = true
	}
	t.Uniques = unique(t.Uniques, func(u model.UniqueConstraint) string { return u.Name })
	t.Uniques = slices.DeleteFunc(t.Uniques, func(u model.UniqueConstraint) bool {
		held := named[u.Name]
		named[u.Name] = true
		return held
	})
	t.Checks = unique(t.Checks, func(c model.CheckConstraint) string { return c.Name })
	t.Checks = slices.DeleteFunc(t.Checks, func(c model.CheckConstraint) bool {
		held := named[c.Name]
		named[c.Name] = true
		return held
	})
	return t
}

func (g *gen) column() model.Column {
	c := model.Column{Name: g.name(), Type: g.typeOf(), Position: g.upTo(8),
		Comment: g.name(), Attrs: g.attrs()}
	if g.flag() {
		c.Default, c.HasDefault = g.text(), true
	}
	c.Identity, c.AutoIncrement = g.flag(), g.flag()
	if g.flag() {
		c.Generated = g.text()
	}
	return c
}

func (g *gen) index() model.Index {
	ix := model.Index{Name: g.name(), Unique: g.flag(), Method: g.name(),
		Predicate: g.text(), Include: g.names(), Attrs: g.attrs()}
	for n := g.upTo(3); n > 0; n-- {
		col := model.IndexColumn{Descending: g.flag()}
		if g.flag() {
			col.Expression = g.text()
		} else {
			col.Name = g.name()
		}
		ix.Columns = append(ix.Columns, col)
	}
	return ix
}

func (g *gen) view() model.View {
	v := model.View{Name: g.name(), Materialized: g.flag(), Definition: g.text(), Comment: g.name()}
	for n := g.upTo(3); n > 0; n-- {
		v.Columns = append(v.Columns, g.column())
	}
	for n := g.upTo(2); n > 0; n-- {
		v.Indexes = append(v.Indexes, g.index())
	}
	v.Columns = unique(v.Columns, func(c model.Column) string { return c.Name })
	v.Indexes = unique(v.Indexes, func(i model.Index) string { return i.Name })
	return v
}

func (g *gen) routine() model.Routine {
	r := model.Routine{Name: g.name(), Language: g.name(), Definition: g.text(), Comment: g.name()}
	r.Kind = model.RoutineFunction
	if g.flag() {
		r.Kind = model.RoutineProcedure
	}
	for n := g.upTo(3); n > 0; n-- {
		r.Parameters = append(r.Parameters, model.Parameter{
			Name: g.name(), Mode: []string{"IN", "OUT", "INOUT", ""}[g.upTo(4)], Type: g.typeOf()})
	}
	if g.flag() {
		t := g.typeOf()
		r.Returns = &t
	}
	return r
}

func (g *gen) sequence() model.Sequence {
	q := model.Sequence{Name: g.name(), DataType: g.name(),
		Start: int64(g.upTo(100)), Increment: int64(g.upTo(10)), Cycle: g.flag(), Comment: g.name()}
	if g.flag() {
		v := int64(g.upTo(50))
		q.MinValue = &v
	}
	if g.flag() {
		v := int64(g.upTo(50))
		q.MaxValue = &v
	}
	return q
}

func (g *gen) trigger() model.Trigger {
	t := model.Trigger{Name: g.name(), Definition: g.text(), Condition: g.text(),
		ForEachRow: g.flag(), Timing: []string{"BEFORE", "AFTER", "INSTEAD OF"}[g.upTo(3)]}
	for n := g.upTo(3); n > 0; n-- {
		t.Events = append(t.Events, []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE"}[g.upTo(4)])
	}
	return t
}

func (g *gen) userType() model.UserType {
	u := model.UserType{Name: g.name(), Comment: g.name(),
		Category: []string{"enum", "domain", "range", "composite"}[g.upTo(4)],
		BaseType: g.name()}
	for n := g.upTo(3); n > 0; n-- {
		u.EnumValues = append(u.EnumValues, g.name())
	}
	for n := g.upTo(2); n > 0; n-- {
		u.Fields = append(u.Fields, model.FieldDef{Name: g.name(), Type: g.typeOf()})
	}
	return u
}

func (g *gen) names() []string {
	var out []string
	for n := g.upTo(3); n > 0; n-- {
		out = append(out, g.name())
	}
	return out
}

// text is the engine's own language, with the whitespace a rule may be told
// to read as one space.
var texts = []string{"id > 0", "id  >  0", "SELECT 1", "SELECT\n  1", "", "lower(name)"}

func (g *gen) text() string { return texts[g.upTo(len(texts))] }

func (g *gen) action() model.ReferentialAction {
	return []model.ReferentialAction{model.ActionNoAction, model.ActionCascade,
		model.ActionRestrict, model.ActionSetNull, model.ActionSetDefault}[g.upTo(5)]
}

func (g *gen) attrs() map[string]string {
	n := g.upTo(3)
	if n == 0 {
		return nil
	}
	out := make(map[string]string, n)
	for ; n > 0; n-- {
		out[[]string{"fillfactor", "collation", "charset", "storage", "encoding"}[g.upTo(5)]] = g.name()
	}
	return out
}

// options are ignore rules built from the same stream.
func (g *gen) options() Options {
	o := Options{Whitespace: g.flag(), Collation: g.flag(), Comments: g.flag()}
	for n := g.upTo(3); n > 0; n-- {
		o.Schemas = append(o.Schemas, g.name())
	}
	for n := g.upTo(3); n > 0; n-- {
		o.Names = append(o.Names, []string{"*", "a*", "*e", "?", "orders", "[a-z]*"}[g.upTo(6)])
	}
	return o
}

// twoOf builds two models from one stream, each from half of it, so that
// they are related often enough to compare interestingly and different
// often enough to be worth comparing.
func twoOf(seed []byte) (*model.Database, *model.Database) {
	half := len(seed) / 2
	a := (&gen{b: seed[:half]}).database()
	b := (&gen{b: seed[half:]}).database()
	return a, b
}

// describeNode names a node for a failure message.
func describeNode(id string, n Node) string {
	return fmt.Sprintf("%s (%s %q, %s)", id, n.Kind, n.Name, n.Status)
}
