package chart

import (
	"bytes"
	"fmt"
	"image/png"
	"strings"
	"testing"

	ftheme "fyne.io/fyne/v2/theme"
)

// Taking a chart away: two files of one picture.

func exported(t *testing.T, c Chart, w, h int) (*bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var asPNG, asSVG bytes.Buffer
	if err := PNG(&asPNG, c, w, h, ftheme.DefaultTheme()); err != nil {
		t.Fatalf("PNG: %v", err)
	}
	if err := SVG(&asSVG, c, w, h); err != nil {
		t.Fatalf("SVG: %v", err)
	}
	return &asPNG, &asSVG
}

// A PNG of the size that was asked for, not of the window.
func TestAPNGIsTheSizeItWasAskedFor(t *testing.T) {
	asPNG, _ := exported(t, plain(Line, oneSeries("v", 1, 5, 3)), 640, 360)
	img, err := png.Decode(asPNG)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 640 || img.Bounds().Dy() != 360 {
		t.Errorf("the picture is %v, want 640x360", img.Bounds())
	}
}

// The frame stays text, so a number in an exported chart can be searched for
// and copied.
func TestAnSVGWritesTheFrameAsText(t *testing.T) {
	c := plain(Line, oneSeries("orders", 1, 5, 3), oneSeries("refunds", 2, 1, 4))
	c.XTitle, c.YTitle = "day", "count"
	f, err := Layout(c, 640, 360)
	if err != nil {
		t.Fatal(err)
	}
	_, asSVG := exported(t, c, 640, 360)
	got := asSVG.String()
	for _, want := range []string{">day<", ">count<", ">orders<", ">refunds<"} {
		if !strings.Contains(got, want) {
			t.Errorf("the file does not contain %s", want)
		}
	}
	for _, tick := range f.YTicks {
		if !strings.Contains(got, ">"+tick.Label+"<") {
			t.Errorf("the tick %q was not written as text", tick.Label)
		}
	}
	if n := strings.Count(got, "<image "); n != 1 {
		t.Errorf("%d embedded images, want one layer of marks", n)
	}
	if !strings.Contains(got, "data:image/png;base64,") {
		t.Error("the marks are not in the file; it would open empty")
	}
}

// The two files are one picture: whatever the PNG shows, the SVG says.
func TestBothFormatsDescribeTheSameFrame(t *testing.T) {
	c := plain(Bar, oneSeries("v", 4, 9, 2))
	c.Labels = []string{"Mon", "Tue", "Wed"}
	f, err := Layout(c, ExportWidth, ExportHeight)
	if err != nil {
		t.Fatal(err)
	}
	_, asSVG := exported(t, c, ExportWidth, ExportHeight)
	got := asSVG.String()
	// The SVG's plot is the frame's plot, to the pixel.
	want := imageAt(f)
	if !strings.Contains(got, want) {
		t.Errorf("the SVG's marks are not at %s", want)
	}
	for _, label := range c.Labels {
		if !strings.Contains(got, ">"+label+"<") {
			t.Errorf("the category %q is missing", label)
		}
	}
}

func imageAt(f Frame) string {
	return fmt.Sprintf(`<image x="%d.00" y="%d.00" width="%d.00" height="%d.00"`,
		f.Plot.Min.X, f.Plot.Min.Y, f.Plot.Dx(), f.Plot.Dy())
}

// An export refuses what cannot be drawn rather than writing a file of
// nothing, which somebody would find out about when they opened it.
func TestAnExportOfWhatCannotBeDrawnIsRefused(t *testing.T) {
	bad := plain(Pie, oneSeries("v", 1, -1))
	var buf bytes.Buffer
	if err := PNG(&buf, bad, 400, 300, ftheme.DefaultTheme()); err == nil {
		t.Error("a pie of a negative value was exported")
	}
	if buf.Len() != 0 {
		t.Errorf("%d bytes were written for a chart that cannot be drawn", buf.Len())
	}
	if err := SVG(&buf, bad, 400, 300); err == nil {
		t.Error("a pie of a negative value was written as SVG")
	}
	if buf.Len() != 0 {
		t.Errorf("%d bytes were written for a chart that cannot be drawn", buf.Len())
	}
}

// A size nobody gave is the default, and one nobody can open is cut down to
// what can be.
func TestAnAskedForSizeIsBoundedByWhatCanBeDrawn(t *testing.T) {
	w, h := bounded(0, 0)
	if w != ExportWidth || h != ExportHeight {
		t.Errorf("bounded(0,0) = %dx%d, want the default %dx%d", w, h, ExportWidth, ExportHeight)
	}
	if w, h := bounded(99_999, 99_999); w != ExportLimit || h != ExportLimit {
		t.Errorf("bounded of a huge size = %dx%d, want %d on each side", w, h, ExportLimit)
	}
	if w, h := bounded(800, 600); w != 800 || h != 600 {
		t.Errorf("bounded(800,600) = %dx%d, want it left alone", w, h)
	}
}

// A picture twice the size is drawn at twice the detail rather than scaled
// up, which is how a crisp file is had.
func TestALargerPictureIsDrawnLargerNotScaled(t *testing.T) {
	c := plain(Line, oneSeries("v", 1, 5, 3))
	small, err := Layout(c, 400, 300)
	if err != nil {
		t.Fatal(err)
	}
	large, err := Layout(c, 1600, 1200)
	if err != nil {
		t.Fatal(err)
	}
	if len(large.YTicks) <= len(small.YTicks) {
		t.Errorf("%d ticks at 1200px and %d at 300px; a taller axis carries more",
			len(large.YTicks), len(small.YTicks))
	}
	if large.Plot.Dx() <= 3*small.Plot.Dx() {
		t.Errorf("plot %d wide at 1600 and %d at 400; the marks are drawn larger",
			large.Plot.Dx(), small.Plot.Dx())
	}
}
