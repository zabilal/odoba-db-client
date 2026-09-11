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
	"sync/atomic"

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
	// src is the current fetcher. Atomic because fetches and counts run off
	// the UI goroutine, and a new sort or filter swaps it (SetFetcher).
	src atomic.Pointer[fetcherRef]
	// gen is bumped by Invalidate and SetFetcher. A fetch or count begun in
	// an older generation is dropped when it lands: its rows belong to an
	// order or a moment that is gone, and would appear in the wrong place.
	gen uint64

	mu sync.RWMutex
	// pages holds resident data keyed by page index.
	pages map[int64][]model.Row
	// lru tracks page access order, most recent last.
	lru []int64
	// inflight dedupes concurrent fetches for the same page.
	inflight map[int64]bool

	total int64 // -1 when unknown
	// seen is how many rows are known to exist: the furthest any fetched page
	// reached. It only grows until Invalidate. While the total is unknown, it
	// is what the grid sizes itself by.
	seen int64
	// derived reports that total came from reaching the end of the data, not
	// from a count, so Invalidate must forget it.
	derived bool

	// added are new rows, not yet written, shown before the rows read
	// (FR-4.2). Row, Read, Extent, Resident and Prefetch count them first;
	// Total is the rows read alone. A new sort or filter keeps them.
	added []model.Row

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
	m := &Model{
		pages:    make(map[int64][]model.Row, MaxResidentPages),
		inflight: make(map[int64]bool),
		total:    -1,
	}
	m.src.Store(&fetcherRef{f})
	return m
}

type fetcherRef struct{ f Fetcher }

func (m *Model) current() Fetcher { return m.src.Load().f }

// Columns returns the result shape.
func (m *Model) Columns() []model.ColumnDef { return m.current().Columns() }

// SetFetcher swaps the source, as a new sort or filter does, then drops the
// cached pages like Invalidate: the grid keeps its size and position until
// the new rows arrive. A fetch still running against the old source is
// dropped when it lands.
func (m *Model) SetFetcher(f Fetcher) {
	m.src.Store(&fetcherRef{f})
	m.Invalidate()
}

// SetAdded sets the new rows shown before the rows read.
func (m *Model) SetAdded(rows []model.Row) {
	m.mu.Lock()
	m.added = rows
	m.mu.Unlock()
}

// Added is how many new rows come before the rows read.
func (m *Model) Added() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.added)
}

// Total returns the count of the rows read, and whether it is known.
func (m *Model) Total() (int64, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.total, m.total >= 0
}

// LoadCount resolves the total row count.
func (m *Model) LoadCount(ctx context.Context) error {
	m.mu.RLock()
	gen, f := m.gen, m.current()
	m.mu.RUnlock()
	n, err := f.Count(ctx)
	if err != nil {
		return err
	}
	if n < 0 {
		// No count from this source. Keep any total already found by reaching
		// the end of the data: forgetting it would regrow the grid by a page
		// of placeholders that can never load.
		return nil
	}
	m.mu.Lock()
	if gen == m.gen { // not a count of a source since replaced
		m.total, m.derived = n, false
	}
	m.mu.Unlock()
	return nil
}

// Extent is how many rows are known to exist, the new ones first: the total
// when final, else the furthest row any fetch has reached so far.
func (m *Model) Extent() (n int64, final bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	k := int64(len(m.added))
	if m.total >= 0 {
		return m.total + k, true
	}
	return m.seen + k, false
}

// noteReachLocked records how far a fetched page reached. A short page is the
// end of the data (see Fetcher): it makes an unknown total known, and corrects
// a stale one kept across Invalidate when the data shrank. Called with mu
// held.
func (m *Model) noteReachLocked(page int64, n int) {
	reach := page*PageSize + int64(n)
	if reach > m.seen {
		m.seen = reach
	}
	if n < PageSize && reach != m.total {
		m.total, m.derived = reach, true
	}
}

// Row returns row i.
//
// loaded is false when the row is not resident; the caller draws a placeholder
// and a fetch is scheduled. This method is on the hot path — it runs for every
// visible cell of every frame — so it does no allocation and holds only a read
// lock.
func (m *Model) Row(ctx context.Context, i int64) (row model.Row, loaded bool) {
	m.mu.RLock()
	k := int64(len(m.added))
	if i < k {
		row = m.added[i]
		m.mu.RUnlock()
		return row, true
	}
	i -= k
	page, within := i/PageSize, int(i%PageSize)
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

// Read returns rows [from, to), from resident pages where it can and the
// fetcher where it must, stopping early at the end of the data. Unlike Row it
// blocks, so it runs off the UI goroutine: it is for copying a selection
// (FR-3.7), which can reach rows never drawn. What it fetches is not kept, so
// a large copy cannot evict the pages on screen.
func (m *Model) Read(ctx context.Context, from, to int64) ([]model.Row, error) {
	m.mu.RLock()
	added := m.added
	m.mu.RUnlock()
	var out []model.Row
	k := int64(len(added))
	if from < k {
		out = append(out, added[from:min(to, k)]...)
	}
	from, to = max(from-k, 0), to-k
	f := m.current()
	for page := from / PageSize; page*PageSize < to; page++ {
		m.mu.RLock()
		rows, ok := m.pages[page]
		m.mu.RUnlock()
		if !ok {
			var err error
			if rows, err = f.Fetch(ctx, page*PageSize, PageSize); err != nil {
				return nil, err
			}
		}
		lo, hi := max(from-page*PageSize, 0), min(to-page*PageSize, int64(len(rows)))
		if lo < hi {
			out = append(out, rows[lo:hi]...)
		}
		if len(rows) < PageSize {
			break // the end of the data
		}
	}
	return out, nil
}

// Resident reports whether a row is available without a fetch. Used by the
// renderers to decide between drawing a value and drawing a placeholder
// without paying for the LRU bookkeeping Row does.
func (m *Model) Resident(i int64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	k := int64(len(m.added))
	if i < k {
		return true
	}
	_, ok := m.pages[(i-k)/PageSize]
	return ok
}

// Prefetch warms the pages covering [first,last] and their neighbours. The
// grid calls this when the viewport moves, so that a steady scroll stays ahead
// of the data rather than chasing it.
func (m *Model) Prefetch(ctx context.Context, first, last int64) {
	k := int64(m.Added())
	first, last = first-k, last-k
	if last < 0 {
		return // only new rows in view
	}
	lo := max(first, 0)/PageSize - prefetchRadius
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
	gen, f := m.gen, m.current()
	m.mu.Unlock()

	m.count(&m.fetches)

	go func() {
		rows, err := f.Fetch(ctx, page*PageSize, PageSize)

		m.mu.Lock()
		if gen != m.gen {
			m.mu.Unlock()
			return // fetched for a source or moment since replaced
		}
		delete(m.inflight, page)
		if err == nil {
			m.pages[page] = rows
			m.lru = append(m.lru, page)
			m.evictLocked()
			m.noteReachLocked(page, len(rows))
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

// Invalidate drops every cached page, for a reload or a filter or sort
// change.
//
// A counted total is kept until the next LoadCount replaces it, so the grid
// keeps its size and scroll position meanwhile instead of collapsing to one
// page; if the data shrank, the first short page corrects it sooner. A total
// known only from reaching the end is dropped, since the end may have moved.
func (m *Model) Invalidate() {
	m.mu.Lock()
	m.gen++
	m.pages = make(map[int64][]model.Row, MaxResidentPages)
	m.inflight = make(map[int64]bool) // requests of the old generation no longer count
	m.lru = m.lru[:0]
	m.seen = 0
	if m.derived {
		m.total, m.derived = -1, false
	}
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
