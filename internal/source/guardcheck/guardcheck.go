// Package guardcheck holds a driver's mutating operations to its guard
// (NFR-S4).
//
// Read-only mode is enforced in the data layer rather than in the window,
// because a disabled button is a courtesy and not a control. That is a claim
// about every write path in every driver, and a claim like that is worth
// checking the same way in each of them rather than trusting that whoever
// added the latest one remembered.
//
// The check has two halves. One runs each operation on a read-only
// connection and on a production one, and insists on the right refusal. The
// other holds the table of operations to the interfaces themselves, so a
// method added to a mutating interface and not listed fails the test — a new
// way to change a database cannot quietly arrive without a guard in front of
// it.
package guardcheck

import (
	"errors"
	"reflect"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Mutating are the interfaces whose methods change a source.
//
// A new one belongs here. What is absent is as deliberate as what is
// present: Browser, Introspector and the rest only read, and a driver that
// refused them on a read-only connection would be useless rather than safe.
var Mutating = []reflect.Type{
	reflect.TypeOf((*source.Writer)(nil)).Elem(),
	reflect.TypeOf((*source.BulkLoader)(nil)).Elem(),
	reflect.TypeOf((*source.IndexManager)(nil)).Elem(),
	reflect.TypeOf((*source.Aggregator)(nil)).Elem(),
	reflect.TypeOf((*source.StreamProducer)(nil)).Elem(),
	reflect.TypeOf((*source.TopicAdmin)(nil)).Elem(),
	reflect.TypeOf((*source.OffsetResetter)(nil)).Elem(),
}

// Planning are the methods on those interfaces that describe a change
// without making one.
//
// Plan renders the SQL a changeset would run and sends nothing; Apply is the
// write. Showing somebody what would happen on a connection they may not
// write to is useful rather than dangerous, and it is how the window knows
// to grey the commit button.
//
// A method belongs here only when it cannot reach the server with anything
// that changes it.
var Planning = map[string]bool{
	"Plan":          true,
	"PlanIndex":     true,
	"PlanDropIndex": true,
}

// Check holds every mutating method a source implements to its guard.
//
// ro is the source on a read-only connection and prod on a production one.
// acts runs one named operation, given the source and whether it has been
// confirmed.
func Check[S any](t *testing.T, ro, prod S, acts map[string]func(S, bool) error) {
	t.Helper()
	Complete(t, ro, acts)

	for name, act := range acts {
		// Read-only refuses it, and consent cannot buy past that: read-only
		// is a decision about the connection rather than a question about
		// this act.
		if err := act(ro, false); !errors.Is(err, source.ErrReadOnly) {
			t.Errorf("%s on a read-only connection: %v", name, err)
		}
		if err := act(ro, true); !errors.Is(err, source.ErrReadOnly) {
			t.Errorf("%s confirmed on a read-only connection: %v", name, err)
		}
		// And production asks before it happens.
		if err := act(prod, false); !errors.Is(err, source.ErrConfirmationRequired) {
			t.Errorf("%s on a production connection, unasked: %v", name, err)
		}
	}
}

// Complete reports any mutating method this source has that the table does
// not hold to the guard.
func Complete[S any](t *testing.T, src S, acts map[string]func(S, bool) error) {
	t.Helper()
	typ := reflect.TypeOf(src)
	for _, iface := range Mutating {
		if !typ.Implements(iface) {
			continue
		}
		for i := range iface.NumMethod() {
			name := iface.Method(i).Name
			if Planning[name] || acts[name] != nil {
				continue
			}
			t.Errorf("%s implements %s.%s, which changes a source, and nothing here holds it to the guard",
				typ, iface.Name(), name)
		}
	}
}
