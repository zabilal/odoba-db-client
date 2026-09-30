package shell

import (
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
)

// Putting a diagram back as somebody arranged it (FR-8.2).
//
// Where an arrangement is kept is the application layer's (app.LayoutStore).
// Putting it back on a graph, and reading it off one, are the window's: they
// take the canvas's own types, and nothing below the window may name those —
// which is what ARCH-1 is for and what the linter found when it first ran.

// ApplyLayout puts a kept arrangement back on a freshly laid out graph.
//
// A node somebody moved is pinned, which is what keeps the next layout from
// undoing it. A kept position for a node the schema no longer has is ignored
// rather than an error: a table dropped is a position nobody needs.
func ApplyLayout(g *canvas.Graph, l localdb.DiagramLayout) {
	for id, at := range l.Moved {
		if n := g.NodeByID(id); n != nil {
			n.Pos = canvas.Point{X: at.X, Y: at.Y}
			n.Pinned = true
		}
	}
}

// LayoutOf is what to keep of a graph: the nodes somebody moved, and nothing
// else.
func LayoutOf(g *canvas.Graph, view *canvas.Viewport) localdb.DiagramLayout {
	out := localdb.DiagramLayout{}
	for _, n := range g.Nodes {
		if !n.Pinned {
			continue
		}
		if out.Moved == nil {
			out.Moved = map[string]localdb.NodePlace{}
		}
		out.Moved[n.ID] = localdb.NodePlace{X: n.Pos.X, Y: n.Pos.Y}
	}
	if view != nil {
		out.Pan = localdb.NodePlace{X: view.Pan.X, Y: view.Pan.Y}
		out.Zoom = view.Zoom
	}
	return out
}

// RestoreView puts the viewport back where it was left, or leaves it alone
// where nothing was kept.
//
// A zoom outside what the canvas allows is ignored rather than clamped: it
// means the file was written by something else, and guessing at what it
// meant is worse than showing the diagram whole.
func RestoreView(view *canvas.Viewport, l localdb.DiagramLayout) bool {
	if view == nil || l.Zoom < canvas.MinZoom || l.Zoom > canvas.MaxZoom {
		return false
	}
	view.Pan = canvas.Point{X: l.Pan.X, Y: l.Pan.Y}
	view.Zoom = l.Zoom
	return true
}
