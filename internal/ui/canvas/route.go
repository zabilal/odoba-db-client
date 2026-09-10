package canvas

import "math"

// Edge routing (T0.52).
//
// Orthogonal rather than straight or curved. In a schema diagram many edges run
// between the same pair of regions, and straight lines at arbitrary angles
// become an unreadable fan. Right angles let the eye follow one relationship
// across a crowded diagram, which is the entire purpose of the picture.

// Side of a node box that an edge attaches to.
type Side uint8

const (
	SideLeft Side = iota
	SideRight
	SideTop
	SideBottom
)

// Route is a polyline in graph space, plus the sides it leaves and arrives on
// so the renderer can orient the cardinality markers.
type Route struct {
	Points   []Point
	FromSide Side
	ToSide   Side
}

// portY returns the vertical centre of a port row within a node, or the node's
// own centre when the port index is out of range.
func portY(n Node, port int) float64 {
	if port < 0 || port >= len(n.Ports) || port >= NodeMaxPorts {
		return n.Pos.Y + n.Size.H/2
	}
	return n.Pos.Y + NodeHeaderH + NodePaddingY + float64(port)*NodePortH + NodePortH/2
}

// RouteEdge computes an orthogonal route between two nodes.
//
// Edges leave and enter on the left or right side, because a schema node is
// wider than it is tall and its rows are horizontal — attaching to the top or
// bottom would cross the very columns the edge refers to.
func RouteEdge(from, to Node, fromPort, toPort int) Route {
	fy := portY(from, fromPort)
	ty := portY(to, toPort)

	fromRight := from.Pos.X + from.Size.W
	toRight := to.Pos.X + to.Size.W

	// Choose sides by which arrangement gives the shorter horizontal run.
	var fx, tx float64
	var fs, ts Side
	if math.Abs(to.Pos.X-fromRight) <= math.Abs(from.Pos.X-toRight) {
		fx, fs = fromRight, SideRight
		tx, ts = to.Pos.X, SideLeft
	} else {
		fx, fs = from.Pos.X, SideLeft
		tx, ts = toRight, SideRight
	}

	const stub = 18.0 // straight run before the first turn

	sx := fx
	if fs == SideRight {
		sx += stub
	} else {
		sx -= stub
	}
	ex := tx
	if ts == SideRight {
		ex += stub
	} else {
		ex -= stub
	}

	pts := []Point{{fx, fy}, {sx, fy}}

	if math.Abs(fy-ty) < 0.5 {
		// Same row: a single straight run reads better than a jog.
		pts = append(pts, Point{ex, ty}, Point{tx, ty})
		return Route{Points: pts, FromSide: fs, ToSide: ts}
	}

	// Turn at the midpoint between the two stubs, so parallel edges between
	// the same pair of nodes stay visually distinct.
	mid := (sx + ex) / 2
	pts = append(pts,
		Point{mid, fy},
		Point{mid, ty},
		Point{ex, ty},
		Point{tx, ty},
	)
	return Route{Points: pts, FromSide: fs, ToSide: ts}
}

// Length returns the route's total length, used to prefer shorter routes when
// several are possible.
func (r Route) Length() float64 {
	var d float64
	for i := 1; i < len(r.Points); i++ {
		d += math.Hypot(r.Points[i].X-r.Points[i-1].X, r.Points[i].Y-r.Points[i-1].Y)
	}
	return d
}
