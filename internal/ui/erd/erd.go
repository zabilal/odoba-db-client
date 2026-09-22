// Package erd turns a database's structure into a diagram (FR-8.1).
//
// The canvas knows about nodes, ports and edges and nothing about databases,
// deliberately, so that one canvas can serve the ER diagram and the visual
// query designer. This is the half that knows about databases: a table is a
// node, a column is a port, a foreign key is an edge.
//
// It draws what the catalogue says and nothing it has inferred. A join table
// between two others is two foreign keys, so it is drawn as two: calling it
// a many-to-many would be this program's opinion about a pattern, drawn as
// though the server had said it.
package erd

import (
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
)

// Options say what to draw.
type Options struct {
	// Schemas are the schemas to draw. Empty draws them all.
	Schemas []string

	// Tables are the tables to draw, named as schema.table or as a bare
	// name where the database has one schema. Empty draws every table in
	// whatever schemas are being drawn.
	Tables []string

	// Views draws views beside the tables. They have columns and are worth
	// seeing, but nothing points at them, so a diagram of a schema with
	// many of them is mostly boxes with no lines.
	Views bool
}

// Diagram is a graph and what had to be left out of it.
type Diagram struct {
	Graph *canvas.Graph

	// Outside counts the foreign keys that point at something not drawn.
	// A diagram of a subset is honest about its edges: a table whose keys
	// lead off the page should say so rather than look unrelated.
	Outside int
}

// FromSchema builds a diagram of a database.
func FromSchema(db *model.Database, opt Options) Diagram {
	if db == nil {
		return Diagram{Graph: canvas.NewGraph(nil, nil)}
	}
	var nodes []canvas.Node
	here := map[string]bool{}
	oneSchema := len(db.Schemas) == 1

	for _, s := range db.Schemas {
		if !drawing(opt.Schemas, s.Name) {
			continue
		}
		for _, t := range s.Tables {
			if !wanted(opt.Tables, s.Name, t.Name, oneSchema) {
				continue
			}
			nodes = append(nodes, node(s.Name, t.Name, t.Columns, keyed(t)))
			here[id(s.Name, t.Name)] = true
		}
		if !opt.Views {
			continue
		}
		for _, v := range s.Views {
			if !wanted(opt.Tables, s.Name, v.Name, oneSchema) {
				continue
			}
			nodes = append(nodes, node(s.Name, v.Name, v.Columns, nil))
			here[id(s.Name, v.Name)] = true
		}
	}

	edges, outside := relationships(db, nodes, here, opt)
	g := canvas.NewGraph(nodes, edges)
	canvas.MeasureNodes(g)
	return Diagram{Graph: g, Outside: outside}
}

// id names a node. A table is known by its schema and its name, because two
// schemas may hold tables of one name and a diagram may show both.
func id(schema, name string) string { return schema + "." + name }

// node builds one box: the table's name, its schema underneath, and a port
// per column.
func node(schema, name string, cols []model.Column, key map[string]bool) canvas.Node {
	n := canvas.Node{ID: id(schema, name), Title: name, Subtitle: schema}
	for _, c := range cols {
		n.Ports = append(n.Ports, canvas.Port{
			Label: c.Name, Detail: c.Type.Native, Key: key[c.Name],
		})
	}
	return n
}

// keyed is the columns a table's primary key is made of.
func keyed(t model.Table) map[string]bool {
	if t.PrimaryKey == nil {
		return nil
	}
	out := make(map[string]bool, len(t.PrimaryKey.Columns))
	for _, c := range t.PrimaryKey.Columns {
		out[c] = true
	}
	return out
}

// relationships turns the foreign keys into edges, and counts the ones that
// lead somewhere not drawn.
func relationships(db *model.Database, nodes []canvas.Node, here map[string]bool, opt Options) ([]canvas.Edge, int) {
	at := map[string]int{}
	for i, n := range nodes {
		at[n.ID] = i
	}
	var edges []canvas.Edge
	outside := 0

	for _, s := range db.Schemas {
		if !drawing(opt.Schemas, s.Name) {
			continue
		}
		for _, t := range s.Tables {
			from := id(s.Name, t.Name)
			if !here[from] {
				continue
			}
			for _, k := range t.ForeignKeys {
				to := id(refSchema(k, s.Name), k.RefTable)
				if !here[to] {
					outside++
					continue
				}
				edges = append(edges, edge(nodes, at, from, to, k, t))
			}
		}
	}
	return edges, outside
}

// refSchema is the schema a key points into, which is the key's own schema
// where it does not say — an unqualified reference is to the same one.
func refSchema(k model.ForeignKey, own string) string {
	if k.RefSchema == "" {
		return own
	}
	return k.RefSchema
}

// edge attaches a relationship to the columns it is about rather than to the
// boxes, so that a table with twenty columns shows which two the key joins.
func edge(nodes []canvas.Node, at map[string]int, from, to string, k model.ForeignKey, t model.Table) canvas.Edge {
	e := canvas.Edge{From: from, To: to, Label: k.Name,
		FromPort:    portOf(nodes, at, from, first(k.Columns)),
		ToPort:      portOf(nodes, at, to, first(k.RefColumns)),
		Cardinality: canvas.OneToMany,
	}
	// One-to-one when the child's own side is unique: a key whose columns
	// are the child's primary key, or a unique constraint on exactly them,
	// can hold at most one row per parent.
	if uniqueIn(t, k.Columns) {
		e.Cardinality = canvas.OneToOne
	}
	return e
}

// portOf is the index of a named column on a node, or -1 where the node does
// not have it — a key on a column the diagram is not drawing attaches to the
// box rather than to nothing.
func portOf(nodes []canvas.Node, at map[string]int, nodeID, column string) int {
	i, ok := at[nodeID]
	if !ok || column == "" {
		return -1
	}
	for p, port := range nodes[i].Ports {
		if port.Label == column {
			return p
		}
	}
	return -1
}

func first(cols []string) string {
	if len(cols) == 0 {
		return ""
	}
	return cols[0]
}

// uniqueIn reports whether a table guarantees at most one row per value of
// these columns.
func uniqueIn(t model.Table, cols []string) bool {
	if len(cols) == 0 {
		return false
	}
	if t.PrimaryKey != nil && sameSet(t.PrimaryKey.Columns, cols) {
		return true
	}
	for _, u := range t.Uniques {
		if sameSet(u.Columns, cols) {
			return true
		}
	}
	for _, i := range t.Indexes {
		// A unique index over only some rows guarantees nothing about the
		// rest, so it says nothing about how many rows a parent can have.
		if !i.Unique || i.Predicate != "" {
			continue
		}
		// An index over an expression is not a guarantee about a column, and
		// needs no check of its own: an expression column carries no name,
		// so the empty name it contributes matches no column a key names.
		names := make([]string, len(i.Columns))
		for j, c := range i.Columns {
			names[j] = c.Name
		}
		if sameSet(names, cols) {
			return true
		}
	}
	return false
}

// sameSet reports two lists of column names holding the same names. Order is
// not compared: a unique constraint on (a, b) guarantees the same thing as
// one on (b, a).
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

// drawing reports a schema this diagram includes.
func drawing(only []string, name string) bool {
	return len(only) == 0 || slices.Contains(only, name)
}

// wanted reports a table this diagram includes.
//
// A name may be given qualified or bare. Bare is accepted only where the
// database has one schema: elsewhere it would be ambiguous, and a diagram
// that quietly drew the wrong table of two would be worse than one that drew
// neither.
func wanted(only []string, schema, name string, oneSchema bool) bool {
	if len(only) == 0 {
		return true
	}
	if slices.Contains(only, schema+"."+name) {
		return true
	}
	return oneSchema && slices.Contains(only, name)
}

// Tables names every table a diagram could draw, for whatever offers the
// choice of a subset.
func Tables(db *model.Database) []string {
	if db == nil {
		return nil
	}
	var out []string
	for _, s := range db.Schemas {
		for _, t := range s.Tables {
			out = append(out, s.Name+"."+t.Name)
		}
	}
	slices.Sort(out)
	return out
}

// Neighbourhood is the tables within n relationships of these, which is how
// a schema of two hundred becomes a diagram somebody can read (FR-8.5).
func Neighbourhood(db *model.Database, of []string, degree int) []string {
	full := FromSchema(db, Options{}).Graph
	seen := map[string]bool{}
	var edge []string
	for _, t := range of {
		if full.NodeByID(t) != nil {
			seen[t] = true
			edge = append(edge, t)
		}
	}
	for ; degree > 0 && len(edge) > 0; degree-- {
		var next []string
		for _, id := range edge {
			for _, n := range full.Neighbours(id) {
				if !seen[n] {
					seen[n] = true
					next = append(next, n)
				}
			}
		}
		edge = next
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Title names a diagram, for a tab and for an exported file.
func Title(db *model.Database, opt Options) string {
	switch {
	case db == nil:
		return "Diagram"
	case len(opt.Tables) == 1:
		return opt.Tables[0]
	case len(opt.Schemas) == 1:
		return opt.Schemas[0]
	case len(opt.Schemas) > 1:
		return strings.Join(opt.Schemas, ", ")
	}
	return db.Name
}
