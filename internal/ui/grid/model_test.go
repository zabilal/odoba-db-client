package grid

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/theme"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

func TestRowMissThenHit(t *testing.T) {
	m := NewModel(NewSyntheticFetcher(10_000))
	var loaded atomic.Bool
	m.OnPageLoaded = func(int64) { loaded.Store(true) }

	ctx := context.Background()

	// A miss must return immediately rather than block the UI goroutine.
	if _, ok := m.Row(ctx, 0); ok {
		t.Fatal("first access should miss")
	}
	waitFor(t, loaded.Load, "page load")

	row, ok := m.Row(ctx, 0)
	if !ok {
		t.Fatal("row should be resident after load")
	}
	if got, want := row[0], int64(1); got != want {
		t.Errorf("row 0 id = %v, want %v", got, want)
	}
}

func TestRowNeverBlocks(t *testing.T) {
	// The whole design rests on this: with a slow source, Row must still
	// return promptly (ARCH-6). If it ever waits on the fetch, the UI stalls.
	f := NewSyntheticFetcher(10_000)
	f.Latency = 500 * time.Millisecond
	m := NewModel(f)

	start := time.Now()
	for i := int64(0); i < 100; i++ {
		m.Row(context.Background(), i)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("100 misses took %v; Row is blocking on the fetch", elapsed)
	}
}

func TestConcurrentFetchesAreDeduped(t *testing.T) {
	f := &countingFetcher{SyntheticFetcher: NewSyntheticFetcher(10_000), delay: 40 * time.Millisecond}
	m := NewModel(f)

	// Every row here lives in page 0. Without dedupe this issues 200 queries.
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.Row(context.Background(), int64(i%PageSize))
		}(i)
	}
	wg.Wait()
	waitFor(t, func() bool { return m.Resident(0) }, "page 0")

	if n := f.calls.Load(); n != 1 {
		t.Errorf("page 0 fetched %d times, want 1; in-flight dedupe is broken", n)
	}
}

func TestEvictionBoundsMemory(t *testing.T) {
	// NFR-P11: memory must not scale with result size. Walking far more pages
	// than the residency bound must not grow the cache.
	m := NewModel(NewSyntheticFetcher(10_000_000))
	ctx := context.Background()

	for p := int64(0); p < MaxResidentPages*3; p++ {
		m.Row(ctx, p*PageSize)
	}
	waitFor(t, func() bool { return m.Stats().Fetches >= int64(MaxResidentPages) }, "fetches")
	time.Sleep(200 * time.Millisecond)

	if got := m.Stats().ResidentPages; got > MaxResidentPages {
		t.Errorf("resident pages = %d, exceeds bound of %d", got, MaxResidentPages)
	}
}

func TestScrollingDoesNotFetchPerRow(t *testing.T) {
	// A page must serve PageSize rows. If this ratio collapses, scrolling
	// becomes one query per row and the grid is unusable regardless of how
	// fast it draws.
	f := &countingFetcher{SyntheticFetcher: NewSyntheticFetcher(1_000_000)}
	m := NewModel(f)
	ctx := context.Background()

	const rows = 4096
	for i := int64(0); i < rows; i++ {
		m.Row(ctx, i)
		if i%64 == 0 {
			time.Sleep(time.Millisecond) // let fetches land, as a real scroll would
		}
	}
	waitFor(t, func() bool { return m.Resident(rows / PageSize) }, "last page")

	fetches := f.calls.Load()
	maxWanted := int64(rows/PageSize) + 4 // pages touched, plus prefetch slack
	if fetches > maxWanted {
		t.Errorf("%d rows caused %d fetches, want <= %d", rows, fetches, maxWanted)
	}
}

func TestFetchErrorIsSurfacedNotSwallowed(t *testing.T) {
	// FR-15.7: a grid that silently shows blank rows on failure is worse than
	// one that says it could not load them.
	sentinel := errors.New("connection reset")
	m := NewModel(&failingFetcher{SyntheticFetcher: NewSyntheticFetcher(1000), err: sentinel})

	var got atomic.Value
	m.OnError = func(err error) { got.Store(err) }

	m.Row(context.Background(), 0)
	waitFor(t, func() bool { return got.Load() != nil }, "error callback")

	if !errors.Is(got.Load().(error), sentinel) {
		t.Errorf("got %v, want %v", got.Load(), sentinel)
	}
	if m.Resident(0) {
		t.Error("a failed page must not be cached as empty")
	}
}

func TestInvalidateClearsEverything(t *testing.T) {
	m := NewModel(NewSyntheticFetcher(10_000))
	ctx := context.Background()
	m.Row(ctx, 0)
	waitFor(t, func() bool { return m.Resident(0) }, "page 0")

	m.Invalidate()

	if m.Resident(0) {
		t.Error("Invalidate left a cached page; a filter change would show stale rows")
	}
	if _, known := m.Total(); known {
		t.Error("Invalidate left a stale total")
	}
}

func TestRowsPastEndReportLoaded(t *testing.T) {
	// A short final page must not look like a perpetual miss, or the grid
	// would fetch it forever at the bottom of the result.
	m := NewModel(NewSyntheticFetcher(10))
	ctx := context.Background()
	m.Row(ctx, 0)
	waitFor(t, func() bool { return m.Resident(0) }, "page 0")

	row, loaded := m.Row(ctx, 50)
	if !loaded {
		t.Error("row past the end should report loaded")
	}
	if row != nil {
		t.Error("row past the end should be nil")
	}
}

// --- helpers ---

// countingFetcher records how many pages were actually fetched, which is how
// the dedupe and paging tests distinguish "worked" from "worked by accident".
type countingFetcher struct {
	*SyntheticFetcher
	calls atomic.Int64
	delay time.Duration
}

func (c *countingFetcher) Fetch(ctx context.Context, offset, limit int64) ([]model.Row, error) {
	c.calls.Add(1)
	if c.delay > 0 {
		select {
		case <-time.After(c.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.SyntheticFetcher.Fetch(ctx, offset, limit)
}

type failingFetcher struct {
	*SyntheticFetcher
	err error
}

func (f *failingFetcher) Fetch(context.Context, int64, int64) ([]model.Row, error) {
	return nil, f.err
}

func TestScheduleRefreshCoalescesThroughTheRunner(t *testing.T) {
	test.NewTempApp(t)
	m := NewModel(NewSyntheticFetcher(1000))
	q := &uithread.Queue{}
	g := NewTableGridWith(context.Background(), m, theme.Light, q.Run, 0)
	for i := 0; i < 50; i++ {
		g.ScheduleRefresh() // as a burst of page loads would
	}
	if q.Len() != 1 {
		t.Fatalf("50 page loads queued %d refreshes, want 1", q.Len())
	}
	q.Flush()
	g.ScheduleRefresh()
	if q.Len() != 1 {
		t.Error("a load after the refresh ran must queue another")
	}
}

// uncounted is a fetcher over a source that cannot count cheaply.
type uncounted struct{ *SyntheticFetcher }

func (uncounted) Count(context.Context) (int64, error) { return -1, nil }

// loadPage fetches one page through the model and waits for it.
func loadPage(t *testing.T, m *Model, page int64) {
	t.Helper()
	loaded := make(chan struct{}, 8)
	m.OnPageLoaded = func(int64) { loaded <- struct{}{} }
	m.Row(context.Background(), page*PageSize)
	select {
	case <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatalf("page %d never loaded", page)
	}
}

func TestUnknownTotalBecomesKnownAtAShortPage(t *testing.T) {
	m := NewModel(uncounted{NewSyntheticFetcher(2*PageSize + PageSize/2)})
	if err := m.LoadCount(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n, final := m.Extent(); n != 0 || final {
		t.Fatalf("before any fetch: extent %d final %v", n, final)
	}
	loadPage(t, m, 0)
	if n, final := m.Extent(); n != PageSize || final {
		t.Fatalf("after a full page: extent %d final %v", n, final)
	}
	loadPage(t, m, 2)
	want := int64(2*PageSize + PageSize/2)
	if n, final := m.Extent(); n != want || !final {
		t.Fatalf("after the short last page: extent %d final %v, want %d final", n, final, want)
	}
	if err := m.LoadCount(context.Background()); err != nil {
		t.Fatal(err)
	}
	if total, known := m.Total(); !known || total != want {
		t.Errorf("an unknown count forgot the end of the data: %d %v", total, known)
	}
	m.Invalidate()
	if _, final := m.Extent(); final {
		t.Error("Invalidate must forget a total found by reaching the end: the data may have changed")
	}
}

func TestACountSurvivesInvalidate(t *testing.T) {
	m := NewModel(NewSyntheticFetcher(1000))
	if err := m.LoadCount(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.Invalidate()
	if n, final := m.Extent(); !final || n != 1000 {
		t.Errorf("extent %d final %v; a counted total is replaced by the next count, not dropped", n, final)
	}
}

func TestGridOverAnUncountedSourceStillFetches(t *testing.T) {
	// The regression: sized by resident pages, an uncounted grid had zero
	// rows, drew no cells, and so never scheduled its first fetch.
	test.NewTempApp(t)
	m := NewModel(uncounted{NewSyntheticFetcher(PageSize / 2)})
	g := NewTableGridWith(context.Background(), m, theme.Light, (&uithread.Queue{}).Run, 0)
	if rows, _ := g.length(); rows != PageSize {
		t.Fatalf("an empty uncounted grid shows %d rows; it needs a page of placeholders to fetch into", rows)
	}
	loadPage(t, m, 0)
	if rows, _ := g.length(); rows != PageSize/2 {
		t.Errorf("after the only, short page the grid shows %d rows, want %d", rows, PageSize/2)
	}
}

func TestAShortPageCorrectsAStaleCount(t *testing.T) {
	// A filter narrows 1000 rows to 10. Until the recount lands, the kept
	// total would draw 990 rows that no longer exist; the first page, short,
	// is proof of the new end.
	f := NewSyntheticFetcher(1000)
	m := NewModel(f)
	if err := m.LoadCount(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.Rows = 10
	m.Invalidate()
	loadPage(t, m, 0)
	if n, final := m.Extent(); n != 10 || !final {
		t.Errorf("extent %d final %v, want 10 final", n, final)
	}
}
