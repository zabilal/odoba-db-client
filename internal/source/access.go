package source

import (
	"errors"
	"fmt"
)

// Access classifies what an operation does to a source.
//
// Every operation that reaches a driver declares its Access, and the guard
// below is the single chokepoint enforcing read-only mode. NFR-S4 requires
// this to live in the data layer, not the UI: a disabled button is a courtesy,
// not a control.
type Access uint8

const (
	// AccessRead reads data or metadata and changes nothing.
	AccessRead Access = iota

	// AccessWrite modifies data: INSERT/UPDATE/DELETE, producing a record,
	// setting a key.
	AccessWrite

	// AccessDDL modifies structure: CREATE/ALTER/DROP/TRUNCATE, creating or
	// deleting a topic.
	AccessDDL

	// AccessAdmin modifies server or cluster state: resetting consumer-group
	// offsets, altering configuration, killing sessions.
	AccessAdmin
)

var accessNames = map[Access]string{
	AccessRead:  "read",
	AccessWrite: "write",
	AccessDDL:   "DDL",
	AccessAdmin: "admin",
}

func (a Access) String() string {
	if n, ok := accessNames[a]; ok {
		return n
	}
	return "unknown"
}

// Mutating reports whether the operation changes anything.
func (a Access) Mutating() bool { return a != AccessRead }

// Environment tags a connection so that risk is visible and enforceable
// (FR-1.7). It drives colour treatment in the UI and confirmation policy here.
type Environment string

const (
	EnvLocal      Environment = "local"
	EnvDev        Environment = "dev"
	EnvStaging    Environment = "staging"
	EnvProduction Environment = "production"
)

// Valid reports whether e is a known environment.
func (e Environment) Valid() bool {
	switch e {
	case EnvLocal, EnvDev, EnvStaging, EnvProduction:
		return true
	}
	return false
}

func (e Environment) String() string { return string(e) }

// ErrReadOnly is returned when a mutating operation is attempted on a
// connection the user marked read-only.
var ErrReadOnly = errors.New("connection is read-only")

// ErrConfirmationRequired is returned when a mutating operation on a
// production connection has not been explicitly confirmed. It is not a
// failure: the caller is expected to obtain typed confirmation from the user
// and retry with Confirmed set (FR-4.9).
var ErrConfirmationRequired = errors.New("operation requires explicit confirmation")

// Guard enforces connection-level write policy. It is constructed from the
// connection's configuration and consulted before every driver operation.
type Guard struct {
	// ReadOnly refuses every mutating operation (FR-1.8).
	ReadOnly bool

	// Environment determines whether confirmation is required.
	Environment Environment
}

// Allow reports whether an operation may proceed.
//
// confirmed records that the user has given explicit, operation-specific
// consent — a typed confirmation, not a remembered preference. Consent is
// never cached across operations: FR-4.9 requires it per write.
func (g Guard) Allow(a Access, confirmed bool) error {
	if !a.Mutating() {
		return nil
	}
	if g.ReadOnly {
		return fmt.Errorf("%w: %s operation refused", ErrReadOnly, a)
	}
	if g.Environment == EnvProduction && !confirmed {
		return fmt.Errorf("%w: %s operation on a production connection", ErrConfirmationRequired, a)
	}
	return nil
}

// RequiresConfirmation reports whether the UI must prompt before this
// operation, so it can render the confirmation ahead of the attempt rather
// than reacting to a refusal.
func (g Guard) RequiresConfirmation(a Access) bool {
	return a.Mutating() && !g.ReadOnly && g.Environment == EnvProduction
}
