package erd

import (
	"slices"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
)

func col(name, native string) model.Column {
	return model.Column{Name: name, Type: model.DataType{Native: native, Length: -1}}
}

// sales is a schema with the shapes a diagram has to draw: a parent, a child
// that points at it, a child that points at it at most once, a table that
// points at itself, and one that points at nothing.
func sales() *model.Database {
	people := model.Table{Name: "people", RowsEstimate: -1,
		Columns:    []model.Column{col("id", "integer"), col("name", "text"), col("manager", "integer")},
		PrimaryKey: &model.PrimaryKey{Name: "people_pkey", Columns: []string{"id"}},
		ForeignKeys: []model.ForeignKey{{Name: "people_manager_fkey", Columns: []string{"manager"},
			RefTable: "people", RefColumns: []string{"id"}}}}
	orders := model.Table{Name: "orders", RowsEstimate: -1,
		Columns:    []model.Column{col("id", "integer"), col("who", "integer")},
		PrimaryKey: &model.PrimaryKey{Name: "orders_pkey", Columns: []string{"id"}},
		ForeignKeys: []model.ForeignKey{{Name: "orders_who_fkey", Columns: []string{"who"},
			RefSchema: "public", RefTable: "people", RefColumns: []string{"id"}}}}
	profile := model.Table{Name: "profile", RowsEstimate: -1,
		Columns:    []model.Column{col("person", "integer"), col("bio", "text")},
		PrimaryKey: &model.PrimaryKey{Name: "profile_pkey", Columns: []string{"person"}},
		ForeignKeys: []model.ForeignKey{{Name: "profile_person_fkey", Columns: []string{"person"},
			RefTable: "people", RefColumns: []string{"id"}}}}
	alone := model.Table{Name: "alone", RowsEstimate: -1, Columns: []model.Column{col("id", "integer")}}

	return &model.Database{Name: "sales", Schemas: []model.Schema{{Name: "public",
		Tables: []model.Table{people, orders, profile, alone},
		Views:  []model.View{{Name: "recent", Definition: "SELECT 1", Columns: []model.Column{col("id", "integer")}}}}}}
}

func idsOf(g *canvas.Graph) []string {
	var out []string
	for _, n := range g.Nodes {
		out = append(out, n.ID)
	}
	slices.Sort(out)
	return out
}

func edgeNamed(g *canvas.Graph, name string) (canvas.Edge, bool) {
	for _, e := range g.Edges {
		if e.Label == name {
			return e, true
		}
	}
	return canvas.Edge{}, false
}

// A table is a node, a column is a port, a foreign key is an edge.
func TestASchemaBecomesADiagram(t *testing.T) {
	d := FromSchema(sales(), Options{})
	if got, want := idsOf(d.Graph), []string{"public.alone", "public.orders", "public.people", "public.profile"}; !slices.Equal(got, want) {
		t.Fatalf("it drew %v, want %v", got, want)
	}
	if d.Outside != 0 {
		t.Errorf("%d keys were said to lead outside a diagram of everything", d.Outside)
	}
	people := d.Graph.NodeByID("public.people")
	if people == nil || len(people.Ports) != 3 {
		t.Fatalf("people has %v", people)
	}
	if people.Title != "people" || people.Subtitle != "public" {
		t.Errorf("it is titled %q under %q", people.Title, people.Subtitle)
	}
	// A column carries its type, and a key column is marked.
	if got := people.Ports[0]; got.Label != "id" || got.Detail != "integer" || !got.Key {
		t.Errorf("its first column is %+v", got)
	}
	if people.Ports[1].Key {
		t.Errorf("a column outside the key is marked as part of it: %+v", people.Ports[1])
	}
	// And a node has been measured, or it would be drawn with no size.
	if people.Size.W <= 0 || people.Size.H <= 0 {
		t.Errorf("it is %v across", people.Size)
	}
}

// An edge attaches to the columns the key is about, so a table with many
// columns shows which two are joined.
func TestARelationshipAttachesToItsColumns(t *testing.T) {
	d := FromSchema(sales(), Options{})
	e, ok := edgeNamed(d.Graph, "orders_who_fkey")
	if !ok {
		t.Fatal("the key was not drawn")
	}
	if e.From != "public.orders" || e.To != "public.people" {
		t.Errorf("it runs %s to %s", e.From, e.To)
	}
	orders := d.Graph.NodeByID("public.orders")
	people := d.Graph.NodeByID("public.people")
	if e.FromPort < 0 || orders.Ports[e.FromPort].Label != "who" {
		t.Errorf("it leaves from port %d", e.FromPort)
	}
	if e.ToPort < 0 || people.Ports[e.ToPort].Label != "id" {
		t.Errorf("it arrives at port %d", e.ToPort)
	}
}

// A key whose own side is unique can hold at most one row per parent, and is
// drawn as that. Everything else is one-to-many.
func TestHowManyOfEachEndAnEdgeHas(t *testing.T) {
	d := FromSchema(sales(), Options{})
	one, _ := edgeNamed(d.Graph, "profile_person_fkey")
	if one.Cardinality != canvas.OneToOne {
		t.Errorf("a key on the child's own primary key is %v", one.Cardinality)
	}
	many, _ := edgeNamed(d.Graph, "orders_who_fkey")
	if many.Cardinality != canvas.OneToMany {
		t.Errorf("a key on an ordinary column is %v", many.Cardinality)
	}
}

// A unique constraint or a unique index says the same thing a primary key
// does about how many rows there can be.
func TestWhatElseMakesAnEndSingular(t *testing.T) {
	child := func(mark func(*model.Table)) model.Table {
		t := model.Table{Name: "child", RowsEstimate: -1,
			Columns: []model.Column{col("id", "integer"), col("parent", "integer")},
			ForeignKeys: []model.ForeignKey{{Name: "k", Columns: []string{"parent"},
				RefTable: "parent", RefColumns: []string{"id"}}}}
		mark(&t)
		return t
	}
	parent := model.Table{Name: "parent", RowsEstimate: -1, Columns: []model.Column{col("id", "integer")},
		PrimaryKey: &model.PrimaryKey{Name: "parent_pkey", Columns: []string{"id"}}}

	for _, c := range []struct {
		what string
		mark func(*model.Table)
		want canvas.Cardinality
	}{
		{"a unique constraint", func(t *model.Table) {
			t.Uniques = []model.UniqueConstraint{{Name: "u", Columns: []string{"parent"}}}
		}, canvas.OneToOne},
		{"a unique index", func(t *model.Table) {
			t.Indexes = []model.Index{{Name: "i", Unique: true,
				Columns: []model.IndexColumn{{Name: "parent"}}}}
		}, canvas.OneToOne},
		{"a unique index over only some rows", func(t *model.Table) {
			t.Indexes = []model.Index{{Name: "i", Unique: true, Predicate: "parent > 0",
				Columns: []model.IndexColumn{{Name: "parent"}}}}
		}, canvas.OneToMany},
		{"a unique index on an expression", func(t *model.Table) {
			t.Indexes = []model.Index{{Name: "i", Unique: true,
				Columns: []model.IndexColumn{{Expression: "lower(parent)"}}}}
		}, canvas.OneToMany},
		{"an index that is not unique", func(t *model.Table) {
			t.Indexes = []model.Index{{Name: "i", Columns: []model.IndexColumn{{Name: "parent"}}}}
		}, canvas.OneToMany},
		{"nothing at all", func(*model.Table) {}, canvas.OneToMany},
	} {
		db := &model.Database{Name: "d", Schemas: []model.Schema{{Name: "public",
			Tables: []model.Table{parent, child(c.mark)}}}}
		e, ok := edgeNamed(FromSchema(db, Options{}).Graph, "k")
		if !ok {
			t.Fatalf("%s: the key was not drawn", c.what)
		}
		if e.Cardinality != c.want {
			t.Errorf("%s: it is %v, want %v", c.what, e.Cardinality, c.want)
		}
	}
}

// A table pointing at itself is an edge from a node to itself, not a
// relationship dropped for being odd.
func TestATableThatPointsAtItself(t *testing.T) {
	e, ok := edgeNamed(FromSchema(sales(), Options{}).Graph, "people_manager_fkey")
	if !ok {
		t.Fatal("it was not drawn")
	}
	if e.From != "public.people" || e.To != "public.people" {
		t.Errorf("it runs %s to %s", e.From, e.To)
	}
}

// A subset is drawn, and a key leading out of it is counted rather than
// silently missing: a table whose keys lead off the page should say so.
func TestASubsetSaysWhatLeadsOutOfIt(t *testing.T) {
	d := FromSchema(sales(), Options{Tables: []string{"public.orders"}})
	if got, want := idsOf(d.Graph), []string{"public.orders"}; !slices.Equal(got, want) {
		t.Fatalf("it drew %v, want %v", got, want)
	}
	if len(d.Graph.Edges) != 0 {
		t.Errorf("it drew %d edges to things it is not drawing", len(d.Graph.Edges))
	}
	if d.Outside != 1 {
		t.Errorf("it says %d keys lead outside, want 1", d.Outside)
	}
}

// A bare name is accepted where the database has one schema and refused
// where it has more, because there it would be ambiguous.
func TestNamingATableWithAndWithoutItsSchema(t *testing.T) {
	if got := idsOf(FromSchema(sales(), Options{Tables: []string{"orders"}}).Graph); len(got) != 1 {
		t.Errorf("with one schema, a bare name drew %v", got)
	}
	two := sales()
	two.Schemas = append(two.Schemas, model.Schema{Name: "audit",
		Tables: []model.Table{{Name: "log", RowsEstimate: -1}}})
	if got := idsOf(FromSchema(two, Options{Tables: []string{"orders"}}).Graph); len(got) != 0 {
		t.Errorf("with two schemas, a bare name drew %v", got)
	}
	if got := idsOf(FromSchema(two, Options{Tables: []string{"public.orders"}}).Graph); len(got) != 1 {
		t.Errorf("a qualified name drew %v", got)
	}
}

// Only the schemas asked for are drawn.
func TestDrawingOneSchemaOfSeveral(t *testing.T) {
	two := sales()
	two.Schemas = append(two.Schemas, model.Schema{Name: "audit",
		Tables: []model.Table{{Name: "log", RowsEstimate: -1}}})
	got := idsOf(FromSchema(two, Options{Schemas: []string{"audit"}}).Graph)
	if !slices.Equal(got, []string{"audit.log"}) {
		t.Errorf("it drew %v", got)
	}
}

// Views are drawn only when asked for: they have columns and are worth
// seeing, but nothing points at them.
func TestViewsAreDrawnOnlyWhenAskedFor(t *testing.T) {
	if slices.Contains(idsOf(FromSchema(sales(), Options{}).Graph), "public.recent") {
		t.Error("a view was drawn without being asked for")
	}
	got := idsOf(FromSchema(sales(), Options{Views: true}).Graph)
	if !slices.Contains(got, "public.recent") {
		t.Errorf("asked for, the views are %v", got)
	}
}

// A join table between two others is two foreign keys, and is drawn as two.
// Calling it a many-to-many would be this program's opinion about a pattern,
// drawn as though the server had said it.
func TestAJoinTableIsTwoRelationships(t *testing.T) {
	db := &model.Database{Name: "d", Schemas: []model.Schema{{Name: "public", Tables: []model.Table{
		{Name: "a", RowsEstimate: -1, Columns: []model.Column{col("id", "integer")}},
		{Name: "b", RowsEstimate: -1, Columns: []model.Column{col("id", "integer")}},
		{Name: "a_b", RowsEstimate: -1,
			Columns: []model.Column{col("a", "integer"), col("b", "integer")},
			ForeignKeys: []model.ForeignKey{
				{Name: "a_b_a_fkey", Columns: []string{"a"}, RefTable: "a", RefColumns: []string{"id"}},
				{Name: "a_b_b_fkey", Columns: []string{"b"}, RefTable: "b", RefColumns: []string{"id"}}}},
	}}}}
	g := FromSchema(db, Options{}).Graph
	if len(g.Edges) != 2 {
		t.Fatalf("it drew %d edges", len(g.Edges))
	}
	for _, e := range g.Edges {
		if e.Cardinality == canvas.ManyToMany {
			t.Errorf("%s was called a many-to-many", e.Label)
		}
	}
}

// The tables within n relationships of a starting point, which is how a
// schema of two hundred becomes a diagram somebody can read.
func TestTheTablesNearAStartingPoint(t *testing.T) {
	for _, c := range []struct {
		degree int
		want   []string
	}{
		{0, []string{"public.orders"}},
		{1, []string{"public.orders", "public.people"}},
		{2, []string{"public.orders", "public.people", "public.profile"}},
		{9, []string{"public.orders", "public.people", "public.profile"}},
	} {
		got := Neighbourhood(sales(), []string{"public.orders"}, c.degree)
		if !slices.Equal(got, c.want) {
			t.Errorf("%d away: %v, want %v", c.degree, got, c.want)
		}
	}
	// A table nobody points at has no neighbours, and a name nobody has is
	// not a starting point.
	if got := Neighbourhood(sales(), []string{"public.alone"}, 3); !slices.Equal(got, []string{"public.alone"}) {
		t.Errorf("an unreferenced table's neighbourhood is %v", got)
	}
	if got := Neighbourhood(sales(), []string{"public.nothing"}, 3); len(got) != 0 {
		t.Errorf("a table nobody has is %v", got)
	}
}

// Every table a diagram could draw, for whatever offers the choice.
func TestNamingEveryTable(t *testing.T) {
	got := Tables(sales())
	want := []string{"public.alone", "public.orders", "public.people", "public.profile"}
	if !slices.Equal(got, want) {
		t.Errorf("it named %v, want %v", got, want)
	}
	if got := Tables(nil); got != nil {
		t.Errorf("nothing named %v", got)
	}
}

// A diagram of nothing is a diagram, not a crash.
func TestADiagramOfNothing(t *testing.T) {
	d := FromSchema(nil, Options{})
	if d.Graph == nil || len(d.Graph.Nodes) != 0 || d.Outside != 0 {
		t.Errorf("it drew %+v", d)
	}
	empty := FromSchema(&model.Database{Name: "d"}, Options{})
	if len(empty.Graph.Nodes) != 0 {
		t.Errorf("an empty database drew %d nodes", len(empty.Graph.Nodes))
	}
}

// A diagram is named after what it draws.
func TestWhatADiagramIsCalled(t *testing.T) {
	for _, c := range []struct {
		what string
		opt  Options
		want string
	}{
		{"everything", Options{}, "sales"},
		{"one table", Options{Tables: []string{"public.orders"}}, "public.orders"},
		{"one schema", Options{Schemas: []string{"public"}}, "public"},
		{"two schemas", Options{Schemas: []string{"public", "audit"}}, "public, audit"},
	} {
		if got := Title(sales(), c.opt); got != c.want {
			t.Errorf("%s is called %q, want %q", c.what, got, c.want)
		}
	}
	if got := Title(nil, Options{}); got == "" || strings.TrimSpace(got) != got {
		t.Errorf("nothing is called %q", got)
	}
}
