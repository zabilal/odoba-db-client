package chart

import (
	"image"
	"image/color"
	"math"
	"strconv"
	"time"
)

// A whole chart: the frame it is drawn in, and the marks in it (FR-11.1).
//
// The frame is everything that is not data — the plot area, the two scales
// and their ticks. It is worked out once and handed to whatever draws: the
// window draws its text as Fyne labels, an SVG writes the same text as text,
// and both put the same raster of marks inside the same rectangle.
//
// That is the rule the diagram settled (ADR-0130): say the drawing once, so
// an exported picture cannot differ from the one it was exported from.

// Chart is everything needed to draw one.
type Chart struct {
	Kind   Kind
	Series []Series

	// Labels name the X positions where they are categories rather than
	// numbers, in the order the positions run.
	Labels []string

	// Times says the X is a moment rather than a number, which changes how
	// its ticks are written and nothing else.
	Times bool

	// XTitle and YTitle are the columns the axes came from.
	XTitle, YTitle string

	// Colours are the series' colours, taken in turn.
	Colours []color.NRGBA

	// Axis is the colour of the lines and the text of the frame.
	Axis color.NRGBA
	// Grid is the colour of the lines across the plot.
	Grid color.NRGBA
	// Background is what the chart sits on.
	Background color.NRGBA
}

// Frame is where a chart's parts go.
type Frame struct {
	// W and H are the size the frame was laid out for, which is the size of
	// the whole picture.
	W, H float64

	// Plot is the rectangle the marks are drawn in, inside the whole chart.
	Plot image.Rectangle

	// X and Y map data to pixels within Plot.
	X, Y Scale

	// XTicks and YTicks are the labelled positions, in Plot's coordinates.
	XTicks, YTicks []Tick

	// Bars is how wide a bar is, for the kinds drawn as bars.
	Bars float64

	// Bins is what a histogram counts into, worked out here so that the
	// axis and the bars cannot disagree about where an edge is.
	Bins []Bin
}

// Type sizes and the room the frame needs.
//
// These are the chart's own, not the theme's, because a chart is drawn at
// whatever size the window gives it and exported at whatever size was asked
// for, and its labels have to stay in proportion to the picture rather than
// to the window they came from.
const (
	TickTextSize   = 10.0
	TitleTextSize  = 11.0
	LegendTextSize = 10.0

	// tickGap is the room between an axis and its labels.
	tickGap = 4.0
	// legendRow is the height of one row of the key.
	legendRow = 15.0
	// legendSwatch is the size of the colour block beside a name.
	legendSwatch = 9.0
	// legendGap is the room between one key entry and the next.
	legendGap = 14.0
	// edge is the margin round the whole picture.
	edge = 6.0

	// charWidth is how wide one character is taken to be, as a fraction of
	// the type size.
	//
	// The frame is worked out without a font, because it is worked out the
	// same way for a window, a PNG and an SVG, and only one of those three
	// has a font to ask. This is a deliberate over-estimate: too wide leaves
	// a gap, too narrow lets two labels touch, and a gap is the better of
	// the two mistakes.
	charWidth = 0.62
)

// axisTitleGap is the room between the tick labels and the axis's name.
const axisTitleGap = 3.0

// Layout works out where everything goes, at a size.
//
// It answers an error rather than a frame where the kind cannot honestly
// draw the data, so that nothing is laid out for a chart that will not be
// drawn.
func Layout(c Chart, w, h int) (Frame, error) {
	if err := Check(c.Kind, c.Series); err != nil {
		return Frame{}, err
	}

	keys := Keys(c)
	top := edge
	if c.YTitle != "" && HasAxes(c.Kind) {
		top += TitleTextSize + axisTitleGap
	}
	top += float64(legendRows(keys, float64(w)-2*edge)) * legendRow

	if HasAxes(c.Kind) {
		// Half a line, so the topmost tick label — which is centred on the
		// top of the plot — is inside the picture rather than cut in two.
		top += TickTextSize / 2
	}

	bottom := edge
	if HasAxes(c.Kind) {
		bottom += TickTextSize + tickGap
		if c.XTitle != "" {
			bottom += TitleTextSize + axisTitleGap
		}
	}

	f := Frame{W: float64(w), H: float64(h)}
	plotH := float64(h) - top - bottom
	if plotH < 1 {
		return Frame{}, errNoRoom
	}

	lo, hi, err := vertical(c, &f)
	if err != nil {
		return Frame{}, err
	}
	f.Y = NiceScale(lo, hi, plotH, true)

	left, right := edge, edge
	if HasAxes(c.Kind) {
		f.YTicks = f.Y.Ticks(yTickTarget(int(plotH)), formatNumber)
		left += widest(f.YTicks, TickTextSize) + tickGap
		// Room for half of the last X label, which is centred on the axis's
		// end and would otherwise hang off the picture.
		right += TickTextSize * charWidth * 2
	}
	plotW := float64(w) - left - right
	if plotW < 1 {
		return Frame{}, errNoRoom
	}
	f.Plot = image.Rect(int(left), int(top), int(left+plotW), int(top+plotH))

	if !HasAxes(c.Kind) {
		return f, nil
	}
	if err := horizontal(c, &f, plotW); err != nil {
		return Frame{}, err
	}
	f.Bars = BarWidth(plotW, barCount(c))
	return f, nil
}

// errNoRoom is what a chart too small to draw answers.
var errNoRoom = &Unplottable{Why: "there is not enough room to draw a chart"}

// vertical is what the upright axis has to cover.
//
// For a histogram that is how many values fell in the fullest bin, which is
// not in the data at all until the bins are worked out; for everything else
// it is the data's own extent.
func vertical(c Chart, f *Frame) (lo, hi float64, err error) {
	if c.Kind != Histogram {
		b, err := Extent(c.Kind, c.Series)
		if err != nil {
			return 0, 0, err
		}
		return b.MinY, b.MaxY, nil
	}
	f.Bins = Bins(counted(c.Series[0]), binTarget(len(c.Series[0].Points)))
	if len(f.Bins) == 0 {
		return 0, 0, ErrNoData
	}
	tallest := 0
	for _, b := range f.Bins {
		tallest = max(tallest, b.Count)
	}
	return 0, float64(tallest), nil
}

// horizontal is the axis along the bottom, and what is written on it.
func horizontal(c Chart, f *Frame, plotW float64) error {
	if c.Kind == Histogram {
		// The bins' own edges are the axis: they are round numbers already,
		// and a scale that rounded them again would draw a bar that did not
		// start where its label says.
		f.X = Scale{Min: f.Bins[0].Min, Max: f.Bins[len(f.Bins)-1].Max, Pixels: plotW}
		f.XTicks = f.X.Ticks(xTickTarget(int(plotW)), formatNumber)
		return nil
	}
	b, err := Extent(c.Kind, c.Series)
	if err != nil {
		return err
	}
	if len(c.Labels) > 0 {
		// Categories sit at whole numbers, so the axis runs from before the
		// first to after the last: a bar standing on the edge is half drawn.
		f.X = Scale{Min: -0.5, Max: float64(len(c.Labels)) - 0.5, Pixels: plotW}
		f.XTicks = categoryTicks(f.X, c.Labels)
		return nil
	}
	f.X = NiceScale(b.MinX, b.MaxX, plotW, false)
	f.XTicks = f.X.Ticks(xTickTarget(int(plotW)), c.xFormat())
	return nil
}

// counted is the values a histogram counts, which are a series' Y: the X of
// a histogram's series is only which row each came from.
func counted(s Series) []float64 {
	out := make([]float64, 0, len(s.Points))
	for _, p := range s.Points {
		out = append(out, p.Y)
	}
	return out
}

// binTarget is how many bins to aim for, from how many values there are.
//
// The square root of the count, which is the ordinary rule, capped so that a
// million rows do not ask for a thousand bars. It deliberately does not
// depend on how wide the chart is: bins that changed as a window was resized
// would mean the same data told two different stories.
func binTarget(n int) int {
	return min(max(int(math.Sqrt(float64(n))), 1), 50)
}

// HasAxes says whether a kind is drawn against two scales. A pie is not: it
// is a whole divided up, and there is nothing for an axis to measure.
func HasAxes(k Kind) bool { return k != Pie }

// widest is how much room a set of tick labels needs.
func widest(ticks []Tick, size float64) float64 {
	n := 0
	for _, t := range ticks {
		n = max(n, len([]rune(t.Label)))
	}
	return float64(n) * size * charWidth
}

// legendRows is how many rows the key takes at a width.
func legendRows(keys []Key, width float64) int {
	if len(keys) == 0 {
		return 0
	}
	rows, x := 1, 0.0
	for _, k := range keys {
		w := keyWidth(k)
		if x > 0 && x+w > width {
			rows++
			x = 0
		}
		x += w + legendGap
	}
	return rows
}

// keyWidth is how much room one entry of the key needs.
func keyWidth(k Key) float64 {
	return legendSwatch + tickGap + float64(len([]rune(k.Name)))*LegendTextSize*charWidth
}

// barCount is how many bars stand across the axis, which is what decides how
// wide each is. Side by side they would be unreadable, so a grouped chart
// counts every bar of every series.
func barCount(c Chart) int {
	positions := map[float64]bool{}
	for _, s := range c.Series {
		for _, p := range s.Points {
			positions[p.X] = true
		}
	}
	n := len(positions)
	if c.Kind == Bar && len(c.Series) > 1 {
		n *= len(c.Series)
	}
	if n < 1 {
		n = 1
	}
	return n
}

// xTickTarget and yTickTarget are how many ticks fit without crowding: about
// one every eighty pixels across, and one every forty down, which is what
// the type scale leaves room for.
func xTickTarget(px int) int { return max(2, px/80) }
func yTickTarget(px int) int { return max(2, px/40) }

// categoryTicks labels each category at its own position, so a bar is
// labelled by what it is rather than by where it happens to stand.
func categoryTicks(s Scale, labels []string) []Tick {
	out := make([]Tick, 0, len(labels))
	for i, name := range labels {
		out = append(out, Tick{Value: float64(i), Pixel: s.Project(float64(i)), Label: name})
	}
	return out
}

// xFormat is how the bottom axis writes its numbers: as moments where the
// column was one, and as numbers otherwise.
func (c Chart) xFormat() func(float64) string {
	if !c.Times {
		return formatNumber
	}
	return func(v float64) string {
		return time.Unix(int64(v), 0).UTC().Format("2006-01-02 15:04")
	}
}

// formatNumber writes an axis number without the exponent a bare %g gives
// for ordinary magnitudes, and without trailing zeros.
func formatNumber(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return strconv.FormatFloat(v, 'g', 4, 64)
}

// Raster draws the marks of a chart into an image the size of its plot.
//
// Only the marks: the frame is lines and text, which the window and the SVG
// each draw in their own way. What comes back is positioned at the plot's
// top-left corner.
func Raster(c Chart, f Frame) *image.NRGBA {
	img := NewCanvas(f.Plot.Dx(), f.Plot.Dy())
	switch c.Kind {
	case Pie:
		r := math.Min(float64(f.Plot.Dx()), float64(f.Plot.Dy()))/2 - 4
		RasterPie(img, Wedges(c), float64(f.Plot.Dx())/2, float64(f.Plot.Dy())/2, r, c.Colours)

	case Histogram:
		RasterHistogram(img, f.Bins, f.X, f.Y, colourAt(c.Colours, 0))

	case StackedBar:
		base := map[float64]float64{}
		for i, s := range c.Series {
			RasterStack(img, s.Points, f.X, f.Y, colourAt(c.Colours, i), f.Bars, base)
		}

	case Bar:
		for i, s := range c.Series {
			RasterBars(img, offsetBars(s.Points, i, len(c.Series), f), f.X, f.Y,
				colourAt(c.Colours, i), f.Bars)
		}

	case Area:
		for i, s := range c.Series {
			RasterArea(img, forDrawing(Sorted(s).Points, f), f.X, f.Y, colourAt(c.Colours, i))
		}

	case Scatter:
		for i, s := range c.Series {
			RasterScatter(img, s.Points, f.X, f.Y, colourAt(c.Colours, i), 2)
		}

	default: // Line
		for i, s := range c.Series {
			RasterLine(img, forDrawing(Sorted(s).Points, f), f.X, f.Y, colourAt(c.Colours, i))
		}
	}
	return img
}

// forDrawing reduces a dense series to the pixel grid before it is drawn.
//
// Only for drawing: every hover and every click reads the whole series
// (downsample.go). Min-max buckets are used rather than LTTB because they
// cannot lose a spike — the picture is the true extent of what fell in each
// column, which is what a chart of a result set has to be.
func forDrawing(pts []Point, f Frame) []Point {
	columns := f.Plot.Dx()
	if len(pts) <= 2*columns {
		return pts
	}
	return MinMaxBuckets(pts, f.X, columns)
}

// offsetBars moves a series' bars aside so that several stand beside each
// other at one position rather than on top of one another.
func offsetBars(pts []Point, at, of int, f Frame) []Point {
	if of < 2 {
		return pts
	}
	// The whole group is centred on the position, so the position still
	// means what its label says.
	shift := (float64(at) - float64(of-1)/2) * f.Bars
	out := make([]Point, len(pts))
	for i, p := range pts {
		out[i] = Point{X: f.X.Unproject(f.X.Project(p.X) + shift), Y: p.Y, Index: p.Index}
	}
	return out
}

// Wedges is what a pie is divided into, named by the categories the rows
// came from.
func Wedges(c Chart) []Slice {
	if len(c.Series) == 0 {
		return nil
	}
	pts := c.Series[0].Points
	return Slices(c.Series[0], func(i int) string {
		if i < 0 || i >= len(pts) {
			return ""
		}
		// A point's X is which category it is, which is what names it. Its
		// place in the series is not: two rows can share a category.
		return labelAt(c.Labels, int(pts[i].X))
	})
}

// Key is one entry of a chart's key: a name and the colour it is drawn in.
type Key struct {
	Name   string
	Colour color.NRGBA
}

// Keys is what a chart's key says.
//
// For most kinds that is the series, because each is one colour. For a pie
// it is the wedges, because the whole chart is one series and the colours
// divide it up.
func Keys(c Chart) []Key {
	var names []string
	if c.Kind == Pie {
		for _, w := range Wedges(c) {
			names = append(names, w.Name)
		}
	} else {
		for _, s := range c.Series {
			names = append(names, s.Name)
		}
	}
	if len(names) < 2 {
		// One name is not a key: there is nothing to tell it apart from.
		return nil
	}
	out := make([]Key, 0, len(names))
	for i, n := range names {
		out = append(out, Key{Name: n, Colour: colourAt(c.Colours, i)})
	}
	return out
}

func labelAt(labels []string, i int) string {
	if i < 0 || i >= len(labels) {
		return strconv.Itoa(i)
	}
	return labels[i]
}
