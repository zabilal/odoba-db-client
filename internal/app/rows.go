package app

import (
	"context"
	"io"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// exportPage is how many rows an export fetches at a time: enough that
// round trips are not the cost, few enough that memory stays flat (FR-10.3).
const exportPage = 5000

// fetcher is what an export pages through: a browse or a query result.
type fetcher interface {
	Columns() []model.ColumnDef
	Fetch(ctx context.Context, offset, limit int64) ([]model.Row, error)
}

// Rows streams every row the browse would show, for export (FR-10.2): the
// whole table, or the filtered result when the browse is filtered. It reads
// to the end a page at a time and lets each page go before fetching the next.
//
// Pages are deterministic because the source orders by a unique key (the
// PostgreSQL driver appends one; see its tiebreak). A deep page of a huge
// table still costs the server an OFFSET scan, which keyset paging would
// avoid; that is recorded as open in ADR-0013.
func (b *BrowseSource) Rows() model.RowStream { return &pagedRows{f: b} }

// Rows streams a query result's rows, for export. They are in memory already
// (MaxResultRows); this only spares the export copying them all first.
func (r *ResultSet) Rows() model.RowStream { return &pagedRows{f: r} }

type pagedRows struct {
	f    fetcher
	page []model.Row
	i    int
	next int64
	last bool // the latest page was short: there are no more
}

func (p *pagedRows) Columns() []model.ColumnDef { return p.f.Columns() }
func (p *pagedRows) Close() error               { p.page = nil; return nil }

func (p *pagedRows) Next(ctx context.Context) (model.Row, error) {
	for p.i >= len(p.page) {
		if p.last {
			return nil, io.EOF
		}
		rows, err := p.f.Fetch(ctx, p.next, exportPage)
		if err != nil {
			return nil, err
		}
		p.page, p.i = rows, 0
		p.next += int64(len(rows))
		p.last = len(rows) < exportPage
	}
	row := p.page[p.i]
	p.page[p.i] = nil // let the row go as soon as it is written
	p.i++
	return row, nil
}
