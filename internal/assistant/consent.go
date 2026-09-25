// Package assistant asks a language model about a database (FR-14).
//
// Everything here is off until somebody turns it on, and what it may see is
// theirs to decide twice over: once for the application and once for the
// connection. That is not a courtesy — it is the whole of FR-14.4, and it is
// enforced here rather than in the window, for the reason read-only mode is
// (NFR-S4): a disabled button is a courtesy and not a control.
//
// This package is the second place in the application that reaches the network
// — the first is internal/cloud, which mints a cloud token — and the offline
// rule says so by name (NFR-D6, cmd/ikigai/offline_test.go). Nothing here is
// contacted unless somebody asks it something.
package assistant

import (
	"errors"
	"fmt"
	"strings"
)

// Consent is what somebody has agreed to. Every field is off in its zero
// value, which is the point: a Consent nobody filled in allows nothing.
type Consent struct {
	// Enabled is the application's own setting. Off by default (FR-14.4):
	// there is no assistant at all until somebody turns it on.
	Enabled bool

	// Connection is whether this connection has been opted in. Per
	// connection, because a person may be happy to ask about a scratch
	// database and not about their employer's.
	Connection bool

	// Data is whether values may be sent, as against names. Off by default:
	// a schema is names, and names are what grounding needs (FR-14.3).
	Data bool

	// Production says this connection is marked production, which is a
	// stricter case: its data may not be sent without consent given for this
	// session, whatever the Data toggle says (FR-14.4).
	Production bool

	// Confirmed is that per-session consent, given for one act rather than
	// remembered. Nothing writes it to settings.
	Confirmed bool
}

// The refusals. Each is one a person can act on, and each names which of the
// several switches is the one that is off.
var (
	ErrOff                   = errors.New("the assistant is off; turn it on in Settings")
	ErrConnectionOff         = errors.New("this connection has not been opted in to the assistant")
	ErrDataOff               = errors.New("sending data to the assistant is off for this connection; it can be asked about the schema")
	ErrProductionUnconfirmed = errors.New("this is a production connection, so sending its data needs consent for this session")
	ErrNoProvider            = errors.New("no assistant provider is configured")
)

// Allow reports whether a request may be made, given whether it would send
// data as well as names.
//
// The order is deliberate: the application's own switch first, then the
// connection's, then the data. Somebody who has not turned the assistant on at
// all should be told that rather than told about a data toggle they have never
// seen.
func (c Consent) Allow(sendsData bool) error {
	switch {
	case !c.Enabled:
		return ErrOff
	case !c.Connection:
		return ErrConnectionOff
	case !sendsData:
		// Names only, which is what grounding is and what the default allows.
		return nil
	case !c.Data:
		return ErrDataOff
	case c.Production && !c.Confirmed:
		// The stricter case, and the one worth being awkward about: a
		// production connection's data goes nowhere on a setting alone.
		return ErrProductionUnconfirmed
	}
	return nil
}

// AllowsData reports whether values may be sent, which is what a caller asks
// before deciding whether to gather any: reading rows to send and then being
// refused would be reading rows for nothing.
func (c Consent) AllowsData() bool { return c.Allow(true) == nil }

// Describe says in one line what the assistant may see, for the window to show
// beside the provider and the model (FR-14.5): somebody about to ask a question
// should be able to read what leaves the machine without opening Settings.
func (c Consent) Describe() string {
	switch {
	case !c.Enabled:
		return "The assistant is off."
	case !c.Connection:
		return "The assistant is off for this connection."
	case c.AllowsData():
		return "It may see the schema and the rows you send it."
	case c.Data && c.Production && !c.Confirmed:
		return "It may see the schema. This is a production connection, so sending data needs consent each session."
	}
	return "It may see the schema — table and column names — and not the data."
}

// Request is one question, and what the model is told in order to answer it.
type Request struct {
	// Kind says what is being asked for, which decides the instructions the
	// model is given and how its answer is read.
	Kind Kind

	// Question is what the person typed, or the statement to explain.
	Question string

	// Grounding is what the model is told about the database (FR-14.1). Names
	// only unless Rows is set.
	Grounding Grounding
}

// Kind is what is being asked.
type Kind string

const (
	// KindQuery asks for a statement: natural language in, SQL out (FR-14.1).
	KindQuery Kind = "query"

	// KindExplain asks what a statement does, in words (FR-14.2).
	KindExplain Kind = "explain"

	// KindExplainPlan asks what a query plan means (FR-14.2).
	KindExplainPlan Kind = "plan"

	// KindChat is a question about the schema, answered in words (FR-14.3).
	KindChat Kind = "chat"
)

var kinds = []Kind{KindQuery, KindExplain, KindExplainPlan, KindChat}

// SendsData reports whether a request would send values rather than only
// names. It is the request that decides, not the caller: a caller that
// mis-stated it would send data under a consent that did not cover it.
func (r Request) SendsData() bool { return len(r.Grounding.Rows) > 0 }

// Valid says what is wrong with a request, or nothing.
func (r Request) Valid() error {
	if !contains(kinds, r.Kind) {
		return fmt.Errorf("%q is not something the assistant is asked for", r.Kind)
	}
	if strings.TrimSpace(r.Question) == "" {
		switch r.Kind {
		case KindExplain, KindExplainPlan:
			return errors.New("there is nothing to explain")
		}
		return errors.New("there is no question")
	}
	return nil
}

func contains[T comparable](list []T, want T) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
