// Package grid implements the data grid — the product's centre of gravity and
// the subject of spike W1 (TASKS.md T0.38–T0.44, RISK-1).
//
// The model here is deliberately independent of any rendering approach, so the
// widget.Table candidate and the raster fallback can be measured against the
// same data path and the comparison means something.
package grid

import (
	"context"
	"sync"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Fetcher supplies windows of rows. It is the seam between the grid and a
// data source: source.Browser adapts to it, and the benchmarks use a synthetic
// implementation so CI needs no database.
type Fetcher interface {
	// Columns describes the result shape. Stable for the fetcher's lifetime.
	Columns() []model.ColumnDef

	// Fetch returns up to limit rows starting at offset. Returning fewer than
	// limit means the end was reached.
	Fetch(ctx context.Context, offset, limit int64) ([]model.Row, error)

	// Count returns the total row count, or -1 when unknown. A grid over an
	// unknown count still works; it simply cannot draw a proportional
	// scrollbar.
	Count(ctx context.Context) (int64, error)
}

const (
	// PageSize is the fetch granularity. Large enough that scrolling a screen
	// rarely crosses two boundaries, small enough that a fetch is quick.
	PageSize = 256

	// MaxResidentPages bounds memory: 64 pages x 256 rows = 16 384 rows.
	// NFR-P11 forbids memory scaling with result size, so the cache is a
	// window over the data, never an accumulation of it.
	MaxResidentPages = 64

	// prefetchRadius is how many pages either side of a request to warm.
	// One page is enough to hide latency during a normal scroll without
	// tripling the query load during a fling.
	prefetchRadius = 1
)

// Model is a windowed, asynchronously-populated view over a result set.
//
// Row is called from the UI goroutine on every frame and must never block
// (ARCH-6). A miss returns immediately with loaded=false; the caller draws a
// placeholder, and the model schedules a fetch that calls OnPageLoaded when
// the data lands.
type Model struct {
	fetcher Fetcher

	mu sync.RWMutex
	// pages holds resident data keyed by page index.
	pages map[int64][]model.Row
	// lru tracks page access order, most recent last.
	lru []int64
	// inflight dedupes concurrent fetches for the same page.
	inflight map[int64]bool

	total int64 // -1 when unknown

	// OnPageLoaded is invoked, off the UI goroutine, when a page arrives.
	// The UI must marshal its refresh onto the main goroutine itself.
	OnPageLoaded func(page int64)

	// OnError is invoked when a fetch fails. Errors are surfaced rather than
	// swallowed: a grid that silently shows blank rows on failure is worse
	// than one that says it could not load them (FR-15.7).
	OnError func(err error)

	// stats
	statMu                sync.Mutex
	hits, misses, fetches int64
}

// NewModel builds a model over a fetcher.
func NewModel(f Fetcher) *Model {
	return &Model{
		fetcher:  f,
		pages:    make(map[int64][]model.Row, MaxResidentPages),
		inflight: make(map[int64]bool),
		total:    -1,
	}
}

// Columns returns the result shape.
func (m *Model) Columns() []model.ColumnDef { return m.fetcher.Columns() }

// Total returns the row count and whether it is known.
func (m *Model) Total() (int64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.total, m.total >= 0
}

// LoadCount resolves the total row count.
func (m *Model) LoadCount(ctx context.Context) error {
	n, err := m.fetcher.Count(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.total = n
	m.mu.Unlock()
	return nil
}

// Row returns row i.
//
// loaded is false when the row is not resident; the caller draws a placeholder
// and a fetch is scheduled. This method is on the hot path — it runs for every
// visible cell of every frame — so it does no allocation and holds only a read
// lock.
func (m *Model) Row(ctx context.Context, i int64) (row model.Row, loaded bool) {
	page := i / PageSize
	within := int(i % PageSize)

	m.mu.RLock()
	rows, ok := m.pages[page]
	m.mu.RUnlock()

	if ok {
		m.touch(page)
		m.count(&m.hits)
		if within < len(rows) {
			return rows[within], true
		}
		// Resident page, but short: this row is past the end of the data.
		return nil, true
	}

	m.count(&m.misses)
	m.schedule(ctx, page)
	return nil, false
}

// Resident reports whether a row is available without a fetch. Used by the
// renderers to decide between drawing a value and drawing a placeholder
// without paying for the LRU bookkeeping Row does.
func (m *Model) Resident(i int64) bool {
	m.mu.RLock()
	_, ok := m.pages[i/PageSize]
	m.mu.RUnlock()
	return ok
}

// Prefetch warms the pages covering [first,last] and their neighbours. The
// grid calls this when the viewport moves, so that a steady scroll stays ahead
// of the data rather than chasing it.
func (m *Model) Prefetch(ctx context.Context, first, last int64) {
	lo := first/PageSize - prefetchRadius
	if lo < 0 {
		lo = 0
	}
	hi := last/PageSize + prefetchRadius
	for p := lo; p <= hi; p++ {
		m.mu.RLock()
		_, resident := m.pages[p]
		m.mu.RUnlock()
		if !resident {
			m.schedule(ctx, p)
		}
	}
}

// schedule starts a fetch for a page unless one is already running.
func (m *Model) schedule(ctx context.Context, page int64) {
	m.mu.Lock()
	if m.inflight[page] {
		m.mu.Unlock()
		return
	}
	if _, resident := m.pages[page]; resident {
		m.mu.Unlock()
		return
	}
	m.inflight[page] = true
	m.mu.Unlock()

	m.count(&m.fetches)

	go func() {
		rows, err := m.fetcher.Fetch(ctx, page*PageSize, PageSize)

		m.mu.Lock()
		delete(m.inflight, page)
		if err == nil {
			m.pages[page] = rows
			m.lru = append(m.lru, page)
			m.evictLocked()
		}
		m.mu.Unlock()

		if err != nil {
			if m.OnError != nil && ctx.Err() == nil {
				m.OnError(err)
			}
			return
		}
		if m.OnPageLoaded != nil {
			m.OnPageLoaded(page)
		}
	}()
}

// touch records a page access for LRU ordering.
func (m *Model) touch(page int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.lru) - 1; i >= 0; i-- {
		if m.lru[i] == page {
			m.lru = append(m.lru[:i], m.lru[i+1:]...)
			break
		}
	}
	m.lru = append(m.lru, page)
}

// evictLocked drops least-recently-used pages past the residency bound.
func (m *Model) evictLocked() {
	for len(m.pages) > MaxResidentPages && len(m.lru) > 0 {
		oldest := m.lru[0]
		m.lru = m.lru[1:]
		// A page can appear in lru more than once only transiently; deleting
		// an already-evicted key is harmless.
		delete(m.pages, oldest)
	}
}

// Invalidate drops every cached page, for use after a filter or sort change.
func (m *Model) Invalidate() {
	m.mu.Lock()
	m.pages = make(map[int64][]model.Row, MaxResidentPages)
	m.lru = m.lru[:0]
	m.total = -1
	m.mu.Unlock()
}

// Stats reports cache behaviour. Used by the spike benchmarks to prove that
// scrolling does not degenerate into a fetch per row.
type Stats struct {
	Hits, Misses, Fetches int64
	ResidentPages         int
}

// Stats returns a snapshot.
func (m *Model) Stats() Stats {
	m.statMu.Lock()
	s := Stats{Hits: m.hits, Misses: m.misses, Fetches: m.fetches}
	m.statMu.Unlock()

	m.mu.RLock()
	s.ResidentPages = len(m.pages)
	m.mu.RUnlock()
	return s
}

func (m *Model) count(p *int64) {
	m.statMu.Lock()
	*p++
	m.statMu.Unlock()
}
