package diagram

import (
	"strconv"

	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// What a diagram is made of, said once (FR-8.3, FR-8.4).
//
// A diagram is drawn three ways: as Fyne objects on a screen, as pixels in a
// PNG, and as text in an SVG. Saying it three times would mean three
// drawings that could drift, and an exported picture that differs from the
// one somebody exported it from is worse than none.
//
// So the drawing is said here, in screen coordinates, and each of the three
// turns these into its own shapes. Nothing in this file imports Fyne.

// The vocabulary itself lives in internal/ui/scene, which is where the Fyne
// renderer and the SVG writer read it from. It is named here so that a
// diagram's shapes read as a diagram's shapes.
type (
	// Shape is one thing to draw.
	Shape = scene.Shape
	// Box is a node's rectangle.
	Box = scene.Box
	// Line is a segment of an edge, a hairline under a header, or part of
	// a cardinality mark.
	Line = scene.Line
	// Text is a label.
	Text = scene.Text
	// Dot is the mark beside a column the primary key is made of.
	Dot = scene.Dot
	// Scene is everything worth drawing, in the order it is drawn.
	Scene = scene.Scene
)

// Draw says what to draw of a graph, seen through a viewport.
//
// Only what the viewport says is visible is drawn, and only as finely as the
// zoom makes legible: that is the same rule on a screen and in a file, so an
// exported overview is the overview somebody exported.
func Draw(g *canvas.Graph, view *canvas.Viewport, pal theme.Palette, selected string) Scene {
	s := Scene{W: view.Screen.W, H: view.Screen.H, Background: pal.ContentBackground}
	visible := view.VisibleNodes(g, nil)
	edges := view.VisibleEdges(g, visible, nil)
	detail := view.Detail()

	// Edges first, so a box is drawn over the lines that reach it.
	for _, i := range edges {
		s.Shapes = append(s.Shapes, edgeShapes(g, view, pal, g.Edges[i], detail)...)
	}
	for _, i := range visible {
		s.Shapes = append(s.Shapes, nodeShapes(view, pal, g.Nodes[i], detail, selected)...)
	}
	return s
}

// nodeShapes is one box and what is written in it.
func nodeShapes(view *canvas.Viewport, pal theme.Palette, n canvas.Node,
	detail canvas.DetailLevel, selected string) []Shape {
	at := view.ToScreen(n.Pos)
	z := view.Zoom
	box := Box{X: at.X, Y: at.Y, W: n.Size.W * z, H: n.Size.H * z,
		Fill: pal.ElevatedBackground, Stroke: pal.ControlBorder, StrokeWidth: 1, Radius: 4}
	if n.ID == selected {
		box.Stroke, box.StrokeWidth = pal.ControlAccent, 2
	}
	out := []Shape{box}
	if detail == canvas.DetailBox {
		// Too far out to read anything. The box alone still carries the
		// shape of the schema, which is what an overview is for.
		return out
	}

	out = append(out, Text{X: at.X + textInset, Y: at.Y + textInset, S: n.Title,
		Size: headerTextSize * z, Fill: pal.Label, Bold: true})
	if detail != canvas.DetailFull {
		return out
	}

	// A hairline under the header, so a table's name is not read as its
	// first column.
	ruleY := at.Y + canvas.NodeHeaderH*z
	out = append(out, Line{X1: at.X, Y1: ruleY, X2: at.X + n.Size.W*z, Y2: ruleY,
		Stroke: pal.Separator, Width: 1})

	y := ruleY + canvas.NodePaddingY*z
	for i, p := range n.Ports {
		if i >= canvas.NodeMaxPorts {
			// A node taller than this is unreadable and would make the
			// diagram taller than it is wide. What is left is counted.
			out = append(out, Text{X: at.X + keyGutter*z, Y: y,
				S:    "+" + strconv.Itoa(len(n.Ports)-i) + " more",
				Size: portTextSize * z, Fill: pal.TertiaryLabel})
			break
		}
		if p.Key {
			// A key is marked rather than only emboldened: weight alone is
			// not a difference somebody can see at a glance down a column
			// of names, and colour alone is not one everybody can see.
			r := keyMarkSize * z / 2
			out = append(out, Dot{X: at.X + textInset + r, Y: y + portTextSize*z/2, R: r,
				Fill: pal.AccentText})
		}
		out = append(out, Text{X: at.X + keyGutter*z, Y: y, S: p.Label,
			Size: portTextSize * z, Fill: pal.Label, Bold: p.Key})
		if p.Detail != "" {
			out = append(out, Text{X: at.X + n.Size.W*z - textInset, Y: y, S: p.Detail,
				Size: detailTextSize * z, Fill: pal.TertiaryLabel, Align: scene.Trailing})
		}
		y += canvas.NodePortH * z
	}
	return out
}

// edgeShapes is one relationship: its segments, and a mark at each end.
func edgeShapes(g *canvas.Graph, view *canvas.Viewport, pal theme.Palette,
	e canvas.Edge, detail canvas.DetailLevel) []Shape {
	from, to := g.NodeByID(e.From), g.NodeByID(e.To)
	if from == nil || to == nil {
		return nil
	}
	route := canvas.RouteEdge(*from, *to, e.FromPort, e.ToPort)
	var out []Shape
	for i := 1; i < len(route.Points); i++ {
		a := view.ToScreen(route.Points[i-1])
		b := view.ToScreen(route.Points[i])
		out = append(out, Line{X1: a.X, Y1: a.Y, X2: b.X, Y2: b.Y,
			Stroke: pal.OpaqueSeparator, Width: 1})
	}
	if len(route.Points) < 2 || detail == canvas.DetailBox {
		// Too far out for a mark to be anything but a smudge.
		return out
	}
	// The child's end says how many rows it may have, which is what the
	// catalogue was read for. The parent's end is always one, because a
	// foreign key points at a single row.
	out = append(out, markShapes(view, pal, route.Points[0], route.FromSide, childEnd(e))...)
	out = append(out, markShapes(view, pal, route.Points[len(route.Points)-1], route.ToSide, singleEnd)...)
	return out
}

// end is what a relationship allows at one of its ends.
type end uint8

const (
	manyEnd end = iota
	singleEnd
)

// childEnd is what the child's end of a relationship allows.
func childEnd(e canvas.Edge) end {
	if e.Cardinality == canvas.OneToOne {
		return singleEnd
	}
	return manyEnd
}

// markShapes draws the crow's foot or the bar at one end of a relationship.
//
// A crow's foot for many and a bar for one is the convention every diagram
// of this kind uses, and a convention somebody already knows beats a legend
// they have to read.
func markShapes(view *canvas.Viewport, pal theme.Palette, at canvas.Point,
	side canvas.Side, kind end) []Shape {
	p := view.ToScreen(at)
	size := markerSize * view.Zoom
	// The mark is drawn back along the line, into the gap between the box
	// and the first corner.
	dx := size
	if side == canvas.SideRight {
		dx = -size
	}
	if kind == singleEnd {
		// A bar across the line: exactly one.
		return []Shape{Line{X1: p.X + dx, Y1: p.Y - size*0.6, X2: p.X + dx, Y2: p.Y + size*0.6,
			Stroke: pal.OpaqueSeparator, Width: 1}}
	}
	// Three lines fanning back from the end: many.
	var out []Shape
	for _, dy := range []float64{-size * 0.6, 0, size * 0.6} {
		out = append(out, Line{X1: p.X, Y1: p.Y, X2: p.X + dx, Y2: p.Y + dy,
			Stroke: pal.OpaqueSeparator, Width: 1})
	}
	return out
}

// Text sizes in graph units, scaled by the zoom so a box reads the same
// whatever size it is drawn at.
const (
	headerTextSize = 12.0
	portTextSize   = 10.0
	detailTextSize = 9.0
	textInset      = 5

	// keyGutter is the room left at the left of a row for the key mark, so
	// that every column's name starts at the same place whether or not it
	// is part of the key.
	keyGutter   = 12.0
	keyMarkSize = 5.0

	// markerSize is how far back along a line a cardinality mark reaches.
	markerSize = 7.0
)
