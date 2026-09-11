package source

import (
	"context"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// This file collects the remaining optional interfaces. Every one is
// discovered by type assertion on a Source, and every one has a capability
// field so the UI can decide what to offer without performing the assertion
// itself.

// BulkLoader is an optional fast path for importing many rows (FR-10.6).
//
// Sources implementing it use their native bulk protocol — Postgres COPY,
// ClickHouse native insert — instead of batched INSERTs, which is the
// difference between meeting NFR-P7 and missing it by an order of magnitude.
type BulkLoader interface {
	// LoadRows streams rows into a target. The source pulls from rows until
	// it returns io.EOF, so memory stays flat regardless of input size.
	LoadRows(ctx context.Context, target model.ObjectRef, columns []string, rows model.RowStream, opt LoadOptions) (int64, error)
}

// LoadOptions parameterises a bulk load.
type LoadOptions struct {
	// BatchSize is the source's commit granularity; 0 selects its default.
	BatchSize int

	// OnError selects the failure policy: "abort" (or empty), which stops at
	// a row refused; "skip", which leaves it out and goes on; or "collect",
	// which leaves out up to MaxErrors rows and stops at the next.
	OnError string

	// MaxErrors caps the rows the "collect" policy leaves out. It must be
	// given with it.
	MaxErrors int

	// Skipped, when given, is told of each row left out, as it is.
	Skipped func(*LoadError)

	// Truncate empties the target first. Always a guarded operation.
	Truncate bool

	// Keys, when given, are the columns of a key: a row whose key is taken
	// already updates the row there instead of being refused. It cannot go
	// with Truncate.
	Keys []string

	Confirmed bool
}

// LoadError is a load stopped at one of its rows: the row's place among the
// rows given, from 1, and why. The row's transaction was rolled back.
type LoadError struct {
	Row int64
	Err error
}

func (e *LoadError) Error() string { return fmt.Sprintf("row %d: %v", e.Row, e.Err) }
func (e *LoadError) Unwrap() error { return e.Err }

// Scriptable renders an object as the statements that would recreate it
// (FR-2.4 "script as", FR-6.7).
type Scriptable interface {
	// ScriptObject returns DDL recreating the object.
	ScriptObject(ctx context.Context, ref model.ObjectRef) ([]Statement, error)

	// ScriptSelect, ScriptInsert and ScriptUpdate render skeleton DML for an
	// object, which is what the explorer's "script as" menu offers.
	ScriptSelect(ctx context.Context, ref model.ObjectRef) (Statement, error)
	ScriptInsert(ctx context.Context, ref model.ObjectRef) (Statement, error)
	ScriptUpdate(ctx context.Context, ref model.ObjectRef) (Statement, error)
}

// Completer supplies schema-aware completion candidates to the editor
// (FR-5.2).
//
// Completion is a source concern rather than a UI one because resolving an
// alias to its table requires knowing the dialect's scoping rules.
type Completer interface {
	// Complete returns candidates for a cursor position within statement text.
	Complete(ctx context.Context, req CompletionRequest) ([]Completion, error)
}

// CompletionRequest describes where completion was invoked.
type CompletionRequest struct {
	// Text is the full statement, and Cursor the 0-based rune offset within
	// it. The whole statement is supplied because alias resolution needs the
	// FROM clause even when the cursor is in the SELECT list.
	Text   string
	Cursor int

	// Database and Schema are the session's current context.
	Database string
	Schema   string

	Limit int
}

// CompletionKind classifies a candidate, driving its icon and ranking.
type CompletionKind uint8

const (
	CompletionKeyword CompletionKind = iota
	CompletionTable
	CompletionView
	CompletionColumn
	CompletionFunction
	CompletionSchema
	CompletionDatabase
	CompletionAlias
	CompletionSnippet
)

// Completion is one candidate.
type Completion struct {
	Kind CompletionKind

	// Label is shown in the popup; Insert is what is written, which differs
	// when an identifier needs quoting.
	Label  string
	Insert string

	// Detail is secondary text — a column's type, a function's signature.
	Detail string

	// Score ranks the candidate; higher sorts first.
	Score int
}

// Killer is an optional interface for cancelling work server-side.
//
// Separate from context cancellation because several engines require a second
// connection to cancel a query on the first. Sources reporting
// capability.Query.Cancel must implement it; without it a cancel button would
// only detach the UI while the server kept working, which FR-5.5 forbids.
type Killer interface {
	// KillQuery cancels a running statement on the server.
	KillQuery(ctx context.Context, handle string) error
}
