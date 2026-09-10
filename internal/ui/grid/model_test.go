package grid

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
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
