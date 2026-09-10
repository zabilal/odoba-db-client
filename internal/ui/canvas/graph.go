// Package canvas implements the node canvas — spike W3 (T0.50–T0.54).
//
// One canvas serves two features: the ER diagram (FR-8) and the visual query
// designer (FR-9). They differ in what a node means and what an edge is
// derived from, not in how either is laid out, routed or drawn, so the model
// here is deliberately free of both.
//
// Nothing in graph.go or layout.go imports Fyne, so layout is benchmarkable on
// its own and the renderer can be replaced without touching it.
package canvas

import "math"

// Point is a position in graph space, which is independent of screen space and
// of zoom. Converting between the two is the viewport's job.
type Point struct{ X, Y float64 }

// Size is a width and height in graph space.
type Size struct{ W, H float64 }

// Rect is an axis-aligned rectangle in graph space.
type Rect struct {
	Min, Max Point
}

// Width and Height of the rectangle.
func (r Rect) Width() float64  { return r.Max.X - r.Min.X }
func (r Rect) Height() float64 { return r.Max.Y - r.Min.Y }

// Center of the rectangle.
func (r Rect) Center() Point {
	return Point{(r.Min.X + r.Max.X) / 2, (r.Min.Y + r.Max.Y) / 2}
}

// Intersects reports whether two rectangles overlap. This is the culling test,
// so it runs per node per frame.
func (r Rect) Intersects(o Rect) bool {
	return r.Min.X <= o.Max.X && r.Max.X >= o.Min.X &&
		r.Min.Y <= o.Max.Y && r.Max.Y >= o.Min.Y
}

// Contains reports whether a point lies inside, used for hit testing.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.Min.X && p.X <= r.Max.X && p.Y >= r.Min.Y && p.Y <= r.Max.Y
}

// Expand grows a rectangle on all sides.
func (r Rect) Expand(by float64) Rect {
	return Rect{Point{r.Min.X - by, r.Min.Y - by}, Point{r.Max.X + by, r.Max.Y + by}}
}

// Port is an attachment point on a node — a column, for an ER diagram.
type Port struct {
	// Label is the column name and type as displayed.
	Label string
	// Key marks a primary-key member, drawn with an emphasis.
	Key bool
	// Detail is secondary text, typically the column type.
	Detail string
}

// Node is one box on the canvas: a table, view, or query-designer source.
type Node struct {
	ID    string
	Title string
	// Subtitle carries the schema name, shown only when zoomed in enough to
	// read it.
	Subtitle string

	Ports []Port

	// Pos is the top-left corner in graph space. Written by layout and by
	// dragging; persisted so a hand-arranged diagram survives a reopen
	// (FR-8.2).
	Pos Point

	// Size is computed from the port count and the type scale.
	Size Size

	// Pinned excludes a node from automatic layout, so a manual arrangement
	// is not undone by a re-layout.
	Pinned bool
}

// Bounds returns the node's rectangle in graph space.
func (n Node) Bounds() Rect {
	return Rect{n.Pos, Point{n.Pos.X + n.Size.W, n.Pos.Y + n.Size.H}}
}

// Cardinality describes a relationship's multiplicity (FR-8.3).
type Cardinality uint8

const (
	OneToMany Cardinality = iota
	OneToOne
	ManyToMany
)

// Edge is a relationship between two nodes. For an ER diagram it is a foreign
// key; for the query designer, a join.
type Edge struct {
	From, To string

	// FromPort and ToPort index into each node's Ports, so an edge attaches to
	// the specific columns rather than to the box. -1 attaches to the node
	// itself, which is what the query designer uses before a join condition
	// is chosen.
	FromPort, ToPort int

	Cardinality Cardinality
	Label       string
}

// Graph is the whole diagram.
type Graph struct {
	Nodes []Node
	Edges []Edge

	index map[string]int
}

// NewGraph builds a graph and its node index.
func NewGraph(nodes []Node, edges []Edge) *Graph {
	g := &Graph{Nodes: nodes, Edges: edges}
	g.reindex()
	return g
}

func (g *Graph) reindex() {
	g.index = make(map[string]int, len(g.Nodes))
	for i, n := range g.Nodes {
		g.index[n.ID] = i
	}
}

// NodeByID returns a pointer to a node, or nil.
func (g *Graph) NodeByID(id string) *Node {
	if i, ok := g.index[id]; ok {
		return &g.Nodes[i]
	}
	return nil
}

// Bounds returns the rectangle enclosing every node, used to fit the view.
func (g *Graph) Bounds() Rect {
	if len(g.Nodes) == 0 {
		return Rect{}
	}
	r := g.Nodes[0].Bounds()
	for _, n := range g.Nodes[1:] {
		b := n.Bounds()
		r.Min.X = math.Min(r.Min.X, b.Min.X)
		r.Min.Y = math.Min(r.Min.Y, b.Min.Y)
		r.Max.X = math.Max(r.Max.X, b.Max.X)
		r.Max.Y = math.Max(r.Max.Y, b.Max.Y)
	}
	return r
}

// Neighbours returns the IDs adjacent to a node, in both directions. Drives
// "filter to N-degree neighbours" (FR-8.5), which is how a 200-table schema
// becomes readable.
func (g *Graph) Neighbours(id string) []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range g.Edges {
		var other string
		switch {
		case e.From == id:
			other = e.To
		case e.To == id:
			other = e.From
		default:
			continue
		}
		if other != id && !seen[other] {
			seen[other] = true
			out = append(out, other)
		}
	}
	return out
}

// WithinDegree returns every node within n hops of a root (FR-8.5).
func (g *Graph) WithinDegree(root string, n int) map[string]bool {
	keep := map[string]bool{root: true}
	frontier := []string{root}
	for d := 0; d < n; d++ {
		var next []string
		for _, id := range frontier {
			for _, nb := range g.Neighbours(id) {
				if !keep[nb] {
					keep[nb] = true
					next = append(next, nb)
				}
			}
		}
		if len(next) == 0 {
			break
		}
		frontier = next
	}
	return keep
}

// Components partitions the graph into connected components.
//
// Real schemas are rarely one connected mass: there are usually a few clusters
// plus a long tail of unreferenced tables. Laying out each component separately
// and packing them keeps isolated tables from being flung to the edges by
// repulsion alone.
func (g *Graph) Components() [][]int {
	seen := make([]bool, len(g.Nodes))
	adj := make([][]int, len(g.Nodes))
	for _, e := range g.Edges {
		i, ok1 := g.index[e.From]
		j, ok2 := g.index[e.To]
		if !ok1 || !ok2 || i == j {
			continue
		}
		adj[i] = append(adj[i], j)
		adj[j] = append(adj[j], i)
	}

	var out [][]int
	for i := range g.Nodes {
		if seen[i] {
			continue
		}
		var comp []int
		stack := []int{i}
		seen[i] = true
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp = append(comp, v)
			for _, w := range adj[v] {
				if !seen[w] {
					seen[w] = true
					stack = append(stack, w)
				}
			}
		}
		out = append(out, comp)
	}
	return out
}
