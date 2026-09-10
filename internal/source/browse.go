package source

import (
	"context"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Browser opens an object as rows.
//
// This is the paradigm-neutral data path, and it is required of every source.
// When the user opens a table, a collection, a key pattern or a topic, the UI
// calls Browse — no query text is involved, and the UI does not know or care
// which paradigm answered.
//
// The interface exists because of T0.29. The original contract assumed the
// grid was fed by a query language, which is true for relational sources and
// false for Kafka, Redis and document stores. Rather than special-casing those,
// Browse became the required path and Queryer became optional. This is the
// difference between a log being a first-class source and a bolted-on one.
type Browser interface {
	// Browse opens a row stream over an object.
	//
	// The returned stream must honour opt as far as the source's capabilities
	// allow, and must ignore nothing silently: if opt requests server-side
	// sorting the source cannot perform, Browse returns an error rather than
	// returning unsorted rows that the UI would present as sorted.
	Browse(ctx context.Context, ref model.ObjectRef, opt BrowseOptions) (model.RowStream, error)
}

// BrowseOptions parameterises a browse.
//
// Fields not supported by a source's capabilities must be rejected, not
// ignored — see the note on Browse.
type BrowseOptions struct {
	// Columns restricts the projection. Empty means all columns.
	Columns []string

	// Filters are combined with AND (FR-3.4, FR-3.5).
	Filters []Filter

	// Sorts are applied in order (FR-3.3).
	Sorts []Sort

	// Offset and Limit window the result. Limit of 0 means the source's
	// default page size; the grid always supplies one, because NFR-P11
	// forbids unbounded reads.
	Offset int64
	Limit  int64

	// Seek positions a stream/log read. Ignored by non-log sources.
	Seek *Seek

	// Follow keeps the stream open, blocking in Next until new records arrive
	// rather than returning io.EOF (FR-13.6, FR-12.5).
	//
	// A following stream must be bounded by the caller: the grid holds a ring
	// buffer, so a fast topic cannot exhaust memory (FR-13.20, NFR-P10).
	Follow bool
}

// FilterOp is a comparison operator.
type FilterOp string

const (
	OpEqual        FilterOp = "="
	OpNotEqual     FilterOp = "!="
	OpLess         FilterOp = "<"
	OpLessEqual    FilterOp = "<="
	OpGreater      FilterOp = ">"
	OpGreaterEqual FilterOp = ">="
	OpLike         FilterOp = "like"
	OpNotLike      FilterOp = "not like"
	OpIn           FilterOp = "in"
	OpNotIn        FilterOp = "not in"
	OpIsNull       FilterOp = "is null"
	OpIsNotNull    FilterOp = "is not null"
	OpBetween      FilterOp = "between"
	OpRegex        FilterOp = "regex"
	OpContains     FilterOp = "contains"
)

// Filter is one column predicate.
//
// Values are carried separately from the operator and are always bound as
// parameters by the source, never interpolated into statement text (NFR-S6).
type Filter struct {
	Column string
	Op     FilterOp

	// Values holds the operands: one for most operators, two for OpBetween,
	// many for OpIn, none for the null tests.
	Values []any

	// Negate inverts the predicate.
	Negate bool
}

// Sort is one ordering term.
type Sort struct {
	Column     string
	Descending bool

	// NullsFirst is honoured by sources whose capability set reports support;
	// others ignore it, since it does not change which rows are returned.
	NullsFirst bool
}

// SeekMode selects where a log read begins (FR-13.5).
type SeekMode uint8

const (
	// SeekBeginning starts at the earliest retained offset.
	SeekBeginning SeekMode = iota

	// SeekEnd starts at the high watermark, reading only new records.
	SeekEnd

	// SeekLast starts N records back from the high watermark.
	SeekLast

	// SeekOffset starts at an explicit offset.
	SeekOffset

	// SeekTimestamp starts at the first record at or after a time.
	SeekTimestamp
)

// Seek positions a log read.
type Seek struct {
	Mode SeekMode

	// Count is the number of records back from the end, for SeekLast.
	Count int64

	// Offset is the starting offset, for SeekOffset.
	Offset int64

	// Time is the starting timestamp, for SeekTimestamp.
	Time time.Time

	// Partitions restricts the read to specific partitions. Empty means all.
	//
	// Reads always use explicit partition assignment and never join a consumer
	// group or commit offsets, so that inspecting a topic cannot perturb
	// production consumers (FR-13.19). That guarantee is a property of every
	// implementation, not an option the caller sets.
	Partitions []int32
}

// Countable is an optional Browser refinement for sources that can count the
// rows a browse would return, so the grid can show a real scrollbar.
type Countable interface {
	// Count returns the number of rows matching opt's filters. Sources where
	// counting is a full scan should report their inability through
	// capability.Data.ExactCount rather than performing it.
	Count(ctx context.Context, ref model.ObjectRef, opt BrowseOptions) (int64, error)
}

// DistinctLister is an optional Browser refinement supplying the Excel-style
// filter picklist (FR-3.4).
type DistinctLister interface {
	// Distinct returns up to limit distinct values of a column, with their
	// frequencies when cheaply available.
	Distinct(ctx context.Context, ref model.ObjectRef, column string, limit int) ([]DistinctValue, error)
}

// DistinctValue is one entry of a filter picklist.
type DistinctValue struct {
	Value any
	// Count is -1 when frequency was not computed.
	Count int64
}
