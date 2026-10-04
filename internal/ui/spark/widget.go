package spark

import (
	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
)

// A sparkline on the screen.
//
// It decides nothing about the picture, which is what lets the same picture be
// drawn and measured: the widget holds a Spark and the size it was last given,
// and draws what Draw says to draw.

// Widget draws a sparkline.
type Widget struct {
	widget.BaseWidget

	s    Spark
	size fyne.Size
}

// New makes a widget for a sparkline.
func New(s Spark) *Widget {
	w := &Widget{s: s}
	w.ExtendBaseWidget(w)
	return w
}

// SetSpark draws something else. It is how a window shows what has arrived
// since the last time it drew.
func (w *Widget) SetSpark(s Spark) {
	w.s = s
	w.Refresh()
}

// Spark is what it is drawing.
func (w *Widget) Spark() Spark { return w.s }

// MinSize is room for a shape and no more: a sparkline sits on a line of
// controls, so it asks for as little as it can be read at.
func (w *Widget) MinSize() fyne.Size {
	w.ExtendBaseWidget(w)
	return fyne.NewSize(96, 18)
}

func (w *Widget) CreateRenderer() fyne.WidgetRenderer {
	w.ExtendBaseWidget(w)
	return &sparkRenderer{w: w, bg: fcanvas.NewRectangle(w.s.Background)}
}

type sparkRenderer struct {
	w    *Widget
	bg   *fcanvas.Rectangle
	size fyne.Size

	objects []fyne.CanvasObject
}

func (r *sparkRenderer) Layout(size fyne.Size) {
	r.size = size
	r.w.size = size
	r.bg.Resize(size)
	r.rebuild()
}

func (r *sparkRenderer) MinSize() fyne.Size { return r.w.MinSize() }

func (r *sparkRenderer) Refresh() {
	r.bg.FillColor = r.w.s.Background
	r.bg.Refresh()
	r.rebuild()
	fcanvas.Refresh(r.w)
}

// rebuild draws the sparkline at the size it has.
func (r *sparkRenderer) rebuild() {
	w, h := float64(r.size.Width), float64(r.size.Height)
	if w < 1 || h < 1 {
		r.objects = []fyne.CanvasObject{r.bg}
		return
	}
	r.objects = append([]fyne.CanvasObject{r.bg}, scene.Objects(Draw(r.w.s, w, h))...)
}

func (r *sparkRenderer) Objects() []fyne.CanvasObject {
	if r.objects == nil {
		r.rebuild()
	}
	return r.objects
}

func (r *sparkRenderer) Destroy() {}
