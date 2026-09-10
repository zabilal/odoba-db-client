package chart

import (
	"math"
	"math/rand"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/testutil/race"
)

func TestNiceScaleRoundsOutward(t *testing.T) {
	s := NiceScale(3.7194, 91.2, 500, false)
	if s.Min > 3.7194 || s.Max < 91.2 {
		t.Fatalf("scale [%v,%v] does not contain the data [3.7194,91.2]", s.Min, s.Max)
	}
	// Bounds must be human numbers, not the raw extremes.
	for _, v := range []float64{s.Min, s.Max} {
		if v != math.Round(v) {
			t.Errorf("bound %v is not a round number", v)
		}
	}
}

func TestConstantSeriesStillGetsAnAxis(t *testing.T) {
	// A query returning one repeated value must still render a readable axis,
	// not a divide-by-zero or a zero-height plot.
	for _, v := range []float64{0, 42, -7.5} {
		s := NiceScale(v, v, 400, false)
		if s.Max <= s.Min {
			t.Errorf("constant %v gave a degenerate scale [%v,%v]", v, s.Min, s.Max)
		}
		if p := s.Project(v); math.IsNaN(p) || math.IsInf(p, 0) {
			t.Errorf("constant %v projects to %v", v, p)
		}
	}
}

func TestProjectUnprojectRoundTrip(t *testing.T) {
	for _, inv := range []bool{false, true} {
		s := Scale{Min: -50, Max: 150, Pixels: 800, Invert: inv}
		for _, v := range []float64{-50, 0, 12.5, 150} {
			if got := s.Unproject(s.Project(v)); math.Abs(got-v) > 1e-9 {
				t.Errorf("invert=%v: round trip of %v gave %v", inv, v, got)
			}
		}
	}
}

func TestYAxisInverts(t *testing.T) {
	// Screen Y grows downward; values grow upward.
	s := Scale{Min: 0, Max: 100, Pixels: 400, Invert: true}
	if s.Project(100) >= s.Project(0) {
		t.Error("larger values must project higher on screen (smaller Y)")
	}
}

func TestTicksSnapZero(t *testing.T) {
	// Floating-point accumulation produces -2.7e-17 where the axis means 0,
	// and "-0.000000" on an axis looks broken.
	s := Scale{Min: -0.3, Max: 0.3, Pixels: 300}
	var sawZero bool
	for _, tk := range s.Ticks(6, func(v float64) string { return strconv.FormatFloat(v, 'g', 4, 64) }) {
		if tk.Value == 0 {
			sawZero = true
			if tk.Label != "0" {
				t.Errorf("zero tick labelled %q", tk.Label)
			}
		}
		if tk.Value != 0 && math.Abs(tk.Value) < 1e-12 {
			t.Errorf("near-zero tick %v was not snapped", tk.Value)
		}
	}
	if !sawZero {
		t.Error("no zero tick on an axis spanning zero")
	}
}

func TestTimeTicksPickAHumanUnit(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ts := TimeScale{Min: start, Max: start.Add(90 * 24 * time.Hour), Pixels: 900}
	ticks := ts.Ticks(8)
	if len(ticks) < 3 || len(ticks) > 20 {
		t.Fatalf("90-day axis produced %d ticks", len(ticks))
	}
	for i := 1; i < len(ticks); i++ {
		if ticks[i].Pixel <= ticks[i-1].Pixel {
			t.Error("time ticks are not increasing")
		}
	}
}

// --- downsampling ---

func series(n int, seed int64) []Point {
	r := rand.New(rand.NewSource(seed))
	pts := make([]Point, n)
	y := 0.0
	for i := range pts {
		y += r.NormFloat64()
		pts[i] = Point{X: float64(i), Y: y, Index: i}
	}
	return pts
}

func TestLTTBKeepsEndpointsAndBound(t *testing.T) {
	pts := series(100_000, 1)
	out := LTTB(pts, 2000)
	if len(out) != 2000 {
		t.Fatalf("got %d points, want 2000", len(out))
	}
	if out[0].Index != 0 || out[len(out)-1].Index != len(pts)-1 {
		t.Error("LTTB dropped the first or last point")
	}
	for i := 1; i < len(out); i++ {
		if out[i].X < out[i-1].X {
			t.Fatal("LTTB output is not in X order; the polyline would double back")
		}
	}
}

func TestLTTBPreservesASpike(t *testing.T) {
	// The reason for LTTB over every-nth sampling: in a query result the spike
	// is usually the row the user is looking for.
	pts := make([]Point, 100_000)
	for i := range pts {
		pts[i] = Point{X: float64(i), Y: 1, Index: i}
	}
	pts[54_321].Y = 1000

	for _, p := range LTTB(pts, 1000) {
		if p.Index == 54_321 {
			return
		}
	}
	t.Error("LTTB lost a single-row spike 1000x the baseline")
}

func TestMinMaxBucketsPreservesColumnExtent(t *testing.T) {
	pts := series(100_000, 2)
	xs := Scale{Min: 0, Max: float64(len(pts) - 1), Pixels: 1000}
	out := MinMaxBuckets(pts, xs, 1000)

	// Every original point's Y must lie within the drawn extent of its column.
	lo := make([]float64, 1000)
	hi := make([]float64, 1000)
	for i := range lo {
		lo[i], hi[i] = math.Inf(1), math.Inf(-1)
	}
	for _, p := range out {
		c := int(xs.Project(p.X) / xs.Pixels * 1000)
		if c >= 1000 {
			c = 999
		}
		lo[c] = math.Min(lo[c], p.Y)
		hi[c] = math.Max(hi[c], p.Y)
	}
	for _, p := range pts {
		c := int(xs.Project(p.X) / xs.Pixels * 1000)
		if c >= 1000 {
			c = 999
		}
		if p.Y < lo[c]-1e-9 || p.Y > hi[c]+1e-9 {
			t.Fatalf("point %d (y=%v) lies outside its column's drawn extent [%v,%v]",
				p.Index, p.Y, lo[c], hi[c])
		}
	}
}

// --- hit testing ---

func scatter(n int, seed int64) []Point {
	r := rand.New(rand.NewSource(seed))
	pts := make([]Point, n)
	for i := range pts {
		// Clustered, like real data, rather than uniform: uniform points make
		// every grid cell equally full and flatter the index.
		cx, cy := float64(r.Intn(5))*20, float64(r.Intn(4))*25
		pts[i] = Point{X: cx + r.NormFloat64()*6, Y: cy + r.NormFloat64()*6, Index: i}
	}
	return pts
}

func TestNearestIsMeasuredInPixelSpace(t *testing.T) {
	// X spans 0..1000, Y spans 0..1, on a square plot. In DATA units point B
	// is far closer to the cursor; in PIXELS point A is. The user sees pixels.
	xs := Scale{Min: 0, Max: 1000, Pixels: 500}
	ys := Scale{Min: 0, Max: 1, Pixels: 500, Invert: true}

	a := Point{X: 500, Y: 0.60, Index: 0} // 10px above the cursor
	b := Point{X: 520, Y: 0.50, Index: 1} // 0.1 data units away in Y, but 10px right
	c := Point{X: 500, Y: 0.30, Index: 2} // far

	h := NewHitIndex([]Point{a, b, c}, xs, ys)
	cursorX, cursorY := xs.Project(500), ys.Project(0.58)

	hit, ok := h.Nearest(cursorX, cursorY, 30)
	if !ok {
		t.Fatal("no hit")
	}
	if hit.Point.Index != 0 {
		t.Errorf("nearest resolved to point %d; distance was not measured in pixels",
			hit.Point.Index)
	}
}

func TestNearestRespectsRadius(t *testing.T) {
	// A tooltip that snaps to a point 200px away is worse than no tooltip.
	xs := Scale{Min: 0, Max: 100, Pixels: 1000}
	ys := Scale{Min: 0, Max: 100, Pixels: 1000, Invert: true}
	h := NewHitIndex([]Point{{X: 90, Y: 90}}, xs, ys)
	if _, ok := h.Nearest(100, 900, 12); ok {
		t.Error("reported a hit 800px from the only point")
	}
}

func TestHitsResolveAgainstFullDataNotDownsampled(t *testing.T) {
	// The value a tooltip reports must exist in the user's result set. Build
	// the index over the full series, draw only the downsampled one, and check
	// hits land on real rows — including rows LTTB discarded.
	pts := series(100_000, 3)
	xs := NiceScale(0, float64(len(pts)-1), 1200, false)
	ys := NiceScale(minY(pts), maxY(pts), 600, true)

	drawn := LTTB(pts, 2400)
	inDrawn := make(map[int]bool, len(drawn))
	for _, p := range drawn {
		inDrawn[p.Index] = true
	}

	h := NewHitIndex(pts, xs, ys)
	discardedHits := 0
	for px := 0.0; px < 1200; px += 3 {
		hit, ok := h.NearestX(px)
		if !ok {
			continue
		}
		if pts[hit.Point.Index] != hit.Point {
			t.Fatalf("hit %+v does not match the original row", hit.Point)
		}
		if !inDrawn[hit.Point.Index] {
			discardedHits++
		}
	}
	if discardedHits == 0 {
		t.Error("every hit landed on a drawn point; the index may be built over " +
			"the downsampled data, which would report proxy values in tooltips")
	}
}

func TestInRectReturnsOriginalIndices(t *testing.T) {
	xs := Scale{Min: 0, Max: 10, Pixels: 100}
	ys := Scale{Min: 0, Max: 10, Pixels: 100, Invert: true}
	pts := []Point{{X: 1, Y: 1, Index: 700}, {X: 5, Y: 5, Index: 701}, {X: 9, Y: 9, Index: 702}}
	h := NewHitIndex(pts, xs, ys)

	got := h.InRect(xs.Project(4), ys.Project(6), xs.Project(6), ys.Project(4))
	if len(got) != 1 || got[0] != 701 {
		t.Errorf("InRect = %v, want [701] (the original row index)", got)
	}
}

// --- GATE G0-4 --------------------------------------------------------------

// TestGateG0_4 is spike W4's gate (TASKS.md T0.57): tooltip hit-testing must
// be ACCURATE at 100k points. Accuracy is defined against a brute-force oracle
// — the index must return the same point, every time, not merely a nearby one.
func TestGateG0_4(t *testing.T) {
	const n = 100_000
	pts := scatter(n, 4)
	xs := NiceScale(minX(pts), maxX(pts), 1200, false)
	ys := NiceScale(minY(pts), maxY(pts), 700, true)

	start := time.Now()
	h := NewHitIndex(pts, xs, ys)
	build := time.Since(start)

	r := rand.New(rand.NewSource(99))
	queries := 20_000
	if race.Enabled {
		// The oracle is O(n) per query, and under the detector 20k queries
		// over 100k points takes most of a minute. Accuracy is what this run
		// is for, and 2k queries prove it just as well; timing is skipped.
		queries = 2_000
	}
	const radius = 12.0

	var mismatches, hits int
	var total, worst time.Duration
	for q := 0; q < queries; q++ {
		px, py := r.Float64()*xs.Pixels, r.Float64()*ys.Pixels

		t0 := time.Now()
		got, ok := h.Nearest(px, py, radius)
		d := time.Since(t0)
		total += d
		if d > worst {
			worst = d
		}

		want, wok := bruteNearest(pts, xs, ys, px, py, radius)
		if ok != wok {
			mismatches++
			continue
		}
		if !ok {
			continue
		}
		hits++
		// Ties at equal distance are legitimately resolved either way; what
		// must match is the distance, i.e. that no closer point was missed.
		if math.Abs(got.Distance-want.Distance) > 1e-9 {
			mismatches++
		}
	}
	mean := total / time.Duration(queries)

	t.Logf("index build over %d points: %v", n, build.Round(time.Microsecond))
	t.Logf("%d queries, %d hits: mean %v, worst %v", queries, hits,
		mean.Round(time.Nanosecond), worst.Round(time.Microsecond))
	t.Logf("disagreements with the brute-force oracle: %d", mismatches)

	if mismatches != 0 {
		t.Errorf("G0-4: %d of %d queries disagreed with the oracle; tooltips "+
			"would report the wrong row", mismatches, queries)
	}
	if hits < queries/20 {
		t.Errorf("G0-4: only %d hits; the test is not exercising dense regions", hits)
	}
	// Timing budgets only mean something without the race detector, which
	// slows execution 5-20x (see internal/testutil/race). The oracle
	// agreement above runs either way.
	if race.Enabled {
		return
	}
	// A mouse move must resolve well inside a frame.
	if mean > 50*time.Microsecond {
		t.Errorf("G0-4: mean query %v exceeds 50µs", mean)
	}
	// Built once per data change; must not stall opening the chart.
	if build > 250*time.Millisecond {
		t.Errorf("G0-4: index build %v exceeds 250ms", build)
	}
}

func BenchmarkHitNearest100k(b *testing.B) {
	pts := scatter(100_000, 5)
	xs := NiceScale(minX(pts), maxX(pts), 1200, false)
	ys := NiceScale(minY(pts), maxY(pts), 700, true)
	h := NewHitIndex(pts, xs, ys)
	r := rand.New(rand.NewSource(1))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Nearest(r.Float64()*1200, r.Float64()*700, 12)
	}
}

func BenchmarkLTTB100kTo2400(b *testing.B) {
	pts := series(100_000, 6)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LTTB(pts, 2400)
	}
}

func minX(p []Point) float64 { return extent(p, func(q Point) float64 { return q.X }, math.Min) }
func maxX(p []Point) float64 { return extent(p, func(q Point) float64 { return q.X }, math.Max) }
func minY(p []Point) float64 { return extent(p, func(q Point) float64 { return q.Y }, math.Min) }
func maxY(p []Point) float64 { return extent(p, func(q Point) float64 { return q.Y }, math.Max) }

func extent(p []Point, f func(Point) float64, pick func(a, b float64) float64) float64 {
	v := f(p[0])
	for _, q := range p[1:] {
		v = pick(v, f(q))
	}
	return v
}

var _ = sort.Float64s
