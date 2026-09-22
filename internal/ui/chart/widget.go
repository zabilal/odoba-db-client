package chart

import (
	"errors"
	"image/color"

	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/scene"
)

// A chart on the screen (FR-11.1).
//
// The widget holds a chart and the size it was last given, and draws what
// Draw says to draw. It decides nothing about the picture itself, which is
// what lets a PNG and an SVG of the same chart be the same picture.

// Widget draws a chart.
type Widget struct {
	widget.BaseWidget

	chart Chart
	frame Frame
	err   error

	// probe answers what is under the pointer, rebuilt whenever the
	// drawing changes and asked on every movement (probe.go).
	probe   *Probe
	reading held

	// OnPick is called with what a click was on, which is what a click
	// filters the result to (FR-11.4).
	OnPick func(Reading)

	renderer *chartRenderer

	// minSize overrides the natural minimum, which is how an export asks
	// for a picture of a particular size.
	minSize fyne.Size
}

// New makes a widget for a chart.
func New(c Chart) *Widget {
	w := &Widget{chart: c}
	w.ExtendBaseWidget(w)
	return w
}

// SetChart draws something else.
//
// What was being pointed at goes with it: a tooltip about a series that is
// no longer drawn is a tooltip about nothing.
func (w *Widget) SetChart(c Chart) {
	w.chart, w.reading = c, held{}
	w.Refresh()
}

// Chart is what is being drawn.
func (w *Widget) Chart() Chart { return w.chart }

// Frame is where its parts are, at the size it was last drawn.
//
// A hover reads this to turn a cursor position into a value, which is why
// the frame is kept rather than thrown away after drawing.
func (w *Widget) Frame() Frame { return w.frame }

// Err is why the chart could not be drawn, if it could not be.
func (w *Widget) Err() error { return w.err }

func (w *Widget) CreateRenderer() fyne.WidgetRenderer {
	w.ExtendBaseWidget(w)
	w.renderer = &chartRenderer{w: w, bg: fcanvas.NewRectangle(w.chart.Background)}
	return w.renderer
}

type chartRenderer struct {
	w    *Widget
	bg   *fcanvas.Rectangle
	size fyne.Size

	// base is the background and the scene, which is the expensive part:
	// it is rebuilt when the chart or the size changes and not when the
	// pointer moves. objects is base with the tooltip after it.
	base    []fyne.CanvasObject
	objects []fyne.CanvasObject

	// tip is the tooltip's parts, so that following the pointer moves them
	// rather than building them again (hover.go).
	tip tip
}

func (r *chartRenderer) Layout(size fyne.Size) {
	r.size = size
	r.bg.Resize(size)
	r.bg.Move(fyne.NewPos(0, 0))
	r.build()
}

func (r *chartRenderer) MinSize() fyne.Size {
	if !r.w.minSize.IsZero() {
		return r.w.minSize
	}
	// Smaller than this and the gutters are most of the picture.
	return fyne.NewSize(240, 160)
}

func (r *chartRenderer) Refresh() {
	r.bg.FillColor = r.w.chart.Background
	r.bg.Refresh()
	r.build()
	fcanvas.Refresh(r.w)
}

func (r *chartRenderer) Objects() []fyne.CanvasObject { return r.objects }

func (r *chartRenderer) Destroy() {}

func (r *chartRenderer) build() {
	size := r.size
	if size.IsZero() {
		size = r.MinSize()
	}
	f, err := Layout(r.w.chart, int(size.Width), int(size.Height))
	r.w.frame, r.w.err = f, err
	if err != nil {
		// Why it cannot be drawn, in the words the kind gave: a chart that
		// silently showed nothing would be read as a chart of nothing.
		r.w.probe, r.w.reading = nil, held{}
		r.base = append([]fyne.CanvasObject{r.bg}, note(err, size, r.w.chart.Axis)...)
		r.retip()
		return
	}
	r.w.probe = NewProbe(r.w.chart, f)
	r.base = append([]fyne.CanvasObject{r.bg}, scene.Objects(Draw(r.w.chart, f))...)
	r.retip()
}

// retip rebuilds only the tooltip, leaving the drawing alone.
//
// A pointer moving across a chart of a hundred thousand points must not
// rasterise them again: what changed is a few words and a ring, and the
// picture under them has not.
func (r *chartRenderer) retip() {
	r.tip = tip{}
	r.objects = append(r.base[:len(r.base):len(r.base)], r.tipObjects()...)
}

// note is the message shown in place of a chart that cannot be drawn.
//
// Both halves of it: why, and what to do instead. A refusal without a way
// forward leaves somebody guessing at which of seven kinds might work.
func note(err error, size fyne.Size, fill color.NRGBA) []fyne.CanvasObject {
	lines := []string{err.Error()}
	var u *Unplottable
	if errors.As(err, &u) {
		lines = []string{u.Why}
		if u.Advice != "" {
			lines = append(lines, u.Advice)
		}
	}
	var out []fyne.CanvasObject
	h := float32(len(lines)) * (TickTextSize + 4)
	y := size.Height/2 - h/2
	for _, line := range lines {
		t := fcanvas.NewText(line, fill)
		t.TextSize = TitleTextSize
		t.Alignment = fyne.TextAlignCenter
		t.Resize(fyne.NewSize(size.Width, TitleTextSize+4))
		t.Move(fyne.NewPos(0, y))
		out = append(out, t)
		y += TickTextSize + 4
	}
	return out
}
