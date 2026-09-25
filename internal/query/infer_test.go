package query

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The joins the catalogue implies (FR-9.1), and the ones it does not.

func TestAForeignKeyIsAJoin(t *testing.T) {
	d := &Design{}
	d.Add(ref("public", "people"))
	d.Add(ref("public", "orders"))
	got := Suggest(d, schema())
	if len(got) != 1 {
		t.Fatalf("it suggested %+v", got)
	}
	s := got[0]
	if s.Key != "orders_person" {
		t.Errorf("it came from %q", s.Key)
	}
	// Left is the table holding the key, whichever order the tables arrived
	// in; the join is written from the reached table either way (render.go).
	if d.Tables[s.Join.Left].Ref.Name() != "orders" || d.Tables[s.Join.Right].Ref.Name() != "people" {
		t.Errorf("it joins %s to %s", d.Tables[s.Join.Left].Alias, d.Tables[s.Join.Right].Alias)
	}
	if len(s.Join.On) != 1 || s.Join.On[0].Left != "person_id" || s.Join.On[0].Right != "id" {
		t.Errorf("it matches %+v", s.Join.On)
	}
	// INNER, because that is what a foreign key means. Changing it to LEFT is
	// one of the commonest things somebody will do, and is theirs to do.
	if s.Join.Kind != JoinInner {
		t.Errorf("it suggests a %s", s.Join.Kind)
	}
	if !s.Join.Inferred {
		t.Error("it does not say it was inferred, so nothing can tell it from a join somebody drew")
	}
}

// Whichever order the tables were dragged on: which one was first says nothing
// about which holds the key.
func TestTheOrderTheTablesArrivedInDoesNotMatter(t *testing.T) {
	for name, order := range map[string][]string{
		"the key's table second": {"people", "orders"},
		"the key's table first":  {"orders", "people"},
	} {
		t.Run(name, func(t *testing.T) {
			d := &Design{}
			for _, n := range order {
				d.Add(ref("public", n))
			}
			if got := Suggest(d, schema()); len(got) != 1 {
				t.Errorf("it suggested %+v", got)
			}
		})
	}
}

// Nothing is guessed. Two columns of the same name in two tables are two
// columns, and joining on them would be this program's opinion presented as
// the schema's.
func TestNothingIsGuessed(t *testing.T) {
	d := &Design{}
	d.Add(ref("public", "people"))
	d.Add(ref("public", "notes"))
	// Both have an "id" and nothing declares a relationship between them.
	if got := Suggest(d, schema()); len(got) != 0 {
		t.Errorf("it invented %+v", got)
	}
}

// A pair already joined is left alone, however it came to be joined: a
// suggestion is for a gap.
func TestAPairAlreadyJoinedIsLeftAlone(t *testing.T) {
	d := &Design{}
	people := d.Add(ref("public", "people"))
	orders := d.Add(ref("public", "orders"))
	d.Joins = []Join{{Kind: JoinLeft, Left: people, Right: orders,
		On: []Pair{{Left: "id", Right: "person_id"}}}}
	if got := Suggest(d, schema()); len(got) != 0 {
		t.Errorf("it suggested %+v over a join somebody has", got)
	}
	// And the direction the existing join was drawn in makes no difference.
	d.Joins[0].Left, d.Joins[0].Right = orders, people
	if got := Suggest(d, schema()); len(got) != 0 {
		t.Errorf("it suggested %+v over the same join drawn backwards", got)
	}
}

// A pair with two foreign keys between them is a choice somebody has to make,
// and making it here would be making it silently.
func TestOnlyOneJoinIsSuggestedPerPair(t *testing.T) {
	db := schema()
	orders := &db.Schemas[0].Tables[1]
	orders.ForeignKeys = append(orders.ForeignKeys, model.ForeignKey{
		Name: "orders_approver", Columns: []string{"id"},
		RefSchema: "public", RefTable: "people", RefColumns: []string{"id"}})
	d := &Design{}
	d.Add(ref("public", "people"))
	d.Add(ref("public", "orders"))
	if got := Suggest(d, db); len(got) != 1 {
		t.Errorf("it suggested %+v", got)
	}
}

// Two tables that point at each other are one join, not two: which direction
// it is written in is a choice, and making it twice would be making the query
// twice.
func TestTwoTablesPointingAtEachOtherAreOneJoin(t *testing.T) {
	db := schema()
	db.Schemas[0].Tables[0].ForeignKeys = []model.ForeignKey{{
		Name: "people_last_order", Columns: []string{"id"},
		RefSchema: "public", RefTable: "orders", RefColumns: []string{"id"}}}
	d := &Design{}
	d.Add(ref("public", "people"))
	d.Add(ref("public", "orders"))
	if got := Suggest(d, db); len(got) != 1 {
		t.Errorf("it suggested %+v", got)
	}
}

// A key over two columns is a join over two columns.
func TestAKeyOverTwoColumnsJoinsOnBoth(t *testing.T) {
	db := schema()
	db.Schemas[0].Tables[1].ForeignKeys = []model.ForeignKey{{
		Name: "orders_person", Columns: []string{"person_id", "total"},
		RefSchema: "public", RefTable: "people", RefColumns: []string{"id", "name"}}}
	d := &Design{}
	d.Add(ref("public", "people"))
	d.Add(ref("public", "orders"))
	got := Suggest(d, db)
	if len(got) != 1 || len(got[0].Join.On) != 2 {
		t.Fatalf("it suggested %+v", got)
	}
	Apply(d, got)
	if sql := render(t, d); !strings.Contains(sql, ` AND `) {
		t.Errorf("it reads\n%s", sql)
	}
}

// A key whose two sides do not line up is one nothing can write a condition
// from: the catalogue said something this does not understand, and inventing
// the pairing would be worse than suggesting nothing.
func TestAKeyThatDoesNotLineUpIsNotSuggested(t *testing.T) {
	for name, fk := range map[string]model.ForeignKey{
		"more columns than it refers to": {Name: "k", Columns: []string{"a", "b"},
			RefTable: "people", RefColumns: []string{"id"}},
		"no columns at all": {Name: "k", RefTable: "people"},
	} {
		t.Run(name, func(t *testing.T) {
			db := schema()
			db.Schemas[0].Tables[1].ForeignKeys = []model.ForeignKey{fk}
			d := &Design{}
			d.Add(ref("public", "people"))
			d.Add(ref("public", "orders"))
			if got := Suggest(d, db); len(got) != 0 {
				t.Errorf("it suggested %+v", got)
			}
		})
	}
}

// A table is looked for in the schema its ref names, and not in another: two
// schemas holding a table of one name is the commonest thing in a real
// database, and finding the wrong one would offer the wrong columns.
func TestATableIsLookedForInItsOwnSchema(t *testing.T) {
	db := schema()
	db.Schemas = append(db.Schemas, model.Schema{Name: "audit", Tables: []model.Table{
		{Name: "people", Columns: []model.Column{{Name: "when_"}, {Name: "who"}}},
	}})
	if got := Columns(db, ref("audit", "people")); strings.Join(got, ",") != "when_,who" {
		t.Errorf("audit.people offers %v", got)
	}
	if got := Columns(db, ref("public", "people")); strings.Join(got, ",") != "id,name" {
		t.Errorf("public.people offers %v", got)
	}
	// And a key into one of them is not a join to the other.
	d := &Design{}
	d.Add(ref("audit", "people"))
	d.Add(ref("public", "orders"))
	if got := Suggest(d, db); len(got) != 0 {
		t.Errorf("it suggested %+v", got)
	}
}

// A key with no schema on it refers to a table in the same schema, which is
// how half the catalogues here record one: treating it as another schema would
// find no joins at all in those databases.
func TestAKeyWithNoSchemaMeansThisOne(t *testing.T) {
	db := schema()
	db.Schemas[0].Tables[1].ForeignKeys[0].RefSchema = ""
	d := &Design{}
	d.Add(ref("public", "people"))
	d.Add(ref("public", "orders"))
	if got := Suggest(d, db); len(got) != 1 {
		t.Errorf("it suggested %+v", got)
	}
}

// A key pointing into another schema is not a join to a table of that name in
// this one.
func TestAKeyIntoAnotherSchemaIsNotThisTable(t *testing.T) {
	db := schema()
	db.Schemas[0].Tables[1].ForeignKeys[0].RefSchema = "elsewhere"
	d := &Design{}
	d.Add(ref("public", "people"))
	d.Add(ref("public", "orders"))
	if got := Suggest(d, db); len(got) != 0 {
		t.Errorf("it suggested %+v", got)
	}
}

func TestSuggestingOverNothing(t *testing.T) {
	d := &Design{}
	d.Add(ref("public", "people"))
	if got := Suggest(d, nil); got != nil {
		t.Errorf("with no schema it suggested %+v", got)
	}
	// A table the snapshot does not hold: a view, or something added since.
	d.Add(ref("public", "nowhere"))
	if got := Suggest(d, schema()); len(got) != 0 {
		t.Errorf("it suggested %+v about a table it has never seen", got)
	}
}

// Taking a table off the canvas takes everything that referred to it: a design
// that kept them would render a statement naming a table that is not in it.
func TestRemovingATableTakesWhatReferredToIt(t *testing.T) {
	d := &Design{}
	people := d.Add(ref("public", "people"))
	orders := d.Add(ref("public", "orders"))
	notes := d.Add(ref("public", "notes"))
	d.Joins = []Join{
		{Kind: JoinInner, Left: people, Right: orders, On: []Pair{{Left: "id", Right: "person_id"}}},
		{Kind: JoinInner, Left: people, Right: notes, On: []Pair{{Left: "id", Right: "id"}}},
	}
	d.Outputs = []Output{
		{Column: Column{Table: people, Name: "name"}},
		{Column: Column{Table: orders, Name: "total"}},
		{Column: Column{Table: notes, Name: "body"}},
	}
	d.Where = []Condition{{Column: Column{Table: orders, Name: "total"}, Op: source.OpGreater, Value: "1"}}
	d.Group = []Column{{Table: notes, Name: "body"}, {Table: orders, Name: "total"}}
	d.Order = []Sort{{Column: Column{Table: orders, Name: "total"}}}

	d.Remove(orders)
	if len(d.Tables) != 2 || d.Tables[0].Alias != "people" || d.Tables[1].Alias != "notes" {
		t.Fatalf("the canvas holds %v", d.Aliases())
	}
	if len(d.Joins) != 1 || d.Tables[d.Joins[0].Right].Alias != "notes" {
		t.Errorf("its joins are %+v", d.Joins)
	}
	if len(d.Outputs) != 2 {
		t.Errorf("it selects %+v", d.Outputs)
	}
	// Everything that pointed past the table it removed moved down with it.
	for _, o := range d.Outputs {
		if o.Column.Table < 0 || o.Column.Table >= len(d.Tables) {
			t.Errorf("an output points at table %d of %d", o.Column.Table, len(d.Tables))
		}
	}
	if len(d.Where) != 0 {
		t.Errorf("a condition on it survived: %+v", d.Where)
	}
	if len(d.Order) != 0 {
		t.Errorf("an ordering by it survived: %+v", d.Order)
	}
	if len(d.Group) != 1 || d.Tables[d.Group[0].Table].Alias != "notes" {
		t.Errorf("its grouping is %+v", d.Group)
	}
	// And what is left renders.
	if _, err := Render(d, pgLike{}); err != nil {
		t.Errorf("what was left does not render: %v", err)
	}
	// Removing something that is not there changes nothing.
	before := len(d.Tables)
	d.Remove(-1)
	d.Remove(99)
	if len(d.Tables) != before {
		t.Errorf("removing nothing removed something")
	}
}

// The tables and views a design can be built over, in a stable order.
func TestWhatADesignCanBeBuiltOver(t *testing.T) {
	db := schema()
	db.Schemas[0].Views = []model.View{{Name: "adults"}}
	plain := TableRefs(db, false)
	if len(plain) != 3 {
		t.Fatalf("it offers %+v", plain)
	}
	if plain[0].Name() != "notes" || plain[2].Name() != "people" {
		t.Errorf("they are ordered %v, %v, %v", plain[0].Path, plain[1].Path, plain[2].Path)
	}
	withViews := TableRefs(db, true)
	if len(withViews) != 4 || withViews[0].Name() != "adults" || withViews[0].Kind != model.KindView {
		t.Errorf("with views it offers %+v", withViews)
	}
	if TableRefs(nil, true) != nil {
		t.Error("over no database it offers something")
	}
	// A database with one nameless schema — MySQL's and SQLite's shape — names
	// its tables without one, as their own drivers do, and its columns are
	// still found: reading the database name as a schema would find nothing.
	flat := &model.Database{Name: "db", Schemas: []model.Schema{{
		Tables: []model.Table{{Name: "t", Columns: []model.Column{{Name: "a"}}}}}}}
	got := TableRefs(flat, false)
	if len(got) != 1 || len(got[0].Path) != 2 {
		t.Fatalf("a schemaless database names its tables %+v", got)
	}
	if cols := Columns(flat, got[0]); len(cols) != 1 || cols[0] != "a" {
		t.Errorf("its columns are %v", cols)
	}
}

// The columns a table offers, in the order it declares them.
func TestTheColumnsATableOffers(t *testing.T) {
	got := Columns(schema(), ref("public", "orders"))
	if strings.Join(got, ",") != "id,person_id,total" {
		t.Errorf("it offers %v", got)
	}
	if Columns(schema(), ref("public", "nowhere")) != nil {
		t.Error("a table it has never seen offers columns")
	}
	if Columns(nil, ref("public", "orders")) != nil {
		t.Error("with no schema at all it offers columns")
	}
}
