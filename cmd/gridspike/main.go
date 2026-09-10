//go:build spike

// Command gridspike is the interactive harness for spike W1 (RISK-1).
//
// The headless benchmarks in internal/ui/grid measure CPU work per frame. They
// cannot measure GPU compositing, which for a canvas-rendered toolkit drawing
// a thousand text objects is the other half of the question. This drives a real
// window and samples Fyne's animation callback, which runs on the render loop —
// so the intervals it records are true frame times, present included.
//
//	go run -tags spike ./cmd/gridspike -bench
//	go run -tags spike ./cmd/gridspike -synthetic -rows 10000000
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

const defaultDSN = "postgres://postgres:ikigai@localhost:55432/ikigai_test"

func main() {
	var (
		dsn       = flag.String("dsn", defaultDSN, "postgres DSN")
		table     = flag.String("table", "grid_spike", "table to browse")
		synthetic = flag.Bool("synthetic", false, "use the generated fetcher instead of postgres")
		rows      = flag.Int64("rows", 10_000_000, "row count for -synthetic")
		latency   = flag.Duration("latency", 0, "simulated fetch latency for -synthetic")
		bench     = flag.Bool("bench", false, "auto-scroll and report frame times, then exit")
		duration  = flag.Duration("duration", 12*time.Second, "auto-scroll duration for -bench")
	)
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var fetcher grid.Fetcher
	if *synthetic {
		sf := grid.NewSyntheticFetcher(*rows)
		sf.Latency = *latency
		fetcher = sf
		fmt.Printf("source: synthetic, %d rows, latency %v\n", *rows, *latency)
	} else {
		pf, err := grid.NewPgFetcher(ctx, *dsn, *table)
		if err != nil {
			fmt.Fprintf(os.Stderr, "postgres: %v\n\nis the spike container running?\n"+
				"  docker start ikigai-pg\n"+
				"or run with -synthetic\n", err)
			os.Exit(1)
		}
		defer pf.Close()
		fetcher = pf
		fmt.Printf("source: postgres %s.%s\n", *dsn, *table)
	}

	m := grid.NewModel(fetcher)

	countStart := time.Now()
	if err := m.LoadCount(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "count: %v\n", err)
		os.Exit(1)
	}
	total, _ := m.Total()
	fmt.Printf("rows: %d (counted in %v)\n", total, time.Since(countStart).Round(time.Millisecond))

	a := app.New()
	th := uitheme.New()
	a.Settings().SetTheme(th)

	w := a.NewWindow("Ikigai DB — Grid Spike (W1)")
	w.Resize(fyne.NewSize(1440, 900))

	g := grid.NewTableGrid(ctx, m, th.PaletteFor(fyneVariant(a)))

	status := widget.NewLabel("scroll to measure")
	// Coalesced: refreshing per page load re-enters Fyne's table renderer
	// and corrupts its cell map under concurrent fetches. See ADR-0002.
	m.OnPageLoaded = func(int64) { g.ScheduleRefresh() }
	m.OnError = func(err error) {
		fyne.Do(func() { status.SetText("fetch error: " + err.Error()) })
	}

	w.SetContent(container.NewBorder(nil, status, nil, nil, g.Table))

	// Time to first paint is NFR-P3: rows visible within 300ms of the data
	// arriving. Measured from here to the first page landing.
	firstPaint := time.Now()
	go func() {
		m.Prefetch(ctx, 0, 200)
		for !m.Resident(0) {
			time.Sleep(time.Millisecond)
		}
		elapsed := time.Since(firstPaint)
		fyne.Do(func() {
			status.SetText(fmt.Sprintf("first page in %v", elapsed.Round(time.Millisecond)))
		})
		fmt.Printf("first page resident in %v\n", elapsed.Round(time.Millisecond))
	}()

	if *bench {
		go runBench(a, w, g, m, total, *duration, status)
	}

	w.ShowAndRun()
}

func fyneVariant(a fyne.App) fyne.ThemeVariant {
	return a.Settings().ThemeVariant()
}

// runBench measures frame cost two ways.
//
// The animation callback runs on Fyne's render loop, so its intervals are true
// frame times with GPU present included — but it only ticks when a window
// server is actually driving the app. In a headless or unattended session it
// never fires, and reporting nothing would be worse than reporting what can be
// measured. So a capture pass always runs: Canvas().Capture() forces a full
// rasterisation of the window, which is the CPU half of a frame — layout, text
// shaping and painting — and is available everywhere.
func runBench(a fyne.App, w fyne.Window, g *grid.TableGrid, m *grid.Model,
	total int64, dur time.Duration, status *widget.Label) {

	// Let the window settle so startup cost is not counted as a frame.
	time.Sleep(1500 * time.Millisecond)

	rowHeight := uitheme.RowHeight
	maxOffset := float32(total) * rowHeight

	// --- pass 1: render-loop cadence, when a display is driving us ---

	var (
		frames []time.Duration
		last   time.Time
	)
	fmt.Printf("\nscrolling %d rows over %v...\n", total, dur)

	anim := fyne.NewAnimation(dur, func(p float32) {
		now := time.Now()
		if !last.IsZero() {
			frames = append(frames, now.Sub(last))
		}
		last = now

		off := p * maxOffset
		g.Table.ScrollToOffset(fyne.NewPos(0, off))
		first := int64(off / rowHeight)
		g.Prefetch(first, first+60)
	})
	anim.Curve = fyne.AnimationLinear
	fyne.Do(anim.Start)
	time.Sleep(dur + 500*time.Millisecond)

	if len(frames) >= 10 {
		report("render loop (true frame times, GPU present included)", frames, m)
	} else {
		fmt.Printf("\nrender loop sampled %d frames — no window server driving this\n"+
			"session, so true frame cadence is unavailable here. Falling back to\n"+
			"rasterisation cost, which is the CPU half of a frame.\n", len(frames))
	}

	// --- pass 2: rasterisation cost, always available ---

	const captures = 120
	caps := make([]time.Duration, 0, captures)
	fmt.Printf("\ncapturing %d full-window rasterisations...\n", captures)

	for i := 0; i < captures; i++ {
		off := float32(i) / float32(captures) * maxOffset
		first := int64(off / rowHeight)

		done := make(chan time.Duration, 1)
		fyne.Do(func() {
			g.Table.ScrollToOffset(fyne.NewPos(0, off))
			g.Prefetch(first, first+60)
			start := time.Now()
			_ = w.Canvas().Capture()
			done <- time.Since(start)
		})

		select {
		case d := <-done:
			caps = append(caps, d)
		case <-time.After(5 * time.Second):
			fmt.Println("capture timed out; aborting")
			i = captures
		}
	}
	report("rasterisation (layout + text shaping + paint, no GPU present)", caps, m)

	fyne.Do(a.Quit)
}

func report(label string, frames []time.Duration, m *grid.Model) {
	if len(frames) < 10 {
		fmt.Printf("\nonly %d frames sampled; too few to judge\n", len(frames))
		return
	}
	sort.Slice(frames, func(i, j int) bool { return frames[i] < frames[j] })

	pct := func(p float64) time.Duration {
		i := int(float64(len(frames)) * p)
		if i >= len(frames) {
			i = len(frames) - 1
		}
		return frames[i]
	}

	var sum time.Duration
	for _, f := range frames {
		sum += f
	}
	mean := sum / time.Duration(len(frames))

	ms := func(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }
	fps := func(d time.Duration) float64 {
		if d == 0 {
			return 0
		}
		return 1e9 / float64(d.Nanoseconds())
	}

	var over33, over16 int
	for _, f := range frames {
		if f > 33300*time.Microsecond {
			over33++
		}
		if f > 16700*time.Microsecond {
			over16++
		}
	}

	s := m.Stats()

	fmt.Printf("\n=== W1: %s ===\n%d samples\n", label, len(frames))
	fmt.Printf("  mean   %6.2f ms  (%5.1f fps)\n", ms(mean), fps(mean))
	fmt.Printf("  p50    %6.2f ms  (%5.1f fps)\n", ms(pct(0.50)), fps(pct(0.50)))
	fmt.Printf("  p95    %6.2f ms  (%5.1f fps)\n", ms(pct(0.95)), fps(pct(0.95)))
	fmt.Printf("  p99    %6.2f ms  (%5.1f fps)\n", ms(pct(0.99)), fps(pct(0.99)))
	fmt.Printf("  worst  %6.2f ms\n", ms(frames[len(frames)-1]))
	fmt.Printf("  frames over 16.7ms (60fps): %d (%.1f%%)\n",
		over16, 100*float64(over16)/float64(len(frames)))
	fmt.Printf("  frames over 33.3ms (30fps): %d (%.1f%%)\n",
		over33, 100*float64(over33)/float64(len(frames)))
	fmt.Printf("\n=== model cache ===\n")
	fmt.Printf("  hits %d  misses %d  fetches %d  resident pages %d\n",
		s.Hits, s.Misses, s.Fetches, s.ResidentPages)

	fmt.Printf("\n=== GATE G0-1 ===\n")
	switch {
	case pct(0.95) <= 16700*time.Microsecond:
		fmt.Println("  PASS — p95 within the 60fps budget")
	case pct(0.95) <= 33300*time.Microsecond:
		fmt.Println("  PASS — p95 within the 30fps floor (below the 60fps target)")
	default:
		fmt.Println("  FAIL — p95 misses the 30fps floor; evaluate the raster fallback (T0.43)")
	}
}
