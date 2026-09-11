// Package panics turns a panic in driver code into an error (NFR-R1). A
// driver that panics fails the operation it was doing, and the application,
// and every other connection, carry on. The stack goes to the log, never
// into the message a person reads (FR-15.7).
//
// A panic can be caught only on the goroutine it happens on, so every
// goroutine that runs driver code defers one of these, and so does every
// call into a driver made from outside one.
package panics

import (
	"fmt"
	"log/slog"
	"runtime/debug"
)

// Error is a recovered panic.
type Error struct {
	What  string // what was being done, as a phrase: "counting rows"
	Value any    // what the driver panicked with
}

func (e *Error) Error() string {
	return fmt.Sprintf("the driver failed while %s, and stopped: %v. The details are in the log", e.What, e.Value)
}

// Recover, deferred by a function that returns an error, turns a panic into
// an *Error in *err and logs it with its stack:
//
//	defer panics.Recover(&err, "counting rows")
func Recover(err *error, what string) {
	if v := recover(); v != nil {
		*err = caught(what, v)
	}
}

// Catch, deferred by a goroutine, which has nobody to return an error to,
// hands a panic to report as an *Error and logs it with its stack.
func Catch(what string, report func(error)) {
	if v := recover(); v != nil {
		report(caught(what, v))
	}
}

func caught(what string, v any) *Error {
	slog.Error("a driver panicked", "while", what, "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
	return &Error{What: what, Value: v}
}
