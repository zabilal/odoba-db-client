package diagram

import (
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"

	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
)

// Drawing only what is on screen.
//
// The renderer keeps its objects between frames and reuses them, because a
// pan that allocated a rectangle per node per frame would be a pan that
// stutters. What changes between frames is which nodes are visible and where
// they are, not how many objects exist.

type renderer struct {
	w  *Widget
	bg *fcanvas.Rectangle

	// objects is what Fyne draws, rebuilt when what is visible changes.
	objects []fyne.CanvasObject

	// visible and edges are reused between frames so that panning allocates
	// nothing.
	visible []int
	edges   []int
}

func (r *renderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.bg.Move(fyne.NewPos(0, 0))
	r.build()
}

func (r *renderer) MinSize() fyne.Size { return fyne.NewSize(120, 90) }

func (r *renderer) Refresh() {
	r.bg.FillColor = r.w.palette.ContentBackground
	r.bg.Refresh()
	r.build()
	fcanvas.Refresh(r.w)
}

func (r *renderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *renderer) Destroy() {}

// build draws what the viewport says is worth drawing.
func (r *renderer) build() {
	w := r.w
	r.visible = w.view.VisibleNodes(w.graph, r.visible[:0])
	r.edges = w.view.VisibleEdges(w.graph, r.visible, r.edges[:0])
	detail := w.view.Detail()

	objs := []fyne.CanvasObject{r.bg}
	// Edges first, so a box is drawn over the lines that reach it rather
	// than under them.
	for _, i := range r.edges {
		objs = append(objs, r.edge(w.graph.Edges[i])...)
	}
	for _, i := range r.visible {
		objs = append(objs, r.node(w.graph.Nodes[i], detail)...)
	}
	r.objects = objs
}

// node draws one box: its outline, its title, and its columns where the zoom
// is close enough to read them.
func (r *renderer) node(n canvas.Node, detail canvas.DetailLevel) []fyne.CanvasObject {
	w := r.w
	at := w.view.ToScreen(n.Pos)
	size := fyne.NewSize(float32(n.Size.W*w.view.Zoom), float32(n.Size.H*w.view.Zoom))

	box := fcanvas.NewRectangle(w.palette.ElevatedBackground)
	box.StrokeColor = w.palette.ControlBorder
	box.StrokeWidth = 1
	box.CornerRadius = 4
	if n.ID == w.selected {
		box.StrokeColor = w.palette.ControlAccent
		box.StrokeWidth = 2
	}
	box.Move(fyne.NewPos(float32(at.X), float32(at.Y)))
	box.Resize(size)
	out := []fyne.CanvasObject{box}

	if detail == canvas.DetailBox {
		// Too far out to read anything. The box alone still carries the
		// shape of the schema, which is what an overview is for.
		return out
	}

	header := fcanvas.NewText(n.Title, w.palette.Label)
	header.TextStyle = fyne.TextStyle{Bold: true}
	header.TextSize = float32(headerTextSize * w.view.Zoom)
	header.Move(fyne.NewPos(float32(at.X)+textInset, float32(at.Y)+textInset))
	out = append(out, header)

	if detail != canvas.DetailFull {
		return out
	}
	// A hairline under the header, so a table's name is not read as its
	// first column.
	rule := fcanvas.NewLine(w.palette.Separator)
	rule.StrokeWidth = 1
	ruleY := float32(at.Y + canvas.NodeHeaderH*w.view.Zoom)
	rule.Position1 = fyne.NewPos(float32(at.X), ruleY)
	rule.Position2 = fyne.NewPos(float32(at.X)+size.Width, ruleY)
	out = append(out, rule)

	y := at.Y + canvas.NodeHeaderH*w.view.Zoom + canvas.NodePaddingY*w.view.Zoom
	for i, p := range n.Ports {
		if i >= canvas.NodeMaxPorts {
			// A node taller than this is unreadable and would make the
			// diagram taller than it is wide. What is left is counted.
			out = append(out, r.text("+"+strconv.Itoa(len(n.Ports)-i)+" more",
				w.palette.TertiaryLabel, at.X+keyGutter*w.view.Zoom, y, false))
			break
		}
		if p.Key {
			// A key is marked rather than only emboldened: weight alone is
			// not a difference somebody can see at a glance down a column
			// of names, and colour alone is not one everybody can see.
			out = append(out, r.keyMark(at.X, y))
		}
		out = append(out, r.text(p.Label, w.palette.Label, at.X+keyGutter*w.view.Zoom, y, p.Key))
		if p.Detail != "" {
			out = append(out, r.detail(p.Detail, at.X+n.Size.W*w.view.Zoom, y))
		}
		y += canvas.NodePortH * w.view.Zoom
	}
	return out
}

// keyMark is the diamond drawn beside a column the primary key is made of.
func (r *renderer) keyMark(x, y float64) *fcanvas.Circle {
	z := r.w.view.Zoom
	side := float32(keyMarkSize * z)
	c := fcanvas.NewCircle(r.w.palette.AccentText)
	c.Move(fyne.NewPos(float32(x)+textInset, float32(y)+float32(portTextSize*z)/2-side/2))
	c.Resize(fyne.NewSize(side, side))
	return c
}

// detail is a column's type, drawn to the right of its name in the quieter
// of the two label colours: it is there to be read when looked for, not to
// compete with the name.
func (r *renderer) detail(s string, right, y float64) *fcanvas.Text {
	t := fcanvas.NewText(s, r.w.palette.TertiaryLabel)
	t.TextSize = float32(detailTextSize * r.w.view.Zoom)
	t.Alignment = fyne.TextAlignTrailing
	t.Move(fyne.NewPos(float32(right)-textInset-t.MinSize().Width, float32(y)))
	return t
}

// text draws one row inside a box.
func (r *renderer) text(s string, c color.Color, x, y float64, bold bool) *fcanvas.Text {
	t := fcanvas.NewText(s, c)
	t.TextSize = float32(portTextSize * r.w.view.Zoom)
	t.TextStyle = fyne.TextStyle{Bold: bold}
	t.Move(fyne.NewPos(float32(x)+textInset, float32(y)))
	return t
}

// edge draws one relationship as the segments the router chose.
func (r *renderer) edge(e canvas.Edge) []fyne.CanvasObject {
	w := r.w
	from, to := w.graph.NodeByID(e.From), w.graph.NodeByID(e.To)
	if from == nil || to == nil {
		return nil
	}
	route := canvas.RouteEdge(*from, *to, e.FromPort, e.ToPort)
	var out []fyne.CanvasObject
	for i := 1; i < len(route.Points); i++ {
		a := w.view.ToScreen(route.Points[i-1])
		b := w.view.ToScreen(route.Points[i])
		out = append(out, r.line(a, b, w.palette.OpaqueSeparator))
	}
	if len(route.Points) < 2 || w.view.Detail() == canvas.DetailBox {
		// Too far out for a marker to be anything but a smudge.
		return out
	}
	// The child's end says how many rows it may have, which is what the
	// catalogue was read for: many for an ordinary key, one where the
	// child's own side is unique. The parent's end is always one, because a
	// foreign key points at a single row.
	out = append(out, r.marker(route.Points[0], route.FromSide, childEnd(e))...)
	out = append(out, r.marker(route.Points[len(route.Points)-1], route.ToSide, singleEnd)...)
	return out
}

// childEnd is what the child's end of a relationship allows.
func childEnd(e canvas.Edge) end {
	if e.Cardinality == canvas.OneToOne {
		return singleEnd
	}
	return manyEnd
}

// end is what a relationship allows at one of its ends.
type end uint8

const (
	manyEnd end = iota
	singleEnd
)

// marker draws the crow's foot or the bar at one end of a relationship.
//
// A crow's foot for many and a bar for one is the convention every diagram
// of this kind uses, and a convention somebody already knows beats a legend
// they have to read.
func (r *renderer) marker(at canvas.Point, side canvas.Side, kind end) []fyne.CanvasObject {
	w := r.w
	p := w.view.ToScreen(at)
	size := markerSize * w.view.Zoom
	// The mark is drawn back along the line, into the gap between the box
	// and the first corner.
	dx := size
	if side == canvas.SideRight {
		dx = -size
	}
	c := w.palette.OpaqueSeparator

	if kind == singleEnd {
		// A bar across the line: exactly one.
		return []fyne.CanvasObject{r.line(
			canvas.Point{X: p.X + dx, Y: p.Y - size*0.6},
			canvas.Point{X: p.X + dx, Y: p.Y + size*0.6}, c)}
	}
	// Three lines fanning back from the end: many.
	var out []fyne.CanvasObject
	for _, dy := range []float64{-size * 0.6, 0, size * 0.6} {
		out = append(out, r.line(
			canvas.Point{X: p.X, Y: p.Y},
			canvas.Point{X: p.X + dx, Y: p.Y + dy}, c))
	}
	return out
}

// line draws one segment between two points already in screen space.
func (r *renderer) line(a, b canvas.Point, c color.Color) *fcanvas.Line {
	l := fcanvas.NewLine(c)
	l.StrokeWidth = 1
	l.Position1 = fyne.NewPos(float32(a.X), float32(a.Y))
	l.Position2 = fyne.NewPos(float32(b.X), float32(b.Y))
	return l
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
