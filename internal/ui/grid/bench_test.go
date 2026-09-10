package grid

import (
	"context"
	"strconv"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// Spike W1 benchmarks (TASKS.md T0.41–T0.42, RISK-1).
//
// Frame budgets from NFR-P4: 60 fps is 16.7 ms per frame, and the hard floor
// of 30 fps is 33.3 ms. Everything measured here is CPU work that happens
// BEFORE the GPU composites the frame, so a viewport update needs to land well
// inside the budget to leave room for compositing. The interactive harness in
// cmd/gridspike measures true end-to-end fps; these numbers are what CI can
// enforce (NFR-Q3).

// warmModel returns a model with the first n rows already resident, so the
// benchmarks measure rendering rather than fetching.
func warmModel(b *testing.B, rows, warm int64) *Model {
	b.Helper()
	m := NewModel(NewSyntheticFetcher(rows))
	ctx := context.Background()

	for i := int64(0); i < warm; i += PageSize {
		m.Row(ctx, i)
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.Stats().ResidentPages < int(warm/PageSize) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	return m
}

// BenchmarkFormatCell isolates the formatting cost — the part shared by every
// rendering approach, and therefore the floor on what any of them can achieve.
func BenchmarkFormatCell(b *testing.B) {
	f := NewSyntheticFetcher(1000)
	rows, _ := f.Fetch(context.Background(), 0, 256)
	cols := f.Columns()
	loc := time.UTC

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := rows[i%len(rows)]
		for c := range cols {
			_ = Format(r[c], cols[c], loc)
		}
	}
	b.ReportMetric(float64(len(cols)), "cells/op")
}

// BenchmarkUpdateViewport measures the real hot path: updating every visible
// cell once, as happens on each scroll frame.
func BenchmarkUpdateViewport(b *testing.B) {
	for _, vp := range []struct {
		name       string
		rows, cols int
	}{
		{"13x40_typical", 40, 13},
		{"13x60_tall", 60, 13},
		{"30x40_wide", 40, 30},
	} {
		b.Run(vp.name, func(b *testing.B) {
			test.NewTempApp(b)

			m := warmModel(b, 10_000_000, 4096)
			g := NewTableGrid(context.Background(), m, theme.Light)

			// Pre-create the cell objects, as widget.Table pools them.
			cells := make([]fyne.CanvasObject, vp.rows*vp.cols)
			for i := range cells {
				cells[i] = g.createCell()
			}

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				// Offset each iteration so cells genuinely change, defeating
				// the unchanged-value fast path. A benchmark that re-renders
				// identical content measures nothing.
				base := i % 1024
				for r := 0; r < vp.rows; r++ {
					for c := 0; c < vp.cols; c++ {
						id := widget.TableCellID{Row: base + r, Col: c % 13}
						g.UpdateCell(id, cells[r*vp.cols+c])
					}
				}
			}

			b.StopTimer()
			perFrame := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / 1e6
			b.ReportMetric(perFrame, "ms/frame")
			b.ReportMetric(float64(vp.rows*vp.cols), "cells/frame")
		})
	}
}

// BenchmarkUpdateViewportUnchanged measures a refresh where nothing changed —
// the common case when the table refreshes without scrolling. The
// unchanged-value guard in cellWidget.set should make this nearly free; if it
// does not, every incidental refresh costs a full frame.
func BenchmarkUpdateViewportUnchanged(b *testing.B) {
	test.NewTempApp(b)

	m := warmModel(b, 10_000_000, 2048)
	g := NewTableGrid(context.Background(), m, theme.Light)

	const rows, cols = 40, 13
	cells := make([]fyne.CanvasObject, rows*cols)
	for i := range cells {
		cells[i] = g.createCell()
	}
	// Prime them once so subsequent updates are no-ops.
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			g.UpdateCell(widget.TableCellID{Row: r, Col: c}, cells[r*cols+c])
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				g.UpdateCell(widget.TableCellID{Row: r, Col: c}, cells[r*cols+c])
			}
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/1e6, "ms/frame")
}

// BenchmarkTableRefresh drives the whole widget through Fyne's renderer,
// including layout. This is the closest headless proxy to a real frame.
func BenchmarkTableRefresh(b *testing.B) {
	test.NewTempApp(b)

	m := warmModel(b, 10_000_000, 4096)
	g := NewTableGrid(context.Background(), m, theme.Light)

	w := test.NewTempWindow(b, g.Table)
	w.Resize(fyne.NewSize(1440, 40*theme.RowHeight))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		g.Table.ScrollToOffset(fyne.NewPos(0, float32(i%2048)*theme.RowHeight))
		g.Table.Refresh()
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/1e6, "ms/frame")
}

// BenchmarkModelRowResident proves NFR-P13: the cost of reading a row must not
// depend on how many rows exist in total.
func BenchmarkModelRowResident(b *testing.B) {
	for _, total := range []int64{10_000, 1_000_000, 10_000_000, 100_000_000} {
		b.Run(sizeName(total), func(b *testing.B) {
			m := warmModel(b, total, PageSize*4)
			ctx := context.Background()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Row(ctx, int64(i%(PageSize*4)))
			}
		})
	}
}

func sizeName(n int64) string {
	switch {
	case n >= 1_000_000:
		return strconv.FormatInt(n/1_000_000, 10) + "M"
	case n >= 1000:
		return strconv.FormatInt(n/1000, 10) + "K"
	}
	return strconv.FormatInt(n, 10)
}

// BenchmarkRasterizeViewport measures a full software render of the grid:
// layout, text shaping and painting every visible cell into an image.
//
// This is the closest headless proxy to a real frame, and unlike the GL path
// it runs anywhere — Fyne's test driver rasterises without a window server, so
// this is what CI can enforce. It does not include GPU present time; the
// interactive harness in cmd/gridspike covers that on an attended machine.
func BenchmarkRasterizeViewport(b *testing.B) {
	for _, vp := range []struct {
		name          string
		width, height float32
	}{
		{"1440x960", 1440, 960},
		{"2560x1440", 2560, 1440},
	} {
		b.Run(vp.name, func(b *testing.B) {
			test.NewTempApp(b)

			m := warmModel(b, 10_000_000, 8192)
			g := NewTableGrid(context.Background(), m, theme.Light)

			w := test.NewTempWindow(b, g.Table)
			w.Resize(fyne.NewSize(vp.width, vp.height))
			c := w.Canvas()
			_ = c.Capture() // discard the first, which builds caches

			rows := VisibleRows(vp.height)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				g.Table.ScrollToOffset(fyne.NewPos(0, float32(i%2048)*theme.RowHeight))
				_ = c.Capture()
			}
			b.StopTimer()

			perFrame := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / 1e6
			b.ReportMetric(perFrame, "ms/frame")
			b.ReportMetric(1000/perFrame, "fps")
			b.ReportMetric(float64(rows*len(m.Columns())), "cells/frame")
		})
	}
}

// TestGateG0_1 asserts the part of spike W1's gate that CI can validly measure.
//
// It deliberately does NOT gate on capture time. The control experiment in
// control_test.go showed that 63% of a software capture is Fyne's CPU
// rasteriser painting 1.4M pixels — work the GPU does in the shipping build —
// so a capture number would fail the gate for reasons that do not exist in
// production. Gating on it would be measurement theatre.
//
// What CI can enforce is the CPU work per frame: formatting every visible cell
// and driving Fyne's layout. That is real, portable, and regresses first when
// the grid grows. True frame cadence needs an attended session:
//
//	go run -tags spike ./cmd/gridspike -bench
func TestGateG0_1(t *testing.T) {
	if testing.Short() {
		t.Skip("gate skipped in -short")
	}
	test.NewTempApp(t)

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

	// A generous viewport: 30 columns by 60 rows is wider and taller than a
	// typical one, so passing here covers the common case comfortably.
	const rows, cols = 60, 30
	cells := make([]fyne.CanvasObject, rows*cols)
	for i := range cells {
		cells[i] = g.createCell()
	}

	const iterations = 60
	start := time.Now()
	for i := 0; i < iterations; i++ {
		for r := 0; r < rows; r++ {
			for c := 0; c < cols; c++ {
				g.UpdateCell(widget.TableCellID{Row: i*7 + r, Col: c % 13}, cells[r*cols+c])
			}
		}
	}
	perUpdate := time.Since(start) / iterations

	w := test.NewTempWindow(t, g.Table)
	w.Resize(fyne.NewSize(1440, 960))
	start = time.Now()
	for i := 0; i < iterations; i++ {
		g.Table.ScrollToOffset(fyne.NewPos(0, float32(i*37)*theme.RowHeight))
		g.Table.Refresh()
	}
	perRefresh := time.Since(start) / iterations

	t.Logf("viewport update (%d cells): %v", rows*cols, perUpdate.Round(time.Microsecond))
	t.Logf("table refresh incl. layout:  %v", perRefresh.Round(time.Microsecond))

	// Budgets are a fraction of the 16.7ms frame, leaving the rest for
	// compositing. Exceeding these means the CPU path alone would threaten the
	// frame, which is a genuine regression regardless of GPU.
	const updateBudget = 4 * time.Millisecond
	const refreshBudget = 8 * time.Millisecond

	if perUpdate > updateBudget {
		t.Errorf("G0-1: viewport update %v exceeds %v; the CPU path alone "+
			"now threatens the frame budget", perUpdate, updateBudget)
	}
	if perRefresh > refreshBudget {
		t.Errorf("G0-1: table refresh %v exceeds %v", perRefresh, refreshBudget)
	}
}

// TestModelCostIsIndependentOfResultSize asserts NFR-P13 directly: render cost
// must scale with visible cells, not total rows. This is what makes "a result
// set of any size" a real claim rather than an aspiration.
func TestModelCostIsIndependentOfResultSize(t *testing.T) {
	ctx := context.Background()

	cost := func(total int64) time.Duration {
		m := NewModel(NewSyntheticFetcher(total))
		for i := int64(0); i < PageSize*4; i += PageSize {
			m.Row(ctx, i)
		}
		deadline := time.Now().Add(3 * time.Second)
		for m.Stats().ResidentPages < 4 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		const n = 200_000
		start := time.Now()
		for i := 0; i < n; i++ {
			m.Row(ctx, int64(i%(PageSize*4)))
		}
		return time.Since(start) / n
	}

	small := cost(10_000)
	huge := cost(100_000_000)
	t.Logf("row access: 10K rows %v, 100M rows %v", small, huge)

	// A 10 000x increase in result size must not meaningfully change the cost
	// of reading a resident row. Allow 3x for timer noise at nanosecond scale.
	if huge > small*3 && huge-small > 100*time.Nanosecond {
		t.Errorf("NFR-P13 violated: cost grew from %v to %v with result size", small, huge)
	}
}
