package source

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Writer applies row changes.
//
// It is OPTIONAL: a source that cannot write, or a log where records are
// immutable, does not implement it, and the grid refuses editing because
// capability.Data reports no write support.
//
// The contract is deliberately two-step. Plan renders exactly what would run
// without running it, and Apply executes a plan. This is what makes FR-4.4's
// mandatory preview structural rather than a courtesy the UI might skip.
type Writer interface {
	// Plan renders the operations a changeset would perform.
	//
	// It must not contact the server for anything with side effects, and must
	// return an error rather than a partial plan if any change cannot be
	// expressed — a changeset that would silently drop an edit is worse than
	// one that refuses.
	Plan(ctx context.Context, cs Changeset) (*WritePlan, error)

	// Apply executes a previously rendered plan.
	//
	// When capability.Data.TransactionalWrite is true this is atomic: on any
	// failure nothing is applied. Otherwise Apply stops at the first failure
	// and reports how many operations had already succeeded, so the UI can
	// tell the user exactly what state they are in (FR-4.5).
	Apply(ctx context.Context, plan *WritePlan) (*WriteOutcome, error)
}

// ChangeKind is the type of a pending row change.
type ChangeKind uint8

const (
	ChangeInsert ChangeKind = iota
	ChangeUpdate
	ChangeDelete
)

// Changeset is a set of pending edits accumulated in the grid (FR-4.3).
//
// Nothing here has touched the server: a changeset is purely local until
// Plan and Apply are called.
type Changeset struct {
	// Target is the object being written.
	Target model.ObjectRef

	// Identity is how rows in this changeset are addressed. A changeset whose
	// Identity is not Editable must be refused at construction (FR-4.7).
	Identity model.RowIdentity

	Changes []RowChange

	// Confirmed records explicit user consent for writing to a production
	// connection (FR-4.9).
	Confirmed bool
}

// RowChange is one pending insert, update or delete.
type RowChange struct {
	Kind ChangeKind

	// Key holds the identity column values addressing the row, in the order
	// given by Changeset.Identity.Columns. Empty for an insert.
	Key []any

	// Values holds the new column values, keyed by column name. For an update
	// it contains only the columns the user actually changed, so that an edit
	// to one cell does not rewrite the whole row and clobber a concurrent
	// change to a column the user never touched.
	Values map[string]any
}

// WritePlan is the rendered, reviewable form of a changeset (FR-4.4).
type WritePlan struct {
	// Target is the object the plan writes, as its changeset's. A driver
	// with a connection per database reads it to find the one to write on.
	Target model.ObjectRef

	// Statements are the operations in execution order. For sources with a
	// query language these carry SQL; for others the text is a faithful
	// human-readable rendering of the driver call that will be made, because
	// the user is entitled to see what will happen either way.
	Statements []Statement

	// Descriptions parallel Statements, giving a one-line summary for sources
	// whose native form is not readable on its own.
	Descriptions []string

	// Atomic reports whether Apply will run this as a single transaction.
	Atomic bool

	// Guarded reports whether the plan requires explicit confirmation before
	// Apply will accept it.
	Guarded bool
}

// WriteOutcome reports the result of applying a plan.
type WriteOutcome struct {
	// Applied is the number of statements that succeeded.
	Applied int

	// Affected is the total row count reported by the server, -1 when unknown.
	Affected int64

	// RolledBack reports whether a failure was fully undone. When false after
	// an error, Applied statements remain in effect and the user must be told
	// precisely that.
	RolledBack bool

	// FailedAt is the index of the failing statement, -1 on success. It is
	// what lets the UI point at the offending row rather than the whole grid.
	FailedAt int

	Err error
}
