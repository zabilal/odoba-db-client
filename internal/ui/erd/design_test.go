package erd

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/query"
	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
)

// A design drawn on the canvas (FR-9.1).

func designSchema() *model.Database {
	return &model.Database{Name: "db", Schemas: []model.Schema{{
		Name: "public",
		Tables: []model.Table{
			{Name: "people", Columns: []model.Column{{Name: "id"}, {Name: "name"}}},
			{Name: "orders", Columns: []model.Column{{Name: "id"}, {Name: "person_id"}, {Name: "total"}},
				ForeignKeys: []model.ForeignKey{{Name: "orders_person", Columns: []string{"person_id"},
					RefSchema: "public", RefTable: "people", RefColumns: []string{"id"}}}},
		},
	}}}
}

func tableRef(name string) model.ObjectRef {
	return model.NewRef(model.KindTable, "db", "public", name)
}

func TestATableIsABoxWithItsColumns(t *testing.T) {
	d := &query.Design{}
	d.Add(tableRef("people"))
	g := FromDesign(d, designSchema()).Graph
	if len(g.Nodes) != 1 {
		t.Fatalf("it drew %d boxes", len(g.Nodes))
	}
	n := g.Nodes[0]
	if n.ID != "people" || n.Title != "people" {
		t.Errorf("the box is %+v", n)
	}
	// The schema is worth the line under the title, whatever the alias is:
	// two tables of one name in two schemas are two tables.
	if n.Subtitle != "public.people" {
		t.Errorf("it is subtitled %q", n.Subtitle)
	}
	if len(n.Ports) != 2 || n.Ports[0].Label != "id" || n.Ports[1].Label != "name" {
		t.Errorf("its columns are %+v", n.Ports)
	}
	// Measured, or the layout has nothing to arrange.
	if n.Size.W <= 0 || n.Size.H <= 0 {
		t.Errorf("it is %v by %v", n.Size.W, n.Size.H)
	}
}

// A database with no schemas has nothing to say under the title but the title
// again, which reads as a mistake.
func TestASchemalessTableIsNotSubtitledWithItself(t *testing.T) {
	flat := &model.Database{Name: "main", Schemas: []model.Schema{{
		Tables: []model.Table{{Name: "people", Columns: []model.Column{{Name: "id"}}}},
	}}}
	d := &query.Design{}
	d.Add(model.NewRef(model.KindTable, "main", "people"))
	n := FromDesign(d, flat).Graph.Nodes[0]
	if n.Title != "people" || n.Subtitle != "" {
		t.Errorf("it is %q / %q", n.Title, n.Subtitle)
	}
	if len(n.Ports) != 1 {
		t.Errorf("its columns are %+v", n.Ports)
	}
}

// The same table twice is two boxes, titled by what the query calls them and
// subtitled by what they are.
func TestTheSameTableTwiceIsTwoBoxes(t *testing.T) {
	d := &query.Design{}
	d.Add(tableRef("people"))
	d.Add(tableRef("people"))
	g := FromDesign(d, designSchema()).Graph
	if len(g.Nodes) != 2 {
		t.Fatalf("it drew %d boxes", len(g.Nodes))
	}
	if g.Nodes[1].Title != "people2" || g.Nodes[1].Subtitle != "public.people" {
		t.Errorf("the second box is %q / %q", g.Nodes[1].Title, g.Nodes[1].Subtitle)
	}
	if g.NodeByID("people2") == nil {
		t.Error("the second box cannot be found by its name")
	}
}

// A column the query selects is emphasised: what somebody is doing here is
// choosing columns, and what they have chosen is worth seeing at a glance.
func TestTheChosenColumnsAreMarked(t *testing.T) {
	d := &query.Design{}
	at := d.Add(tableRef("people"))
	d.Outputs = []query.Output{{Column: query.Column{Table: at, Name: "name"}}}
	ports := FromDesign(d, designSchema()).Graph.Nodes[0].Ports
	if ports[0].Key {
		t.Error("a column nobody chose is marked")
	}
	if !ports[1].Key {
		t.Error("the chosen column is not marked")
	}
}

// Nothing chosen selects everything, and the boxes say so.
func TestNothingChosenMarksEverything(t *testing.T) {
	d := &query.Design{}
	d.Add(tableRef("people"))
	for _, p := range FromDesign(d, designSchema()).Graph.Nodes[0].Ports {
		if !p.Key {
			t.Errorf("%s is not marked, and nothing was chosen", p.Label)
		}
	}
}

// Every column of one table, chosen as a whole, marks all of them.
func TestEveryColumnOfATableMarksAll(t *testing.T) {
	d := &query.Design{}
	people := d.Add(tableRef("people"))
	orders := d.Add(tableRef("orders"))
	d.Outputs = []query.Output{
		{All: true, Column: query.Column{Table: people}},
		{Column: query.Column{Table: orders, Name: "total"}},
	}
	g := FromDesign(d, designSchema()).Graph
	for _, p := range g.NodeByID("people").Ports {
		if !p.Key {
			t.Errorf("people.%s is not marked", p.Label)
		}
	}
	marked := 0
	for _, p := range g.NodeByID("orders").Ports {
		if p.Key {
			marked++
		}
	}
	if marked != 1 {
		t.Errorf("%d of orders' columns are marked", marked)
	}
}

// An aggregate is said under the column it is on, which is where an ER diagram
// puts the type and is the more useful thing to say here.
func TestAnAggregateIsSaidUnderItsColumn(t *testing.T) {
	d := &query.Design{}
	at := d.Add(tableRef("orders"))
	d.Outputs = []query.Output{{Column: query.Column{Table: at, Name: "total"}, Aggregate: query.AggregateSum}}
	for _, p := range FromDesign(d, designSchema()).Graph.Nodes[0].Ports {
		switch p.Label {
		case "total":
			if p.Detail != "SUM" {
				t.Errorf("total says %q", p.Detail)
			}
		default:
			if p.Detail != "" {
				t.Errorf("%s says %q", p.Label, p.Detail)
			}
		}
	}
}

// A join is a line, labelled with the kind — an INNER and a LEFT between the
// same two tables are different queries — and attached to the columns it
// matches.
func TestAJoinIsALineBetweenTheColumnsItMatches(t *testing.T) {
	d := &query.Design{}
	people := d.Add(tableRef("people"))
	orders := d.Add(tableRef("orders"))
	d.Joins = []query.Join{{Kind: query.JoinLeft, Left: people, Right: orders,
		On: []query.Pair{{Left: "id", Right: "person_id"}}}}
	g := FromDesign(d, designSchema()).Graph
	if len(g.Edges) != 1 {
		t.Fatalf("it drew %d lines", len(g.Edges))
	}
	e := g.Edges[0]
	if e.From != "people" || e.To != "orders" {
		t.Errorf("it runs %s to %s", e.From, e.To)
	}
	if e.Label != "LEFT" {
		t.Errorf("it is labelled %q", e.Label)
	}
	if e.FromPort != 0 || e.ToPort != 1 {
		t.Errorf("it attaches to ports %d and %d", e.FromPort, e.ToPort)
	}
	// A join is a condition, and a condition says nothing about how many rows
	// match it: the ER diagram's cardinality comes from a declared key, which
	// is a different claim.
	if e.Cardinality != canvas.ManyToMany {
		t.Errorf("it claims a cardinality of %v", e.Cardinality)
	}
}

// A join the schema suggested says so, so that it can be told from one
// somebody drew.
func TestAnInferredJoinSaysSo(t *testing.T) {
	d := &query.Design{}
	d.Add(tableRef("people"))
	d.Add(tableRef("orders"))
	query.Apply(d, query.Suggest(d, designSchema()))
	g := FromDesign(d, designSchema()).Graph
	if len(g.Edges) != 1 {
		t.Fatalf("it drew %d lines", len(g.Edges))
	}
	if !strings.Contains(g.Edges[0].Label, "from the schema") {
		t.Errorf("it is labelled %q", g.Edges[0].Label)
	}
}

// A cross join has no columns, so its line attaches to the boxes: which is what
// the canvas's own -1 port is for.
func TestACrossJoinAttachesToTheBoxes(t *testing.T) {
	d := &query.Design{}
	a := d.Add(tableRef("people"))
	b := d.Add(tableRef("orders"))
	d.Joins = []query.Join{{Kind: query.JoinCross, Left: a, Right: b}}
	e := FromDesign(d, designSchema()).Graph.Edges[0]
	if e.FromPort != -1 || e.ToPort != -1 {
		t.Errorf("it attaches to ports %d and %d", e.FromPort, e.ToPort)
	}
	if e.Label != "CROSS" {
		t.Errorf("it is labelled %q", e.Label)
	}
}

// A join on a column the designer cannot see attaches to the box rather than to
// nothing.
func TestAJoinOnAColumnNobodyCanSeeAttachesToTheBox(t *testing.T) {
	d := &query.Design{}
	a := d.Add(tableRef("people"))
	b := d.Add(tableRef("orders"))
	d.Joins = []query.Join{{Kind: query.JoinInner, Left: a, Right: b,
		On: []query.Pair{{Left: "gone", Right: "also gone"}}}}
	e := FromDesign(d, designSchema()).Graph.Edges[0]
	if e.FromPort != -1 || e.ToPort != -1 {
		t.Errorf("it attaches to ports %d and %d", e.FromPort, e.ToPort)
	}
}

// A table the snapshot does not hold is a box with no columns — a view, or a
// table added since — rather than nothing at all.
func TestATableNobodyHasSeenIsStillABox(t *testing.T) {
	d := &query.Design{}
	d.Add(tableRef("nowhere"))
	g := FromDesign(d, designSchema()).Graph
	if len(g.Nodes) != 1 || len(g.Nodes[0].Ports) != 0 {
		t.Errorf("it drew %+v", g.Nodes)
	}
	if g.Nodes[0].Size.H <= 0 {
		t.Error("a box with no columns has no height")
	}
}

func TestDrawingNothing(t *testing.T) {
	if g := FromDesign(nil, designSchema()).Graph; g == nil || len(g.Nodes) != 0 {
		t.Errorf("no design drew %+v", g)
	}
	if g := FromDesign(&query.Design{}, nil).Graph; len(g.Nodes) != 0 {
		t.Errorf("an empty design drew %+v", g.Nodes)
	}
	// A design with a table and no schema to read its columns from: a box, and
	// no ports.
	d := &query.Design{}
	d.Add(tableRef("people"))
	if g := FromDesign(d, nil).Graph; len(g.Nodes) != 1 || len(g.Nodes[0].Ports) != 0 {
		t.Errorf("with no schema it drew %+v", g.Nodes)
	}
}

// A join naming a table that is not on the canvas draws no line, rather than
// a line to nothing.
func TestAJoinToNowhereDrawsNothing(t *testing.T) {
	d := &query.Design{}
	d.Add(tableRef("people"))
	d.Joins = []query.Join{{Kind: query.JoinInner, Left: 0, Right: 9,
		On: []query.Pair{{Left: "id", Right: "id"}}}}
	if g := FromDesign(d, designSchema()).Graph; len(g.Edges) != 0 {
		t.Errorf("it drew %+v", g.Edges)
	}
}
