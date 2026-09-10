package chart

import (
	"image/color"
	"math/rand"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	fcanvas "fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
)

// T0.58 — image render versus native canvas drawing.
//
// Both approaches are measured through the SAME capture path, with an empty
// window captured as a floor and subtracted. Spike W1 showed that 63% of a
// software capture can be the rasteriser itself rather than the content, so an
// unsubtracted capture number would compare two things that are mostly the
// same constant.

const plotW, plotH = 1200, 700

var markColor = color.NRGBA{R: 0x00, G: 0x63, B: 0xE1, A: 0x40}

func capturePlot(tb testing.TB, content fyne.CanvasObject, frames int) time.Duration {
	tb.Helper()
	w := test.NewTempWindow(tb, content)
	w.Resize(fyne.NewSize(plotW, plotH))
	c := w.Canvas()
	_ = c.Capture()

	start := time.Now()
	for i := 0; i < frames; i++ {
		_ = c.Capture()
	}
	return time.Since(start) / time.Duration(frames)
}

func TestRenderApproach(t *testing.T) {
	if testing.Short() {
		t.Skip("render comparison skipped in -short")
	}
	test.NewTempApp(t)

	floor := capturePlot(t, fcanvas.NewRectangle(color.White), 5)
	t.Logf("empty-window floor: %v", floor.Round(time.Microsecond))
	t.Logf("")
	t.Logf("%-8s %-22s %-22s %s", "points", "raster (draw+capture)", "native (build+capture)", "native objects")

	r := rand.New(rand.NewSource(1))
	xs := Scale{Min: 0, Max: 1, Pixels: plotW}
	ys := Scale{Min: 0, Max: 1, Pixels: plotH, Invert: true}

	var nativeAt10k time.Duration

	for _, n := range []int{1_000, 10_000, 100_000} {
		pts := make([]Point, n)
		for i := range pts {
			pts[i] = Point{X: r.Float64(), Y: r.Float64(), Index: i}
		}

		// Raster: one canvas.Image, whatever the point count.
		drawStart := time.Now()
		img := NewCanvas(plotW, plotH)
		RasterScatter(img, pts, xs, ys, markColor, 1)
		draw := time.Since(drawStart)
		ri := fcanvas.NewImageFromImage(img)
		ri.FillMode = fcanvas.ImageFillStretch
		rasterCap := capturePlot(t, ri, 3)
		raster := draw + (rasterCap - floor)

		// Native: one canvas.Circle per point. At 100k this is expected to be
		// prohibitive; if 10k already exceeded two seconds, extrapolate rather
		// than spend minutes proving it.
		var nativeCell string
		if n == 100_000 && nativeAt10k > 2*time.Second {
			est := nativeAt10k * 10
			nativeCell = "~" + est.Round(time.Second).String() + " (extrapolated)"
		} else {
			buildStart := time.Now()
			objs := make([]fyne.CanvasObject, n)
			for i, p := range pts {
				c := fcanvas.NewCircle(markColor)
				x, y := float32(xs.Project(p.X)), float32(ys.Project(p.Y))
				c.Move(fyne.NewPos(x-1, y-1))
				c.Resize(fyne.NewSize(2, 2))
				objs[i] = c
			}
			cont := container.NewWithoutLayout(objs...)
			build := time.Since(buildStart)
			nativeCap := capturePlot(t, cont, 1)
			native := build + (nativeCap - floor)
			if n == 10_000 {
				nativeAt10k = native
			}
			nativeCell = native.Round(time.Millisecond).String()
		}

		t.Logf("%-8d %-22v %-22s %d", n, raster.Round(time.Millisecond), nativeCell, n)
	}

	// Sanity bound on the approach ADR-0004 selects: rasterising 100k points
	// must not itself threaten opening a chart.
	pts := make([]Point, 100_000)
	for i := range pts {
		pts[i] = Point{X: r.Float64(), Y: r.Float64(), Index: i}
	}
	start := time.Now()
	RasterScatter(NewCanvas(plotW, plotH), pts, xs, ys, markColor, 1)
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Errorf("rasterising 100k points took %v; exceeds 100ms", d)
	}
}

func BenchmarkRasterScatter100k(b *testing.B) {
	r := rand.New(rand.NewSource(1))
	pts := make([]Point, 100_000)
	for i := range pts {
		pts[i] = Point{X: r.Float64(), Y: r.Float64(), Index: i}
	}
	xs := Scale{Min: 0, Max: 1, Pixels: plotW}
	ys := Scale{Min: 0, Max: 1, Pixels: plotH, Invert: true}
	img := NewCanvas(plotW, plotH)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RasterScatter(img, pts, xs, ys, markColor, 1)
	}
}

func BenchmarkRasterLineLTTB(b *testing.B) {
	pts := series(100_000, 7)
	xs := NiceScale(0, float64(len(pts)-1), plotW, false)
	ys := NiceScale(minY(pts), maxY(pts), plotH, true)
	img := NewCanvas(plotW, plotH)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		RasterLine(img, LTTB(pts, plotW*2), xs, ys, markColor)
	}
}
