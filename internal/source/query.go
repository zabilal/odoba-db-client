package source

import (
	"context"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Queryer executes statement text.
//
// It is OPTIONAL. Sources without a query language — Kafka, Redis in its
// browsing mode — simply do not implement it, and the UI offers no query
// editor for them because capability.Query.Supported is false.
type Queryer interface {
	// Query executes a single statement and returns its result.
	//
	// The statement's Access is classified by the Dialect and checked against
	// the connection Guard before execution; a driver must never bypass that.
	Query(ctx context.Context, stmt Statement) (*Result, error)

	// QueryMulti executes a script that may contain several statements,
	// delivering results as they complete (FR-5.4).
	//
	// Results arrive on the returned channel, which is closed when the script
	// finishes or ctx is cancelled. Streaming them rather than collecting them
	// lets the UI show the first result while later statements still run.
	QueryMulti(ctx context.Context, script string) (<-chan ScriptResult, error)
}

// Statement is one statement plus its bound parameters.
type Statement struct {
	SQL string

	// Args are bound as parameters, never interpolated (NFR-S6).
	Args []any

	// Named holds named parameters for dialects that support them (FR-5.7).
	Named map[string]any

	// Confirmed records explicit user consent for a mutating statement on a
	// production connection (FR-4.9).
	Confirmed bool
}

// Result is the outcome of one statement.
//
// Rows is nil for statements that return no result set; Affected is -1 when
// the engine does not report a count.
type Result struct {
	Rows model.RowStream

	Affected int64
	Duration time.Duration

	// Messages carries server notices, warnings and print output (FR-5.6).
	Messages []Message
}

// MessageLevel classifies a server message.
type MessageLevel uint8

const (
	MessageInfo MessageLevel = iota
	MessageWarning
	MessageError
)

// Message is one server notice.
type Message struct {
	Level MessageLevel
	Text  string

	// Code is the engine's own error or notice code, preserved verbatim so
	// the user can search for it.
	Code string

	// Position is the 1-based character offset in the statement the message
	// refers to, or 0 when not applicable. It is what lets the editor
	// underline the offending token (FR-5.10).
	Position int
}

// ScriptResult pairs a result with the statement that produced it.
type ScriptResult struct {
	// Index is the statement's 0-based position in the script.
	Index int

	// Offset is the statement's character offset in the script text, so the
	// editor can map an error back to the right line (FR-5.10).
	Offset int

	Statement string

	Result *Result
	Err    error
}

// Explainer is an optional Queryer refinement returning a query plan
// (FR-5.13). Kept separate from Queryer because plan retrieval differs enough
// per engine that folding it in would force every driver to stub it.
type Explainer interface {
	// Explain returns the plan for a statement. When analyze is true the
	// statement is actually executed, which for a mutating statement is a
	// write and must be guarded accordingly.
	Explain(ctx context.Context, stmt Statement, analyze bool) (*Plan, error)
}

// Plan is a query-execution plan rendered as a tree.
type Plan struct {
	Root *PlanNode

	// Text is the engine's raw plan output, always retained so the user can
	// read what the server actually said rather than only our rendering.
	Text string
}

// PlanNode is one step of a query plan.
type PlanNode struct {
	Operation string
	Detail    string

	// EstimatedCost and EstimatedRows are -1 when not reported.
	EstimatedCost float64
	EstimatedRows int64

	// ActualRows and ActualTime are populated only for an analyzed plan;
	// ActualRows is -1 otherwise. The gap between estimated and actual is the
	// most useful thing on the screen, so both are kept.
	ActualRows int64
	ActualTime time.Duration

	Children []*PlanNode
}

// Transactor is an optional Queryer refinement for explicit transaction
// control (FR-5.14). The UI shows a persistent indicator whenever a
// transaction is open, because an unnoticed open transaction holds locks.
type Transactor interface {
	Begin(ctx context.Context) error
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error

	// InTransaction reports whether a transaction is currently open.
	InTransaction() bool
}
