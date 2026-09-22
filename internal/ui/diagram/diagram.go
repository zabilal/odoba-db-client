// Package diagram draws a node canvas in a window: pan, zoom and drag, with
// the arrangement kept (FR-8.2).
//
// Everything about where a box goes and what a line does between two of them
// belongs to internal/ui/canvas, which has no Fyne in it and is benchmarked
// on its own (ADR-0007). This is the part that has to know about a mouse and
// a screen, and it is deliberately thin: it converts events into calls on a
// viewport and a graph, and draws what the viewport says is worth drawing.
//
// Only what is on screen becomes a Fyne object. That is the whole of why a
// two-hundred-table schema pans at all, and it is the same rule the data
// grid follows (NFR-P13).
package diagram

import (
	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// Widget draws a graph and lets somebody move about in it.
type Widget struct {
	widget.BaseWidget

	graph   *canvas.Graph
	view    *canvas.Viewport
	palette theme.Palette

	// OnMoved is called when a node has been dragged, so whatever owns the
	// diagram can keep the arrangement. It is called once a drag ends, not
	// on every step of one.
	OnMoved func()
	// OnSelect is called when a node is chosen, with its ID, or with "" when
	// the choice is dropped.
	OnSelect func(id string)

	// dragging is the node under the pointer during a drag, or -1 while the
	// background is being panned.
	dragging int
	dragged  bool
	last     fyne.Position
	selected string

	// minSize is what the widget claims to need, which is how an exported
	// picture asks to be drawn at the size of the whole diagram. Zero on a
	// widget in a window, which takes whatever room it is given.
	minSize fyne.Size

	renderer *renderer
}

// New builds a widget over a graph, drawn in these colours.
//
// The palette is given rather than looked up, as the grid's cells do it: a
// widget that reached for the current theme could not be drawn twice in two
// appearances, and could not be tested without an application.
func New(g *canvas.Graph, p theme.Palette) *Widget {
	w := &Widget{graph: g, palette: p, view: canvas.NewViewport(canvas.Size{W: 800, H: 600}), dragging: -1}
	w.ExtendBaseWidget(w)
	return w
}

// Recolour draws the diagram in another palette, for a change of appearance.
func (w *Widget) Recolour(p theme.Palette) {
	w.palette = p
	w.Refresh()
}

// Graph is what is being drawn.
func (w *Widget) Graph() *canvas.Graph { return w.graph }

// View is where the diagram is being looked at from.
func (w *Widget) View() *canvas.Viewport { return w.view }

// Selected is the node chosen, or "".
func (w *Widget) Selected() string { return w.selected }

// Fit puts the whole diagram on screen, which is where one starts.
func (w *Widget) Fit() {
	w.view.FitTo(w.graph.Bounds(), fitPadding)
	w.Refresh()
}

// fitPadding leaves a margin round a fitted diagram so its outermost boxes
// are not against the edge of the window.
const fitPadding = 40

// Resize tells the viewport how big the screen is, which is what decides
// what is visible.
func (w *Widget) Resize(size fyne.Size) {
	w.view.Screen = canvas.Size{W: float64(size.Width), H: float64(size.Height)}
	w.BaseWidget.Resize(size)
}

// Dragged pans the view, or moves the node a drag started on.
func (w *Widget) Dragged(e *fyne.DragEvent) {
	if w.dragging == -2 {
		return // a drag that began outside anything this widget owns
	}
	if w.dragging == -1 && !w.dragged {
		// The first step of a drag decides what is being dragged: a node if
		// one is under where it began, otherwise the view.
		w.dragging = w.view.HitTest(w.graph, canvas.Point{
			X: float64(e.Position.X - e.Dragged.DX), Y: float64(e.Position.Y - e.Dragged.DY)})
	}
	w.dragged = true
	if w.dragging < 0 {
		w.view.PanBy(float64(e.Dragged.DX), float64(e.Dragged.DY))
		w.Refresh()
		return
	}
	// A node moves by as much as the pointer did, in graph units: dragging
	// at a low zoom must move a box as far as the hand went, not further.
	n := &w.graph.Nodes[w.dragging]
	n.Pos.X += float64(e.Dragged.DX) / w.view.Zoom
	n.Pos.Y += float64(e.Dragged.DY) / w.view.Zoom
	n.Pinned = true
	w.Refresh()
}

// DragEnd finishes a drag, and says so once rather than per step.
//
// A node is only ever being dragged because Dragged decided so on the first
// step, so having dragged at all is not asked again here.
func (w *Widget) DragEnd() {
	moved := w.dragging >= 0
	w.dragging, w.dragged = -1, false
	if moved && w.OnMoved != nil {
		w.OnMoved()
	}
}

// Tapped chooses the node under the pointer, or drops the choice.
func (w *Widget) Tapped(e *fyne.PointEvent) {
	at := w.view.HitTest(w.graph, canvas.Point{X: float64(e.Position.X), Y: float64(e.Position.Y)})
	id := ""
	if at >= 0 {
		id = w.graph.Nodes[at].ID
	}
	if id == w.selected {
		return
	}
	w.selected = id
	w.Refresh()
	if w.OnSelect != nil {
		w.OnSelect(id)
	}
}

// Scrolled zooms about the pointer, so that what is under it stays under it.
func (w *Widget) Scrolled(e *fyne.ScrollEvent) {
	w.view.ZoomAt(canvas.Point{X: float64(e.Position.X), Y: float64(e.Position.Y)},
		zoomFor(float64(e.Scrolled.DY)))
	w.Refresh()
}

// zoomFor turns a scroll into a zoom factor. A wheel notch is a fixed step
// so that zooming feels the same whatever a trackpad reports.
func zoomFor(dy float64) float64 {
	const step = 1.12
	switch {
	case dy > 0:
		return step
	case dy < 0:
		return 1 / step
	}
	return 1
}

// Zoom steps the zoom about the middle of the screen, for the buttons and
// the keyboard.
func (w *Widget) Zoom(in bool) {
	factor := 1.0 / 1.25
	if in {
		factor = 1.25
	}
	w.view.ZoomAt(canvas.Point{X: w.view.Screen.W / 2, Y: w.view.Screen.H / 2}, factor)
	w.Refresh()
}

// Cursor says the pointer is over something draggable.
func (w *Widget) Cursor() desktop.Cursor { return desktop.DefaultCursor }

// CreateRenderer builds the renderer, which owns every Fyne object drawn.
func (w *Widget) CreateRenderer() fyne.WidgetRenderer {
	w.renderer = &renderer{w: w, bg: fcanvas.NewRectangle(w.palette.ContentBackground)}
	return w.renderer
}
