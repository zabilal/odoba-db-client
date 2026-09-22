package postgres

import (
	"context"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Looking for a name in the text of a routine body is a search, and a search
// takes the name as a word rather than as a pattern: a table called "a.b"
// must not match "axb".
func TestANameIsLookedForAsItselfAndNotAsAPattern(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"orders", "orders"},
		{"a.b", `a\.b`},
		{"v1+v2", `v1\+v2`},
		{"x(y)", `x\(y\)`},
		{"a|b", `a\|b`},
	} {
		if got := regexpQuote(c.in); got != c.want {
			t.Errorf("%q became %q, want %q", c.in, got, c.want)
		}
	}
}

// A routine is called by its name alone from inside another body, so that is
// what is looked for — not the signature the tree lists it under.
func TestARoutineIsLookedForWithoutItsArguments(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"total(a integer)", "total"},
		{"total()", "total"},
		{"total", "total"},
	} {
		if got := bareName(c.in); got != c.want {
			t.Errorf("%q became %q, want %q", c.in, got, c.want)
		}
	}
}

// A ref that does not say where it is cannot be asked about.
func TestAnIncompleteRefHasNoDependents(t *testing.T) {
	var s pgSource
	if _, err := s.Dependents(context.Background(), model.NewRef(model.KindTable, "public")); err == nil {
		t.Error("an incomplete ref was accepted")
	}
}

// A kind nothing in the database can name is answered with nothing rather
// than with a query that would find whatever shared its name.
func TestAKindNothingCanNameHasNoDependents(t *testing.T) {
	var s pgSource
	deps, err := s.Dependents(context.Background(),
		model.NewRef(model.KindColumn, "db", "public", "people", "id"))
	if err != nil || deps != nil {
		t.Errorf("it said %+v, %v", deps, err)
	}
}
