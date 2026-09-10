package grid

import (
	"context"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// Control experiment for spike W1.
//
// Fyne's test driver rasterises in software, pixel by pixel on the CPU, with
// no GPU. A capture time measured through it is therefore NOT a valid proxy
// for the GL renderer that ships. Before drawing any conclusion from a capture
// number, this establishes how much of it is the rasteriser itself.
func TestRasterBaselineIsolatesTheRenderer(t *testing.T) {
	if testing.Short() {
		t.Skip("rendering baseline skipped in -short")
	}
	test.NewTempApp(t)

	const w, h = 1440, 960
	measure := func(name string, content fyne.CanvasObject) time.Duration {
		win := test.NewTempWindow(t, content)
		win.Resize(fyne.NewSize(w, h))
		c := win.Canvas()
		_ = c.Capture()

		const n = 15
		var total time.Duration
		for i := 0; i < n; i++ {
			start := time.Now()
			_ = c.Capture()
			total += time.Since(start)
		}
		d := total / n
		t.Logf("%-28s %v", name, d.Round(time.Microsecond))
		return d
	}

	// A single filled rectangle: the floor cost of rasterising the window.
	blank := measure("empty window", canvas.NewRectangle(theme.Light.ContentBackground))

	// One text object, to price glyph rendering separately from layout.
	single := measure("one text label", widget.NewLabel("SELECT * FROM orders"))

	// The real grid.
	m := NewModel(NewSyntheticFetcher(10_000_000))
	ctx := context.Background()
	for i := int64(0); i < 8192; i += PageSize {
		m.Row(ctx, i)
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.Stats().ResidentPages < 32 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	g := NewTableGrid(ctx, m, theme.Light)
	full := measure("full grid", g.Table)

	cells := VisibleRows(h) * len(m.Columns())
	t.Logf("")
	t.Logf("grid drew ~%d cells", cells)
	t.Logf("cost attributable to the grid: %v (%.1f%% of the frame)",
		(full - blank).Round(time.Microsecond),
		100*float64(full-blank)/float64(full))
	t.Logf("per-cell rasterisation cost: %v",
		(time.Duration(float64(full-blank) / float64(cells))).Round(time.Nanosecond))
	_ = single

	if blank > full {
		t.Fatal("baseline exceeded the grid; measurement is unreliable")
	}
	// The conclusion this test exists to support: if the empty window already
	// consumes most of the frame, the capture number measures Fyne's software
	// rasteriser, not the grid, and cannot gate a GPU-composited application.
	if share := float64(blank) / float64(full); share > 0.5 {
		t.Logf("NOTE: %.0f%% of the capture is the software rasteriser itself; "+
			"this number cannot gate GL performance", 100*share)
	}
}
