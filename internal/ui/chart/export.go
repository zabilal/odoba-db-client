package chart

import (
	"fmt"
	"image/png"
	"io"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/software"

	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
)

// Taking a chart away (FR-11.3).
//
// Both formats are laid out at the size that was asked for rather than at
// whatever size the window happens to be, so an exported chart is a picture
// somebody chose the shape of. Both read the same scene, so the PNG and the
// SVG of one chart are one picture in two files.
//
// The SVG keeps the frame as text and embeds the marks as an image. That is
// the honest answer to what an SVG should do with a layer that is pixels on
// purpose (ADR-0004): a hundred thousand scatter points written as a hundred
// thousand circles is a file nothing will open, and a chart whose axis
// labels could not be searched for would lose what an SVG is for.

// ExportSize is the size an export is drawn at when none is given.
//
// Wide enough for a legible axis and small enough to put in a document.
const (
	ExportWidth  = 960
	ExportHeight = 540

	// ExportLimit is the largest side an exported picture is given. Past
	// this a PNG is tens of megabytes and the marks are drawn pixel by
	// pixel, which is slow enough to look like a hang.
	ExportLimit = 8000
)

// PNG writes a chart as an image.
//
// It is a widget rendered off-screen rather than a second drawing, so the
// file is what the window shows. A size larger than the window's is how a
// crisp file is had: everything in the picture is worked out from the size,
// so a chart at twice the size is drawn at twice the detail rather than
// scaled up.
func PNG(out io.Writer, c Chart, w, h int, t fyne.Theme) error {
	w, h = bounded(w, h)
	if _, err := Layout(c, w, h); err != nil {
		return err
	}
	shot := New(c)
	// software.Render captures at the object's minimum size, so the picture
	// is asked for by saying that the widget is that big.
	shot.minSize = fyne.NewSize(float32(w), float32(h))
	img := software.Render(shot, t)
	if img == nil {
		return fmt.Errorf("chart: the picture came back empty")
	}
	return png.Encode(out, img)
}

// SVG writes a chart as text: the frame as text and lines, the marks as an
// image inside it.
func SVG(out io.Writer, c Chart, w, h int) error {
	w, h = bounded(w, h)
	f, err := Layout(c, w, h)
	if err != nil {
		return err
	}
	return scene.WriteSVG(out, Draw(c, f))
}

// bounded keeps an asked-for size within what can be drawn.
func bounded(w, h int) (int, int) {
	if w <= 0 {
		w = ExportWidth
	}
	if h <= 0 {
		h = ExportHeight
	}
	return min(w, ExportLimit), min(h, ExportLimit)
}
