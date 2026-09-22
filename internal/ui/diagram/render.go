package diagram

import (
	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"

	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
)

// Turning a scene into Fyne objects.
//
// The renderer draws what Draw says to draw and decides nothing itself: what
// is visible, how finely, and in what colours are all settled before it is
// called. That is what makes an exported picture the same picture — the PNG
// is this widget rendered, and the SVG is the same scene written out.

type renderer struct {
	w  *Widget
	bg *fcanvas.Rectangle

	// objects is what Fyne draws, rebuilt when what is visible changes.
	objects []fyne.CanvasObject
}

func (r *renderer) Layout(size fyne.Size) {
	r.bg.Resize(size)
	r.bg.Move(fyne.NewPos(0, 0))
	r.build()
}

func (r *renderer) MinSize() fyne.Size {
	if r.w.minSize.IsZero() {
		return fyne.NewSize(120, 90)
	}
	return r.w.minSize
}

func (r *renderer) Refresh() {
	r.bg.FillColor = r.w.palette.ContentBackground
	r.bg.Refresh()
	r.build()
	fcanvas.Refresh(r.w)
}

func (r *renderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *renderer) Destroy() {}

func (r *renderer) build() {
	s := Draw(r.w.graph, r.w.view, r.w.palette, r.w.selected)
	r.objects = append([]fyne.CanvasObject{r.bg}, scene.Objects(s)...)
}
