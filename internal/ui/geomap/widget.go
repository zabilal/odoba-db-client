package geomap

import (
	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
)

// A map on the screen.
//
// The widget holds a map and the size it was last given, and draws what Draw
// says to draw. It decides nothing about the picture, which is what lets the
// picture be shown and exported as the same picture.

// Widget draws a map.
type Widget struct {
	widget.BaseWidget

	m     Map
	frame Frame

	// OnPick is called with what a click was on, which is what a click filters
	// the result to (FR-11.4).
	OnPick func(Reading)
	// OnHover is called as the pointer moves, with what is under it, so that
	// whatever is showing the coordinates can follow it.
	OnHover func(Reading)

	renderer *mapRenderer
}

// New makes a widget for a map.
func New(m Map) *Widget {
	w := &Widget{m: m}
	w.ExtendBaseWidget(w)
	return w
}

// SetMap draws something else.
func (w *Widget) SetMap(m Map) {
	w.m = m
	w.Refresh()
}

// Frame is where its parts are, at the size it was last drawn, which is what
// turns a cursor position into a coordinate.
func (w *Widget) Frame() Frame { return w.frame }

func (w *Widget) CreateRenderer() fyne.WidgetRenderer {
	w.ExtendBaseWidget(w)
	w.renderer = &mapRenderer{w: w, bg: fcanvas.NewRectangle(w.m.Background)}
	return w.renderer
}

// MinSize is enough room for the graticule and a little map inside it: smaller
// than this and the labels are all there is.
func (w *Widget) MinSize() fyne.Size {
	w.ExtendBaseWidget(w)
	return fyne.NewSize(240, 180)
}

// Tapped is a click: what it was on goes to whoever is listening.
func (w *Widget) Tapped(e *fyne.PointEvent) {
	if w.OnPick == nil {
		return
	}
	w.OnPick(w.read(e.Position))
}

// MouseIn, MouseMoved and MouseOut follow the pointer, for the line that says
// where it is. A map whose coordinates did not follow the pointer would be a
// picture rather than a reading.
func (w *Widget) MouseIn(e *desktop.MouseEvent) { w.MouseMoved(e) }

func (w *Widget) MouseMoved(e *desktop.MouseEvent) {
	if w.OnHover == nil {
		return
	}
	w.OnHover(w.read(e.Position))
}

func (w *Widget) MouseOut() {
	if w.OnHover != nil {
		w.OnHover(Reading{Row: -1})
	}
}

// read is what is under a position of this widget.
func (w *Widget) read(at fyne.Position) Reading {
	m := w.m
	m.Frame = w.frame
	return Read(m, float64(at.X), float64(at.Y))
}

type mapRenderer struct {
	w    *Widget
	bg   *fcanvas.Rectangle
	size fyne.Size

	objects []fyne.CanvasObject
}

func (r *mapRenderer) Layout(size fyne.Size) {
	r.size = size
	r.bg.Resize(size)
	r.rebuild()
}

func (r *mapRenderer) MinSize() fyne.Size { return r.w.MinSize() }

func (r *mapRenderer) Refresh() {
	r.bg.FillColor = r.w.m.Background
	r.bg.Refresh()
	r.rebuild()
	fcanvas.Refresh(r.w)
}

// rebuild draws the map at the size it has.
func (r *mapRenderer) rebuild() {
	w, h := float64(r.size.Width), float64(r.size.Height)
	if w < 1 || h < 1 {
		r.objects = []fyne.CanvasObject{r.bg}
		return
	}
	m := r.w.m
	m.Frame = Layout(m.Built, w, h)
	r.w.frame = m.Frame
	r.objects = append([]fyne.CanvasObject{r.bg}, scene.Objects(Draw(m))...)
}

func (r *mapRenderer) Objects() []fyne.CanvasObject {
	if r.objects == nil {
		r.rebuild()
	}
	return r.objects
}

func (r *mapRenderer) Destroy() {}

// The widget is tappable and follows the pointer; the compiler is asked to agree
// rather than a reviewer.
var (
	_ fyne.Tappable     = (*Widget)(nil)
	_ desktop.Hoverable = (*Widget)(nil)
)
