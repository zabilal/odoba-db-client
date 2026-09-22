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
	y := at.Y + canvas.NodeHeaderH*w.view.Zoom
	for i, p := range n.Ports {
		if i >= canvas.NodeMaxPorts {
			out = append(out, r.text("+"+strconv.Itoa(len(n.Ports)-i)+" more",
				w.palette.TertiaryLabel, at.X, y, false))
			break
		}
		out = append(out, r.text(p.Label, w.palette.Label, at.X, y, p.Key))
		y += canvas.NodePortH * w.view.Zoom
	}
	return out
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
		line := fcanvas.NewLine(w.palette.OpaqueSeparator)
		line.StrokeWidth = 1
		line.Position1 = fyne.NewPos(float32(a.X), float32(a.Y))
		line.Position2 = fyne.NewPos(float32(b.X), float32(b.Y))
		out = append(out, line)
	}
	return out
}

// Text sizes in graph units, scaled by the zoom so a box reads the same
// whatever size it is drawn at.
const (
	headerTextSize = 12.0
	portTextSize   = 10.0
	textInset      = 5
)
