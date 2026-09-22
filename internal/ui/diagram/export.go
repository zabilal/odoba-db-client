package diagram

import (
	"fmt"
	"image/png"
	"io"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/software"

	"github.com/ikigai-db/ikigai-db/internal/ui/canvas"
	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
)

// Taking a diagram away (FR-8.4).
//
// What is exported is the whole diagram, not what happens to be on screen.
// Somebody exporting a picture wants the picture, not their scroll position,
// and a file cropped to a window is a file they have to make again.
//
// Both formats are the same scene: a PNG is this widget rendered off-screen,
// and an SVG is the same shapes written as text, so the two cannot disagree
// about what a diagram looks like.

// ExportLimit is the largest side an exported image is given.
//
// A two-hundred-table schema at natural size is tens of thousands of pixels
// across, which is a file nothing will open. Past this it is drawn smaller,
// which is what somebody wanting the whole picture asked for anyway.
const ExportLimit = 8000

// exportPadding leaves a margin round an exported diagram, as fitting leaves
// one on screen.
const exportPadding = 24

// whole is a viewport showing all of a graph at a scale that fits the limit.
func whole(g *canvas.Graph, zoom float64) *canvas.Viewport {
	b := g.Bounds().Expand(exportPadding)
	w, h := b.Width()*zoom, b.Height()*zoom
	if m := max(w, h); m > ExportLimit {
		zoom *= ExportLimit / m
		w, h = b.Width()*zoom, b.Height()*zoom
	}
	v := canvas.NewViewport(canvas.Size{W: w, H: h})
	v.Zoom = zoom
	// Pan is the graph-space point shown at the screen origin, so putting
	// the diagram's own corner there is what puts all of it in the picture.
	v.Pan = b.Min
	return v
}

// Scene is the whole diagram at a scale, for either format to draw.
func (w *Widget) Scene(zoom float64) Scene {
	view := whole(w.graph, zoom)
	// Nothing is chosen in an exported picture: a selection is a thing
	// somebody is doing, not a thing about the schema.
	return Draw(w.graph, view, w.palette, "")
}

// PNG writes the whole diagram as an image.
//
// It is this widget rendered off-screen rather than a second drawing, so the
// file is what the window shows.
func (w *Widget) PNG(out io.Writer, zoom float64, t fyne.Theme) error {
	view := whole(w.graph, zoom)
	shot := New(w.graph, w.palette)
	shot.view = view
	// software.Render captures at the object's minimum size, so the picture
	// is asked for by saying that the widget is that big.
	shot.minSize = fyne.NewSize(float32(view.Screen.W), float32(view.Screen.H))
	img := software.Render(shot, t)
	if img == nil {
		return fmt.Errorf("diagram: the picture came back empty")
	}
	return png.Encode(out, img)
}

// SVG writes the whole diagram as text.
//
// Text is written as text rather than as outlines, so a name in an exported
// diagram can be searched for and copied, and the file stays small enough to
// put in a document.
func (w *Widget) SVG(out io.Writer, zoom float64) error {
	return scene.WriteSVG(out, w.Scene(zoom))
}
