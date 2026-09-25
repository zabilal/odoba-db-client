package erd

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/query"
	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
)

// A query design drawn on the canvas (FR-9.1).
//
// The same canvas as the ER diagram, which is why that one was written without
// any database in it: a box is a box and a line is a line, and this is the
// other half that knows what they mean. A table on the designer's canvas is a
// node, its columns are ports, and a join is an edge.
//
// Two things are drawn differently from an ER diagram, because they mean
// something different. A node is titled by the name the query calls the table —
// the alias — with the table itself underneath, since the same table may be on
// the canvas twice and the alias is what tells them apart. And a port is
// emphasised when the column is selected rather than when it is a key: what a
// person is doing here is choosing columns, and what they have chosen is the
// thing worth seeing at a glance.

// FromDesign builds the diagram of a design. The database is where the columns
// come from; a table it does not hold is drawn as a box with no ports, which is
// what a view or a table added since the snapshot looks like.
func FromDesign(d *query.Design, db *model.Database) Diagram {
	if d == nil {
		return Diagram{Graph: canvas.NewGraph(nil, nil)}
	}
	nodes := make([]canvas.Node, len(d.Tables))
	for i, t := range d.Tables {
		nodes[i] = designNode(d, db, i, t)
	}
	edges := make([]canvas.Edge, 0, len(d.Joins))
	for _, j := range d.Joins {
		if j.Left < 0 || j.Left >= len(nodes) || j.Right < 0 || j.Right >= len(nodes) {
			continue // a join to a table that is not on the canvas draws nothing
		}
		edges = append(edges, designEdge(nodes, j, d))
	}
	g := canvas.NewGraph(nodes, edges)
	canvas.MeasureNodes(g)
	return Diagram{Graph: g}
}

// designNode is one table on the canvas.
func designNode(d *query.Design, db *model.Database, at int, t query.Table) canvas.Node {
	n := canvas.Node{
		ID:       t.Alias,
		Title:    t.Alias,
		Subtitle: strings.Join(t.Ref.Path[1:], "."),
	}
	if n.Subtitle == n.Title {
		// A database with no schemas — SQLite's and MySQL's shape — has
		// nothing to say underneath but the title again, which reads as a
		// mistake. Where there is a schema, the subtitle is worth the line.
		n.Subtitle = ""
	}
	for _, name := range query.Columns(db, t.Ref) {
		n.Ports = append(n.Ports, canvas.Port{
			Label:  name,
			Key:    selected(d, at, name),
			Detail: aggregateOf(d, at, name),
		})
	}
	return n
}

// selected reports whether a column is one the query selects — by name, or
// because the whole table is selected.
func selected(d *query.Design, at int, name string) bool {
	for _, o := range d.Outputs {
		if o.Column.Table != at {
			continue
		}
		if o.All || o.Column.Name == name {
			return true
		}
	}
	// Nothing chosen at all selects everything, which is what a canvas with
	// tables on it and nothing picked means — and the boxes should say so.
	return len(d.Outputs) == 0
}

// aggregateOf is the aggregate a column is selected through, where there is
// one: the detail line under a port, which is the type in an ER diagram and is
// the more useful thing to say here.
func aggregateOf(d *query.Design, at int, name string) string {
	for _, o := range d.Outputs {
		if o.Column.Table == at && o.Column.Name == name && o.Aggregate != query.AggregateNone {
			return string(o.Aggregate)
		}
	}
	return ""
}

// designEdge is one join.
//
// It is labelled with the kind of join rather than with a name, because that is
// the thing about a join somebody needs to see without opening it: an INNER and
// a LEFT between the same two tables are different queries. An inferred join
// says so, so that what the schema suggested can be told from what somebody
// decided (FR-9.1).
func designEdge(nodes []canvas.Node, j query.Join, d *query.Design) canvas.Edge {
	label := strings.TrimSuffix(string(j.Kind), " JOIN")
	if j.Inferred {
		label += " (from the schema)"
	}
	e := canvas.Edge{
		From: nodes[j.Left].ID, To: nodes[j.Right].ID,
		FromPort: -1, ToPort: -1, Label: label,
		// A join is many-to-many unless something says otherwise, and nothing
		// here does: a join is a condition, and a condition says nothing about
		// how many rows match it. The ER diagram's cardinality comes from a
		// declared key, which is a different claim.
		Cardinality: canvas.ManyToMany,
	}
	if len(j.On) > 0 {
		e.FromPort = portIn(nodes[j.Left], j.On[0].Left)
		e.ToPort = portIn(nodes[j.Right], j.On[0].Right)
	}
	return e
}

// portIn is where a named column sits on a node, or -1 where the node has not
// got it — a join on a column the designer cannot see attaches to the box,
// which is what the canvas's own -1 is for.
func portIn(n canvas.Node, column string) int {
	for i, p := range n.Ports {
		if p.Label == column {
			return i
		}
	}
	return -1
}
