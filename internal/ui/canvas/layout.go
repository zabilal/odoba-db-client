package canvas

import (
	"math"
	"math/rand"
	"sort"
)

// Automatic layout (T0.53).
//
// Force-directed rather than layered. Layered (Sugiyama) layouts are excellent
// for DAGs, and a database schema is not one: foreign keys form cycles
// routinely, and a layered algorithm has to break them arbitrarily, which
// produces a different picture each time the schema changes slightly.
//
// Written directly rather than pulled from a library. gonum/graph has no force
// layout, and the graph libraries that do are topology-only. The algorithm is
// about 150 lines and needs to know node box sizes to avoid overlap, which a
// generic implementation would not.
//
// Layout is deterministic: the same schema always produces the same picture.
// A diagram that rearranges itself on every open is not a diagram, it is a
// distraction.

// Node metrics, in graph-space units. These match the theme's type scale so
// that a node at zoom 1.0 is drawn at its natural size.
const (
	NodeWidth     = 190.0
	NodeHeaderH   = 30.0
	NodePortH     = 18.0
	NodePaddingY  = 6.0
	NodeMaxPorts  = 12 // beyond this a node is truncated with a "+N more" row
	NodeMinHeight = NodeHeaderH + NodePaddingY*2
)

// LayoutOptions parameterises automatic layout.
type LayoutOptions struct {
	// Iterations of the force simulation. More is tidier and slower.
	Iterations int

	// Spacing is the ideal separation between unrelated nodes.
	Spacing float64

	// ComponentGap separates packed connected components.
	ComponentGap float64

	// Width is the target canvas width used when packing components.
	Width float64

	// Seed makes the layout reproducible.
	Seed int64
}

// DefaultLayout returns options tuned for schema diagrams.
func DefaultLayout() LayoutOptions {
	return LayoutOptions{
		Iterations:   300,
		Spacing:      70,
		ComponentGap: 90,
		Width:        2400,
		Seed:         1,
	}
}

// MeasureNodes computes each node's box size from its port count.
func MeasureNodes(g *Graph) {
	for i := range g.Nodes {
		n := &g.Nodes[i]
		ports := len(n.Ports)
		if ports > NodeMaxPorts {
			ports = NodeMaxPorts + 1 // the "+N more" row
		}
		h := NodeHeaderH + float64(ports)*NodePortH + NodePaddingY*2
		if h < NodeMinHeight {
			h = NodeMinHeight
		}
		n.Size = Size{W: NodeWidth, H: h}
	}
}

// Layout positions every unpinned node.
//
// Pinned nodes keep their positions and still exert force on others, so a
// user's manual arrangement survives a re-layout (FR-8.2).
func Layout(g *Graph, opt LayoutOptions) {
	if len(g.Nodes) == 0 {
		return
	}
	MeasureNodes(g)

	comps := g.Components()

	// Largest component first: it dominates the picture and belongs at the
	// top-left where the eye starts.
	sort.SliceStable(comps, func(i, j int) bool { return len(comps[i]) > len(comps[j]) })

	boxes := make([]Rect, len(comps))
	for ci, comp := range comps {
		layoutComponent(g, comp, opt, int64(ci))
		boxes[ci] = componentBounds(g, comp)
	}
	packComponents(g, comps, boxes, opt)
}

// layoutComponent runs the force simulation over one connected component.
func layoutComponent(g *Graph, comp []int, opt LayoutOptions, salt int64) {
	n := len(comp)
	if n == 1 {
		g.Nodes[comp[0]].Pos = Point{}
		return
	}

	local := make(map[int]int, n) // graph index -> component index
	for i, gi := range comp {
		local[gi] = i
	}

	pos := make([]Point, n)
	rng := rand.New(rand.NewSource(opt.Seed*1000 + salt))

	// Seed on a circle rather than uniformly at random: a circle has no
	// clumps, so the simulation spends its iterations refining structure
	// instead of untangling a random start.
	radius := math.Sqrt(float64(n)) * opt.Spacing * 0.5
	for i := range pos {
		if g.Nodes[comp[i]].Pinned {
			pos[i] = g.Nodes[comp[i]].Pos
			continue
		}
		a := 2 * math.Pi * float64(i) / float64(n)
		jitter := (rng.Float64() - 0.5) * opt.Spacing * 0.3
		pos[i] = Point{
			X: math.Cos(a)*(radius+jitter) + radius,
			Y: math.Sin(a)*(radius+jitter) + radius,
		}
	}

	// Component-local edges.
	type link struct{ a, b int }
	var links []link
	for _, e := range g.Edges {
		i, ok1 := g.index[e.From]
		j, ok2 := g.index[e.To]
		if !ok1 || !ok2 {
			continue
		}
		li, in1 := local[i]
		lj, in2 := local[j]
		if in1 && in2 && li != lj {
			links = append(links, link{li, lj})
		}
	}

	// Frame the drawing area.
	//
	// Fruchterman-Reingold constrains nodes to a frame, and omitting it is not
	// a cosmetic shortcut: repulsion is summed over every pair, so without a
	// bound the equilibrium radius grows as sqrt(n)*k and a 150-node component
	// spreads across thousands of units with a hole in the middle. The frame
	// is sized from the total area the boxes actually occupy, in a landscape
	// ratio because that is the shape of the window it will be viewed in.
	var totalArea float64
	for _, gi := range comp {
		totalArea += (NodeWidth + opt.Spacing) * (g.Nodes[gi].Size.H + opt.Spacing)
	}
	const aspect = 1.45
	const slack = 1.5 // room for the layout to breathe without a hole
	frameW := math.Sqrt(totalArea * aspect * slack)
	frameH := math.Sqrt(totalArea / aspect * slack)

	k := math.Sqrt(frameW * frameH / float64(n))
	temp := math.Max(frameW, frameH) / 8
	cool := temp / float64(opt.Iterations+1)
	disp := make([]Point, n)

	for iter := 0; iter < opt.Iterations; iter++ {
		for i := range disp {
			disp[i] = Point{}
		}

		// Repulsion between every pair. O(n^2), which for the component sizes
		// a schema produces is far cheaper than the bookkeeping a Barnes-Hut
		// tree would cost.
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				dx := pos[i].X - pos[j].X
				dy := pos[i].Y - pos[j].Y
				d2 := dx*dx + dy*dy
				if d2 < 0.01 {
					// Coincident nodes: nudge deterministically so they
					// separate without the result depending on iteration order.
					dx, dy, d2 = float64(i%7)-3, float64(j%5)-2, 1
				}
				d := math.Sqrt(d2)
				f := k * k / d
				ux, uy := dx/d, dy/d
				disp[i].X += ux * f
				disp[i].Y += uy * f
				disp[j].X -= ux * f
				disp[j].Y -= uy * f
			}
		}

		// Attraction along edges.
		for _, l := range links {
			dx := pos[l.a].X - pos[l.b].X
			dy := pos[l.a].Y - pos[l.b].Y
			d := math.Hypot(dx, dy)
			if d < 0.01 {
				continue
			}
			f := d * d / k
			ux, uy := dx/d, dy/d
			disp[l.a].X -= ux * f
			disp[l.a].Y -= uy * f
			disp[l.b].X += ux * f
			disp[l.b].Y += uy * f
		}

		for i := range pos {
			if g.Nodes[comp[i]].Pinned {
				continue
			}
			d := math.Hypot(disp[i].X, disp[i].Y)
			if d < 1e-9 {
				continue
			}
			lim := math.Min(d, temp)
			pos[i].X += disp[i].X / d * lim
			pos[i].Y += disp[i].Y / d * lim

			// Hold nodes inside the frame. This is what bounds the layout.
			pos[i].X = clampF(pos[i].X, 0, frameW)
			pos[i].Y = clampF(pos[i].Y, 0, frameH)
		}
		temp -= cool
	}

	for i, gi := range comp {
		if !g.Nodes[gi].Pinned {
			g.Nodes[gi].Pos = pos[i]
		}
	}
	resolveOverlaps(g, comp)
}

// resolveOverlaps pushes overlapping boxes apart.
//
// The force simulation treats nodes as points, so wide boxes routinely end up
// overlapping even when the point layout is good. A diagram with overlapping
// tables is unreadable regardless of how well-structured it is underneath.
//
// Nodes are deliberately allowed to move outside the simulation frame here.
// The frame exists to bound repulsion, not to bound the picture, and clamping
// during separation is what leaves nodes piled along its edge.
func resolveOverlaps(g *Graph, comp []int) {
	const passes = 120
	const pad = 16.0

	for p := 0; p < passes; p++ {
		moved := false
		for i := 0; i < len(comp); i++ {
			for j := i + 1; j < len(comp); j++ {
				a, b := &g.Nodes[comp[i]], &g.Nodes[comp[j]]
				ra, rb := a.Bounds().Expand(pad/2), b.Bounds().Expand(pad/2)
				if !ra.Intersects(rb) {
					continue
				}

				// Separate along the axis of least penetration, which keeps
				// the arrangement the simulation found.
				ca, cb := ra.Center(), rb.Center()
				ox := (ra.Width()+rb.Width())/2 - math.Abs(ca.X-cb.X)
				oy := (ra.Height()+rb.Height())/2 - math.Abs(ca.Y-cb.Y)
				if ox <= 0 || oy <= 0 {
					continue
				}
				moved = true

				if ox < oy {
					shift := ox / 2
					if ca.X < cb.X {
						shift = -shift
					}
					movePair(a, b, shift, 0)
				} else {
					shift := oy / 2
					if ca.Y < cb.Y {
						shift = -shift
					}
					movePair(a, b, 0, shift)
				}
			}
		}
		if !moved {
			return
		}
	}
}

func movePair(a, b *Node, dx, dy float64) {
	switch {
	case a.Pinned && b.Pinned:
		return
	case a.Pinned:
		b.Pos.X -= dx * 2
		b.Pos.Y -= dy * 2
	case b.Pinned:
		a.Pos.X += dx * 2
		a.Pos.Y += dy * 2
	default:
		a.Pos.X += dx
		a.Pos.Y += dy
		b.Pos.X -= dx
		b.Pos.Y -= dy
	}
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func componentBounds(g *Graph, comp []int) Rect {
	r := g.Nodes[comp[0]].Bounds()
	for _, i := range comp[1:] {
		b := g.Nodes[i].Bounds()
		r.Min.X = math.Min(r.Min.X, b.Min.X)
		r.Min.Y = math.Min(r.Min.Y, b.Min.Y)
		r.Max.X = math.Max(r.Max.X, b.Max.X)
		r.Max.Y = math.Max(r.Max.Y, b.Max.Y)
	}
	return r
}

// packComponents lays components out in rows, largest first.
//
// A schema's unreferenced tables form dozens of single-node components. Left to
// the force simulation they would be flung to the periphery by repulsion with
// nothing pulling them back; packing gives them a tidy shelf instead.
func packComponents(g *Graph, comps [][]int, boxes []Rect, opt LayoutOptions) {
	// Widen the packing row to fit the largest component. A fixed width that
	// the biggest cluster exceeds forces it onto a row of its own and leaves
	// the diagram far taller than it needs to be.
	width := opt.Width
	for _, b := range boxes {
		if w := b.Width() + opt.ComponentGap*2; w > width {
			width = w
		}
	}
	opt.Width = width

	x, y, rowH := 0.0, 0.0, 0.0

	for ci, comp := range comps {
		b := boxes[ci]
		w, h := b.Width(), b.Height()

		if x > 0 && x+w > opt.Width {
			x = 0
			y += rowH + opt.ComponentGap
			rowH = 0
		}

		dx, dy := x-b.Min.X, y-b.Min.Y
		for _, i := range comp {
			if !g.Nodes[i].Pinned {
				g.Nodes[i].Pos.X += dx
				g.Nodes[i].Pos.Y += dy
			}
		}

		x += w + opt.ComponentGap
		if h > rowH {
			rowH = h
		}
	}
}
