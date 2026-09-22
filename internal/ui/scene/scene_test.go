package scene

import (
	"bytes"
	"image"
	"image/color"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
)

// One description of a picture, read three ways. These tests are about the
// two readers here: the Fyne objects and the SVG.

var (
	black = color.NRGBA{A: 255}
	blue  = color.NRGBA{B: 0xEE, A: 255}
	faint = color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0x40}
)

func raster(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, blue)
	return img
}

// objectsOf draws a scene the way a widget would.
//
// A text's width is measured with the theme's font, which needs an app, so
// every test that asks where a label ended up starts one.
func objectsOf(t *testing.T, shapes ...Shape) []fyne.CanvasObject {
	t.Helper()
	test.NewTempApp(t)
	return Objects(Scene{Shapes: shapes, W: 100, H: 100, Background: black})
}

// Every shape is drawn, and nothing else is.
func TestEveryShapeBecomesOneObject(t *testing.T) {
	got := objectsOf(t,
		Box{X: 1, Y: 2, W: 3, H: 4, Fill: blue, Stroke: black, StrokeWidth: 2, Radius: 1},
		Line{X1: 1, Y1: 2, X2: 3, Y2: 4, Stroke: black, Width: 1},
		Dot{X: 10, Y: 20, R: 3, Fill: blue},
		Text{X: 5, Y: 6, S: "hello", Size: 11, Fill: black},
		Raster{X: 7, Y: 8, W: 9, H: 10, Img: raster(9, 10)},
	)
	if len(got) != 5 {
		t.Fatalf("%d objects for 5 shapes", len(got))
	}
	box, ok := got[0].(*fcanvas.Rectangle)
	if !ok {
		t.Fatalf("a box became %T", got[0])
	}
	if box.Position() != fyne.NewPos(1, 2) || box.Size() != fyne.NewSize(3, 4) {
		t.Errorf("box at %v size %v, want (1,2) 3x4", box.Position(), box.Size())
	}
	if box.StrokeWidth != 2 || box.CornerRadius != 1 || box.FillColor != color.Color(blue) {
		t.Errorf("box is %+v, want its own stroke, radius and fill", box)
	}
	line, ok := got[1].(*fcanvas.Line)
	if !ok {
		t.Fatalf("a line became %T", got[1])
	}
	if line.Position1 != fyne.NewPos(1, 2) || line.Position2 != fyne.NewPos(3, 4) {
		t.Errorf("line runs %v..%v", line.Position1, line.Position2)
	}
	dot, ok := got[2].(*fcanvas.Circle)
	if !ok {
		t.Fatalf("a dot became %T", got[2])
	}
	// A dot is given by its centre; Fyne wants its corner.
	if dot.Position() != fyne.NewPos(7, 17) || dot.Size() != fyne.NewSize(6, 6) {
		t.Errorf("dot at %v size %v, want its centre at (10,20)", dot.Position(), dot.Size())
	}
	img, ok := got[4].(*fcanvas.Image)
	if !ok {
		t.Fatalf("a raster became %T", got[4])
	}
	if img.Position() != fyne.NewPos(7, 8) || img.Size() != fyne.NewSize(9, 10) {
		t.Errorf("image at %v size %v", img.Position(), img.Size())
	}
	if img.FillMode != fcanvas.ImageFillStretch {
		t.Errorf("image fill is %v; a fitted image would move itself", img.FillMode)
	}
}

// Fyne draws text from its top-left corner, so anything else is measured and
// moved. The alignment is the whole point: a tick label that began at the
// axis would run across the plot.
func TestTextIsMovedToWhereItWasAskedFor(t *testing.T) {
	at := func(a Align, middle bool) *fcanvas.Text {
		got := objectsOf(t, Text{X: 50, Y: 40, S: "value", Size: 11, Fill: black, Align: a, Middle: middle})
		return got[0].(*fcanvas.Text)
	}
	lead := at(Leading, false)
	if lead.Position().X != 50 {
		t.Errorf("leading text starts at %v, want 50", lead.Position().X)
	}
	w := lead.MinSize().Width
	if w <= 0 {
		t.Fatal("the text measured as nothing wide")
	}
	if got := at(Center, false).Position().X; got != 50-w/2 {
		t.Errorf("centred text starts at %v, want %v", got, 50-w/2)
	}
	if got := at(Trailing, false).Position().X; got != 50-w {
		t.Errorf("trailing text starts at %v, want %v", got, 50-w)
	}
	if got := at(Leading, false).Position().Y; got != 40 {
		t.Errorf("text sits at %v, want its top at 40", got)
	}
	h := lead.MinSize().Height
	if got := at(Leading, true).Position().Y; got != 40-h/2 {
		t.Errorf("centred text sits at %v, want %v", got, 40-h/2)
	}
}

// Bold is a difference somebody can see without colour.
func TestBoldIsCarriedThrough(t *testing.T) {
	got := objectsOf(t, Text{X: 0, Y: 0, S: "title", Size: 11, Fill: black, Bold: true})
	if !got[0].(*fcanvas.Text).TextStyle.Bold {
		t.Error("a bold label came out light")
	}
}

func svgOf(t *testing.T, shapes ...Shape) string {
	t.Helper()
	var b bytes.Buffer
	if err := WriteSVG(&b, Scene{Shapes: shapes, W: 100, H: 50, Background: black}); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// An SVG is a document of the size of the scene, with its background.
func TestAnSVGIsTheSizeOfTheScene(t *testing.T) {
	got := svgOf(t, Line{X1: 0, Y1: 0, X2: 10, Y2: 10, Stroke: black, Width: 1})
	for _, want := range []string{`width="100"`, `height="50"`, `viewBox="0 0 100 50"`, `fill="#000000"`} {
		if !strings.Contains(got, want) {
			t.Errorf("the document does not contain %s:\n%s", want, got)
		}
	}
}

// Text is written as text, ended or centred by SVG's own anchor rather than
// by measuring, which needs no font at all.
func TestAnSVGTellsTheTextWhereToEnd(t *testing.T) {
	got := svgOf(t,
		Text{X: 10, Y: 20, S: "left", Size: 10, Fill: black},
		Text{X: 30, Y: 20, S: "mid", Size: 10, Fill: black, Align: Center},
		Text{X: 50, Y: 20, S: "right", Size: 10, Fill: black, Align: Trailing},
	)
	if strings.Contains(splitAt(got, ">left<"), "text-anchor") {
		t.Error("leading text was given an anchor it does not need")
	}
	if !strings.Contains(splitAt(got, ">mid<"), `text-anchor="middle"`) {
		t.Errorf("centred text was not anchored:\n%s", got)
	}
	if !strings.Contains(splitAt(got, ">right<"), `text-anchor="end"`) {
		t.Errorf("trailing text was not anchored:\n%s", got)
	}
}

// A scene's Y is the top of a line of text; SVG's is the baseline. The
// arithmetic is done here rather than left to dominant-baseline, which is
// not honoured everywhere a file might be opened.
func TestAnSVGPutsTextOnItsBaseline(t *testing.T) {
	top := svgOf(t, Text{X: 0, Y: 20, S: "top", Size: 10, Fill: black})
	if !strings.Contains(top, `y="30.00"`) {
		t.Errorf("text from the top was not lowered to a baseline:\n%s", top)
	}
	mid := svgOf(t, Text{X: 0, Y: 20, S: "mid", Size: 10, Fill: black, Middle: true})
	if strings.Contains(mid, "dominant-baseline") {
		t.Error("the file relies on dominant-baseline")
	}
	if !strings.Contains(mid, `y="23.60"`) {
		t.Errorf("middled text is not on its line:\n%s", mid)
	}
}

// Transparency goes in its own attribute: an eight-digit hex is SVG 2, and a
// viewer that did not read it would show a different picture.
func TestTransparencyIsItsOwnAttribute(t *testing.T) {
	got := svgOf(t, Line{X1: 0, Y1: 0, X2: 10, Y2: 0, Stroke: faint, Width: 1})
	if !strings.Contains(got, `stroke="#808080"`) {
		t.Errorf("the colour was not written in six digits:\n%s", got)
	}
	if !strings.Contains(got, `stroke-opacity="0.251"`) {
		t.Errorf("the transparency was lost:\n%s", got)
	}
	opaque := svgOf(t, Line{X1: 0, Y1: 0, X2: 10, Y2: 0, Stroke: black, Width: 1})
	if strings.Contains(opaque, "stroke-opacity") {
		t.Error("an opaque line was given an opacity it does not need")
	}
}

// A raster is embedded, so an exported picture is one file.
func TestARasterIsEmbeddedInTheDocument(t *testing.T) {
	got := svgOf(t, Raster{X: 1, Y: 2, W: 3, H: 4, Img: raster(3, 4)})
	if !strings.Contains(got, `<image x="1.00" y="2.00" width="3.00" height="4.00"`) {
		t.Errorf("the image is not where it was put:\n%s", got)
	}
	if !strings.Contains(got, `xlink:href="data:image/png;base64,`) {
		t.Errorf("the image is not in the file:\n%s", got)
	}
	if !strings.Contains(got, `xmlns:xlink=`) {
		t.Error("the document does not declare the namespace its own href uses")
	}
}

// A raster with nothing in it is an error, not a file that opens broken.
func TestARasterWithNoImageIsRefused(t *testing.T) {
	var b bytes.Buffer
	err := WriteSVG(&b, Scene{Shapes: []Shape{Raster{W: 3, H: 4}}, W: 10, H: 10})
	if err == nil {
		t.Fatal("a raster with no image was written")
	}
	if b.Len() != 0 {
		t.Errorf("%d bytes were written anyway", b.Len())
	}
}

// A label called <b> is a label called <b>, not a bold tag.
func TestMarkupInALabelIsWrittenAsText(t *testing.T) {
	got := svgOf(t, Text{X: 0, Y: 0, S: `a <b> & "c" 'd'`, Size: 10, Fill: black})
	if !strings.Contains(got, `a &lt;b&gt; &amp; &quot;c&quot; &apos;d&apos;`) {
		t.Errorf("the label was not escaped:\n%s", got)
	}
}

// Add is how a scene is built, and what it adds is drawn in the order given.
func TestAddKeepsTheOrderShapesWereGivenIn(t *testing.T) {
	var s Scene
	s.Add(Line{X1: 1}, Box{X: 2})
	s.Add(Dot{X: 3})
	if len(s.Shapes) != 3 {
		t.Fatalf("%d shapes", len(s.Shapes))
	}
	if _, ok := s.Shapes[0].(Line); !ok {
		t.Errorf("the first shape is %T, want the one added first", s.Shapes[0])
	}
	if _, ok := s.Shapes[2].(Dot); !ok {
		t.Errorf("the last shape is %T, want the one added last", s.Shapes[2])
	}
}

// splitAt is the rest of a document from a marker, so that a test can ask
// about one shape's attributes rather than the whole file's.
func splitAt(doc, marker string) string {
	i := strings.Index(doc, marker)
	if i < 0 {
		return ""
	}
	j := strings.LastIndex(doc[:i], "<")
	return doc[j:i]
}
