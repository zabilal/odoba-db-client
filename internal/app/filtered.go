package app

import (
	"context"
	"sync"

	"github.com/ikigai-db/ikigai-db/internal/app/rowfilter"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Filtering what has been read, where the server will not filter (FR-13.9,
// ADR-0099).
//
// A grid asks for a window of rows and takes a short one to mean the data ran
// out, so a filter cannot simply drop rows from a window: asking for fifty and
// being handed three would end the grid at three. This reads from the source
// until it has filled the window or the source has nothing more, which keeps
// that contract while the filtering happens here.
//
// What it claims is narrower than a filter on a server, and the difference is
// the point: this has seen what it has read and nothing else.

// filterPage is how many rows it reads at a time while looking for matches.
// Large enough that a selective filter is not a round trip per row, small
// enough that nothing is read far past what was asked for.
const filterPage = 500

// filterScan bounds what one filter will read. A log has no end worth reading
// to, and memory here must not depend on how much was written (NFR-P10). It is
// a variable so that a test can prove the bound without writing fifty thousand
// rows to prove it.
var filterScan int64 = 50_000

// FilteredRows is a source's rows with a filter applied here.
type FilteredRows struct {
	src rowSource
	m   *rowfilter.Matcher

	mu      sync.Mutex
	kept    []model.Row // the matches found so far, in order
	read    int64       // how far into the source it has looked
	done    bool        // the source had nothing more
	stopped bool        // it stopped at the bound rather than at the end
}

// rowSource is what a filter reads from: a browse, or a query's result.
type rowSource interface {
	Columns() []model.ColumnDef
	Fetch(ctx context.Context, offset, limit int64) ([]model.Row, error)
	Count(ctx context.Context) (int64, error)
}

// NewFilteredRows reads src through filters. A filter that cannot be compiled
// — a pattern that is not one — is refused now rather than matching nothing.
func NewFilteredRows(src rowSource, filters []source.Filter) (*FilteredRows, error) {
	m, err := rowfilter.Compile(src.Columns(), filters)
	if err != nil {
		return nil, err
	}
	return &FilteredRows{src: src, m: m}, nil
}

// Columns are the source's own: filtering changes which rows are shown, never
// what a row is.
func (f *FilteredRows) Columns() []model.ColumnDef { return f.src.Columns() }

// Fetch returns up to limit matching rows from offset, reading as much of the
// source as it must to find them.
func (f *FilteredRows) Fetch(ctx context.Context, offset, limit int64) (_ []model.Row, err error) {
	defer panics.Recover(&err, "filtering rows")
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fill(ctx, offset+limit); err != nil {
		return nil, err
	}
	if offset >= int64(len(f.kept)) {
		return nil, nil
	}
	end := min(offset+limit, int64(len(f.kept)))
	out := make([]model.Row, end-offset)
	copy(out, f.kept[offset:end])
	return out, nil
}

// fill reads until it holds want matches, or there is nothing more to read.
// The caller holds the lock.
func (f *FilteredRows) fill(ctx context.Context, want int64) error {
	for !f.done && int64(len(f.kept)) < want {
		if f.read >= filterScan {
			// Far enough. Saying so is the alternative to reading a log until
			// the memory runs out.
			f.done, f.stopped = true, true
			return nil
		}
		rows, err := f.src.Fetch(ctx, f.read, filterPage)
		if err != nil {
			return err
		}
		f.read += int64(len(rows))
		for _, row := range rows {
			if f.m.Match(row) {
				f.kept = append(f.kept, row)
			}
		}
		if int64(len(rows)) < filterPage {
			f.done = true
		}
	}
	return nil
}

// Count is how many rows match, once everything has been read, and unknown
// before that. A grid over an unknown count still works; one told a number
// that is only what has been found so far would draw a scrollbar that lies.
func (f *FilteredRows) Count(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.done && !f.stopped {
		return int64(len(f.kept)), nil
	}
	return -1, nil
}

// Read reports how far into the source the filter has looked, whether it
// reached the end, and whether it stopped at its bound instead. The footer
// says this: a filter that has seen a thousand records of a log has not
// searched the log, and must not look as though it has.
func (f *FilteredRows) Read() (rows int64, atEnd, stopped bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.read, f.done && !f.stopped, f.stopped
}
