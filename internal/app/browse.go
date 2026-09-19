package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// BrowseSource feeds the data grid from a live connection: it has exactly the
// Columns / Fetch / Count shape the grid's Fetcher interface asks for.
//
// It lives here, not in the grid, because the grid must not talk to drivers
// (ARCH-2). Go's structural typing means this package satisfies the grid's
// interface without importing it, so the dependency still points downward.
type BrowseSource struct {
	src      source.Source
	ref      model.ObjectRef
	opt      source.BrowseOptions
	cols     []model.ColumnDef
	identity model.RowIdentity
}

// NewBrowseSource opens an object for browsing. It reads one row up front,
// because the grid needs the column shape before it fetches anything, and the
// browse itself is the authority on what shape rows will have.
func NewBrowseSource(ctx context.Context, src source.Source, ref model.ObjectRef, opt source.BrowseOptions) (_ *BrowseSource, err error) {
	defer panics.Recover(&err, "opening the rows")
	probe := opt
	probe.Offset, probe.Limit = 0, 1
	rs, err := src.Browse(ctx, ref, probe)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	b := &BrowseSource{src: src, ref: ref, opt: opt, cols: rs.Columns(),
		identity: model.RowIdentity{Kind: model.IdentityNone}}
	if id, ok := rs.(model.Identified); ok {
		b.identity = id.Identity()
	}
	return b, nil
}

// Options is how the browse is filtered and sorted.
func (b *BrowseSource) Options() source.BrowseOptions { return b.opt }

// With is the same object browsed another way: a new sort or filter. It
// probes the source again, so an option it cannot honour fails here rather
// than while the grid pages (REQ-DRV-3).
func (b *BrowseSource) With(ctx context.Context, opt source.BrowseOptions) (*BrowseSource, error) {
	return NewBrowseSource(ctx, b.src, b.ref, opt)
}

// CanSort reports whether the source sorts on the server (capability.Data).
func (b *BrowseSource) CanSort() bool { return b.src.Capabilities().Data.ServerSort }

// CanFilter reports whether the source filters on the server (capability.Data).
func (b *BrowseSource) CanFilter() bool { return b.src.Capabilities().Data.ServerFilter }

// FiltersHere reports whether the grid filters the rows it has read, the
// source not filtering them (FR-13.9, ADR-0099).
//
// A broker hands over bytes and asks no questions about them, so a topic's
// records are filtered here. Every other paradigm that cannot filter on the
// server is a separate decision — what such a grid may claim differs by what
// a partial read of it means — and is deliberately left open.
func (b *BrowseSource) FiltersHere() bool {
	c := b.src.Capabilities()
	return !c.Data.ServerFilter && c.Paradigm == model.ParadigmStream
}

// CanListValues reports whether the source can list a column's distinct
// values, for the filter picklist.
func (b *BrowseSource) CanListValues() bool {
	_, ok := b.src.(source.DistinctLister)
	return ok && b.src.Capabilities().Data.DistinctValues
}

// Distinct lists up to limit of a column's values for the filter picklist
// (FR-3.4), among the rows the other columns' filters and the typed WHERE
// select. The column's own filter is left out: with it, the list could only
// offer what is already picked.
func (b *BrowseSource) Distinct(ctx context.Context, column string, limit int) (_ []source.DistinctValue, err error) {
	defer panics.Recover(&err, "listing a column's values")
	dl, ok := b.src.(source.DistinctLister)
	if !ok || !b.CanListValues() {
		return nil, errors.New("app: this source cannot list a column's values")
	}
	var others []source.Filter
	for _, f := range b.opt.Filters {
		if f.Column != column {
			others = append(others, f)
		}
	}
	return dl.Distinct(ctx, b.ref, column, source.BrowseOptions{Filters: others, Where: b.opt.Where}, limit)
}

// CanScriptRows reports whether rows can be copied as INSERT statements.
func (b *BrowseSource) CanScriptRows() bool {
	_, ok := b.src.(source.RowScripter)
	return ok
}

// InsertRows writes rows of this object as the INSERT statements that would
// add them (FR-3.7). The dialect writes the values (ARCH-2).
func (b *BrowseSource) InsertRows(cols []model.ColumnDef, rows []model.Row) (_ string, err error) {
	defer panics.Recover(&err, "writing INSERT statements")
	rs, ok := b.src.(source.RowScripter)
	if !ok {
		return "", errors.New("app: this source cannot write rows as statements")
	}
	return rs.InsertRows(b.ref, cols, rows)
}

// CanOpenRows reports whether a row of this object is an object of its own —
// a Redis key in a database's keyspace (capability.Data.RowObjects, FR-12.2).
func (b *BrowseSource) CanOpenRows() bool {
	_, ok := b.src.(source.RowObject)
	return ok && b.src.Capabilities().Data.RowObjects
}

// ObjectOf is what a row names, or false where it names nothing. The source
// answers it: what a row is is as much the engine's business as what its
// columns are (REQ-DB-1).
func (b *BrowseSource) ObjectOf(row model.Row) (_ model.ObjectRef, _ bool) {
	defer panics.Catch("reading what a row names", func(error) {})
	ro, ok := b.src.(source.RowObject)
	if !ok || !b.CanOpenRows() {
		return model.ObjectRef{}, false
	}
	return ro.ObjectOf(b.ref, b.cols, row)
}

// CanPipeline reports whether the source reads by a pipeline of stages
// (capability.Data.Pipeline, FR-12.1).
func (b *BrowseSource) CanPipeline() bool { return b.src.Capabilities().Data.Pipeline }

// CanWhere reports whether the grid can take a WHERE clause typed by the
// person (FR-3.6): the source needs a query language to write it in.
func (b *BrowseSource) CanWhere() bool {
	_, ok := b.src.(source.Dialect)
	return ok
}

// Columns describes every row.
func (b *BrowseSource) Columns() []model.ColumnDef { return b.cols }

// errNoWrites refuses the changes of a source that does not write rows.
var errNoWrites = errors.New("app: this source does not write rows")

// Identity reports whether browsed rows can be addressed for editing.
func (b *BrowseSource) Identity() model.RowIdentity { return b.identity }

// Plan renders a changeset as the statements that would write it, running
// nothing (FR-4.4, ADR-0031).
func (b *BrowseSource) Plan(ctx context.Context, cs source.Changeset) (_ *source.WritePlan, err error) {
	defer panics.Recover(&err, "planning the changes")
	w, ok := b.src.(source.Writer)
	if !ok {
		return nil, errNoWrites
	}
	return w.Plan(ctx, cs)
}

// Apply writes a plan: in one transaction where the source has them
// (FR-4.5, ADR-0031).
func (b *BrowseSource) Apply(ctx context.Context, plan *source.WritePlan) (_ *source.WriteOutcome, err error) {
	defer panics.Recover(&err, "writing the changes")
	w, ok := b.src.(source.Writer)
	if !ok {
		return nil, errNoWrites
	}
	return w.Apply(ctx, plan)
}

// LoadRows imports rows into an object in bulk (FR-10.6, ADR-0050).
func (b *BrowseSource) LoadRows(ctx context.Context, target model.ObjectRef, columns []string, rows model.RowStream, opt source.LoadOptions) (_ int64, err error) {
	defer panics.Recover(&err, "loading rows")
	l, ok := b.src.(source.BulkLoader)
	if !ok {
		return 0, errors.New("app: this source cannot load rows in bulk")
	}
	return l.LoadRows(ctx, target, columns, rows, opt)
}

// Ref is the object being browsed.
func (b *BrowseSource) Ref() model.ObjectRef { return b.ref }

// Fetch reads one window of rows. The stream is always closed, whatever
// happens, because each open stream holds a server connection.
func (b *BrowseSource) Fetch(ctx context.Context, offset, limit int64) (_ []model.Row, err error) {
	defer panics.Recover(&err, "reading rows")
	opt := b.opt
	opt.Offset, opt.Limit = offset, limit
	rs, err := b.src.Browse(ctx, b.ref, opt)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := make([]model.Row, 0, limit)
	for {
		row, err := rs.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if int64(len(out)) >= limit {
			// A source that ignores Limit must not grow this page without
			// bound (NFR-P11). Stop, and say so rather than truncate quietly.
			return nil, fmt.Errorf("app: %s returned more than the %d rows asked for", b.ref, limit)
		}
		out = append(out, row)
	}
}

// Count returns the total, or -1 when counting would cost a full scan. On a
// ten-million-row table COUNT(*) is exactly that, and the grid pages
// perfectly well without it; it just cannot draw a proportional scrollbar.
func (b *BrowseSource) Count(ctx context.Context) (_ int64, err error) {
	defer panics.Recover(&err, "counting rows")
	c, ok := b.src.(source.Countable)
	if !ok || !b.src.Capabilities().Data.ExactCount {
		return -1, nil
	}
	return c.Count(ctx, b.ref, b.opt)
}

// Statement returns the SQL behind this browse, for the grid to show
// (UX principle 6, FR-3.6). False for sources with no statement language.
func (b *BrowseSource) Statement() (st source.Statement, ok bool) {
	defer panics.Catch("building the statement", func(error) { st, ok = source.Statement{}, false })
	d, ok := b.src.(source.Dialect)
	if !ok {
		return source.Statement{}, false
	}
	st, err := d.BuildBrowse(b.ref, b.opt)
	return st, err == nil
}
