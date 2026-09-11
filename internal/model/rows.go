package model

import (
	"context"
	"time"
)

// Row is one record.
//
// A nil element is NULL, which the UI renders distinctly from an empty string
// (FR-3.8, UX-7). Elements must be one of the following concrete types, so
// that renderers and editors form a closed set:
//
//	bool, int64, float64, Decimal, string, []byte, time.Time,
//	JSON, []any (arrays), map[string]any (documents/structs)
//
// Drivers are responsible for narrowing engine types to this set; the UI never
// type-switches on anything outside it.
type Row []any

// Decimal carries an exact numeric as text, so that no precision is lost
// between the server and the display.
type Decimal string

// JSON carries pre-encoded JSON that the UI pretty-prints on expansion.
type JSON []byte

// ColumnDef describes one column of a RowStream.
type ColumnDef struct {
	Name string
	Type DataType

	// Origin identifies the object this column was read from, when the source
	// can determine it. Editable query results (FR-4.8) require every selected
	// column to share a single origin.
	Origin ObjectRef

	// OriginColumn is this column's name in Origin, where the source knows
	// it: a query can rename a column ("SELECT name AS label"), and a change
	// is written by the table's name for it (FR-4.8, ADR-0035).
	OriginColumn string

	// ReadOnly marks a column that cannot be written even when the stream is
	// otherwise editable — a computed column, or a Kafka offset.
	ReadOnly bool

	Comment string
}

// SourceName is the column's name where it is stored: its name in its
// origin when known, else its name here.
func (c ColumnDef) SourceName() string {
	if c.OriginColumn != "" {
		return c.OriginColumn
	}
	return c.Name
}

// RowStream is how every paradigm delivers tabular data to the UI.
//
// Relational result sets, Mongo documents, Redis key listings and Kafka
// messages all arrive through this one interface. It is the reason the grid
// has no per-source branches (REQ-DB-3, REQ-DB-4).
//
// Implementations must stream: memory must not scale with the number of rows
// available (NFR-P11). Next must honour ctx cancellation promptly (NFR-P9).
type RowStream interface {
	// Columns describes the shape of every Row returned by Next. It must be
	// stable for the life of the stream.
	Columns() []ColumnDef

	// Next returns the next row, or io.EOF when the stream is exhausted.
	//
	// A following stream (BrowseOptions.Follow) blocks until a new record
	// arrives or ctx is cancelled, and never returns io.EOF.
	Next(ctx context.Context) (Row, error)

	// Close releases the stream. It is safe to call more than once.
	Close() error
}

// IdentityKind describes what gives a row its identity, and therefore whether
// and how it can be edited (FR-4.7).
type IdentityKind uint8

const (
	// IdentityNone means rows cannot be addressed individually. Editing must
	// be refused with an explanation, and the user offered the chance to
	// nominate a key.
	IdentityNone IdentityKind = iota

	IdentityPrimaryKey  // relational primary key
	IdentityUniqueIndex // relational unique index over non-null columns
	IdentityRowID       // engine-internal row address (SQLite rowid, ctid, ...)
	IdentityDocumentID  // document _id
	IdentityKeyName     // key-value key
	IdentityLogOffset   // stream partition+offset — addressable but immutable

	// IdentityChosen is columns a person named as the key of rows that have
	// none (FR-4.7). Nothing says they are unique, so a write that would
	// change more than one row is refused (ADR-0034).
	IdentityChosen
)

// Mutable reports whether rows identified this way may be updated in place.
// Log records are addressable but append-only, so they are readable and
// exportable yet never editable.
func (k IdentityKind) Mutable() bool {
	switch k {
	case IdentityPrimaryKey, IdentityUniqueIndex, IdentityRowID,
		IdentityDocumentID, IdentityKeyName, IdentityChosen:
		return true
	}
	return false
}

// RowIdentity describes how to address a single row of a stream for editing.
type RowIdentity struct {
	Kind IdentityKind

	// Columns are the stream columns forming the identity, in order.
	Columns []string

	// Target is the object an edit would be written back to.
	Target ObjectRef
}

// Editable reports whether a changeset may be built against this identity.
func (id RowIdentity) Editable() bool {
	return id.Kind.Mutable() && len(id.Columns) > 0 && !id.Target.IsZero()
}

// RowState is how a row stands in changes not yet written (FR-4.3).
type RowState uint8

const (
	RowUnchanged RowState = iota
	RowModified
	RowDeleted
	RowAdded
)

// Default stands, in a new row not yet written, for a column given no value:
// the store gives that column its default (FR-4.2).
type Default struct{}

func (Default) String() string { return "DEFAULT" }

// Identified is an optional RowStream refinement exposing row identity.
//
// A stream that does not implement it is treated as read-only, which is the
// safe default: FR-4.7 requires refusing to edit rows we cannot address.
type Identified interface {
	RowStream
	Identity() RowIdentity
}

// Counted is an optional RowStream refinement for sources that can report a
// total cheaply, letting the UI show a real scrollbar rather than an estimate.
type Counted interface {
	RowStream
	// Total returns the row count and true, or false when unknown.
	Total() (int64, bool)
}

// Progressing is an optional RowStream refinement for long streams that can
// report how far through they are — used by the task centre (FR-15.6).
type Progressing interface {
	RowStream
	// Progress returns rows delivered so far and, when known, the elapsed
	// time the source has spent producing them.
	Progress() (rows int64, elapsed time.Duration)
}
