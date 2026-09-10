package canvas

import "math"

// Viewport maps graph space to screen space, and decides what is worth drawing.
//
// Two mechanisms make a 200-table schema pannable, and neither is about drawing
// faster:
//
//   - Culling. Only nodes intersecting the visible rectangle become canvas
//     objects. Pan cost then depends on what is on screen, not on schema size
//     — the same principle as the data grid (NFR-P13).
//   - Level of detail. Below a zoom where column names are unreadable, they are
//     not drawn. A whole-schema overview is exactly the view where every node
//     is visible, so without this, culling saves nothing at the one zoom level
//     that needs it most.
type Viewport struct {
	// Pan is the graph-space point displayed at the screen origin.
	Pan Point

	// Zoom is screen pixels per graph unit.
	Zoom float64

	// Screen is the widget's size in pixels.
	Screen Size
}

// Zoom limits. Below MinZoom nothing is legible even as a shape; above MaxZoom
// the diagram is a wall of text with no structure visible.
const (
	MinZoom = 0.08
	MaxZoom = 3.0
)

// NewViewport returns an identity viewport.
func NewViewport(screen Size) *Viewport {
	return &Viewport{Zoom: 1, Screen: screen}
}

// ToScreen converts a graph-space point to screen space.
func (v *Viewport) ToScreen(p Point) Point {
	return Point{(p.X - v.Pan.X) * v.Zoom, (p.Y - v.Pan.Y) * v.Zoom}
}

// ToGraph converts a screen-space point to graph space.
func (v *Viewport) ToGraph(p Point) Point {
	return Point{p.X/v.Zoom + v.Pan.X, p.Y/v.Zoom + v.Pan.Y}
}

// VisibleRect returns the graph-space rectangle currently on screen.
func (v *Viewport) VisibleRect() Rect {
	return Rect{
		Min: v.ToGraph(Point{0, 0}),
		Max: v.ToGraph(Point{v.Screen.W, v.Screen.H}),
	}
}

// PanBy scrolls by a screen-space delta.
func (v *Viewport) PanBy(dx, dy float64) {
	v.Pan.X -= dx / v.Zoom
	v.Pan.Y -= dy / v.Zoom
}

// ZoomAt scales by a factor, keeping the graph point under a screen position
// fixed. Zooming toward the cursor rather than the centre is what makes a zoom
// gesture feel like it is under the user's control.
func (v *Viewport) ZoomAt(screen Point, factor float64) {
	before := v.ToGraph(screen)

	v.Zoom = clampFloat(v.Zoom*factor, MinZoom, MaxZoom)

	after := v.ToGraph(screen)
	v.Pan.X += before.X - after.X
	v.Pan.Y += before.Y - after.Y
}

// FitTo frames a rectangle with padding, used by "fit to window".
func (v *Viewport) FitTo(r Rect, padding float64) {
	w, h := r.Width(), r.Height()
	if w <= 0 || h <= 0 || v.Screen.W <= 0 || v.Screen.H <= 0 {
		return
	}
	zx := (v.Screen.W - 2*padding) / w
	zy := (v.Screen.H - 2*padding) / h
	v.Zoom = clampFloat(math.Min(zx, zy), MinZoom, MaxZoom)

	c := r.Center()
	v.Pan = Point{
		X: c.X - v.Screen.W/(2*v.Zoom),
		Y: c.Y - v.Screen.H/(2*v.Zoom),
	}
}

// DetailLevel is how much of a node is worth drawing at the current zoom.
type DetailLevel uint8

const (
	// DetailBox draws a filled rectangle only. Below this zoom a title is
	// sub-pixel and reads as noise.
	DetailBox DetailLevel = iota
	// DetailTitle draws the header with the table name.
	DetailTitle
	// DetailFull draws the header and every column row.
	DetailFull
)

// Zoom thresholds, chosen from the theme's type scale: below roughly 8px
// effective text height a label is not readable, so drawing it is pure cost.
const (
	titleZoomThreshold = 0.30
	fullZoomThreshold  = 0.62
)

// Detail returns what to draw at the current zoom.
func (v *Viewport) Detail() DetailLevel {
	switch {
	case v.Zoom >= fullZoomThreshold:
		return DetailFull
	case v.Zoom >= titleZoomThreshold:
		return DetailTitle
	default:
		return DetailBox
	}
}

// VisibleNodes returns the indices of nodes intersecting the viewport.
//
// The margin keeps a node that is partly off-screen from popping in as it
// scrolls into view.
func (v *Viewport) VisibleNodes(g *Graph, out []int) []int {
	out = out[:0]
	vis := v.VisibleRect().Expand(NodeWidth)
	for i := range g.Nodes {
		if g.Nodes[i].Bounds().Intersects(vis) {
			out = append(out, i)
		}
	}
	return out
}

// VisibleEdges returns the indices of edges with at least one visible endpoint.
//
// An edge whose endpoints are both off-screen may still cross the viewport, but
// drawing those costs more than it adds: a line arriving from nowhere and
// leaving to nowhere carries no information the user can act on.
func (v *Viewport) VisibleEdges(g *Graph, visibleNodes []int, out []int) []int {
	out = out[:0]
	if len(visibleNodes) == 0 {
		return out
	}
	set := make(map[string]bool, len(visibleNodes))
	for _, i := range visibleNodes {
		set[g.Nodes[i].ID] = true
	}
	for i, e := range g.Edges {
		if set[e.From] || set[e.To] {
			out = append(out, i)
		}
	}
	return out
}

// HitTest returns the topmost node at a screen position, or -1.
//
// Iterates in reverse so the node drawn last — visually on top — wins, which
// is what a user expects when boxes overlap.
func (v *Viewport) HitTest(g *Graph, screen Point) int {
	p := v.ToGraph(screen)
	for i := len(g.Nodes) - 1; i >= 0; i-- {
		if g.Nodes[i].Bounds().Contains(p) {
			return i
		}
	}
	return -1
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
