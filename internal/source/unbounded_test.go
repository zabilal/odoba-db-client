package source

import (
	"errors"
	"strings"
	"testing"
)

// wordsOf stands in for what a dialect's lexer hands over: lowercased
// keywords and identifiers, with punctuation, comments and strings left out.
func wordsOf(s string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, ",;()")
		for _, part := range strings.Split(w, ".") {
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func TestAStatementThatChangesEveryRow(t *testing.T) {
	for _, c := range []struct {
		stmt   string
		verb   string
		target string
	}{
		{"DELETE FROM orders", "DELETE", "orders"},
		{"delete from orders", "DELETE", "orders"},
		{"UPDATE orders SET paid = true", "UPDATE", "orders"},
		// A qualified name arrives as its parts; the last is the table.
		{"DELETE FROM public.orders", "DELETE", "orders"},
		{"UPDATE public.orders SET paid = true", "UPDATE", "orders"},
		// Modifiers stand between the verb and the table.
		{"UPDATE ONLY orders SET paid = true", "UPDATE", "orders"},
		{"UPDATE LOW_PRIORITY IGNORE orders SET paid = true", "UPDATE", "orders"},
		{"UPDATE OR REPLACE orders SET paid = true", "UPDATE", "orders"},
		{"DELETE FROM ONLY orders", "DELETE", "orders"},
		// A CTE can hide one, and the table is still found — from the verb
		// onward, so the SELECT's own FROM is not mistaken for it.
		{"WITH d AS (DELETE FROM orders RETURNING *) SELECT * FROM d", "DELETE", "orders"},
		{"WITH x AS (SELECT * FROM audit) DELETE FROM orders", "DELETE", "orders"},
		{"WITH x AS (SELECT * FROM audit) UPDATE orders SET paid = true", "UPDATE", "orders"},
	} {
		u := UnboundedIn(wordsOf(c.stmt))
		if u == nil {
			t.Errorf("%q was not noticed", c.stmt)
			continue
		}
		if u.Verb != c.verb || u.Target != c.target {
			t.Errorf("%q reads as %s of %q, want %s of %q", c.stmt, u.Verb, u.Target, c.verb, c.target)
		}
	}
}

func TestWhatDoesNotChangeEveryRow(t *testing.T) {
	for _, stmt := range []string{
		"DELETE FROM orders WHERE id = 1",
		"UPDATE orders SET paid = true WHERE id = 1",
		"update orders set paid = true where id = 1",
		// The trap. A locking read changes nothing, and asking about it
		// would teach people to type through the question.
		"SELECT * FROM orders FOR UPDATE",
		"SELECT * FROM orders FOR NO KEY UPDATE",
		// The same lock inside a CTE, which is where the word UPDATE really
		// can turn up in a statement that changes nothing.
		"WITH locked AS (SELECT * FROM orders FOR UPDATE) SELECT * FROM locked",
		"SELECT * FROM orders",
		"INSERT INTO orders SELECT * FROM staging",
		// TRUNCATE empties a table, and is DDL: refused by read-only mode
		// and confirmed on production like any other structural change.
		"TRUNCATE orders",
		"DROP TABLE orders",
		"",
	} {
		if u := UnboundedIn(wordsOf(stmt)); u != nil {
			t.Errorf("%q was taken for one: %v", stmt, u)
		}
	}
}

func TestWhatAnUnboundedStatementSaysForItself(t *testing.T) {
	named := UnboundedIn(wordsOf("DELETE FROM orders"))
	if got := named.Error(); !strings.Contains(got, "DELETE") || !strings.Contains(got, "orders") ||
		!strings.Contains(got, "no WHERE") {
		t.Errorf("it says %q", got)
	}
	// Where no table can be read out of it, it says so rather than naming
	// one it is not sure of.
	anon := &UnboundedError{Verb: "DELETE"}
	if got := anon.Error(); !strings.Contains(got, "every row it can reach") {
		t.Errorf("it says %q", got)
	}
	// It is a confirmation, so everything that already knows how to ask about
	// one keeps working.
	if !errors.Is(named, ErrConfirmationRequired) {
		t.Error("it is not a confirmation")
	}
}

func TestTheGuardAsksAboutAStatementThatChangesEveryRow(t *testing.T) {
	everything := UnboundedIn(wordsOf("DELETE FROM orders"))

	// On a connection that is not production at all, because the danger is
	// in the statement (FR-4.9).
	g := Guard{Environment: EnvDev}
	err := g.AllowStatement(AccessWrite, everything, false)
	if !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("a development connection ran it unasked: %v", err)
	}
	var got *UnboundedError
	if !errors.As(err, &got) || got.Target != "orders" {
		t.Errorf("the refusal does not say what it is: %v", err)
	}

	// Confirmed, it goes.
	if err := g.AllowStatement(AccessWrite, everything, true); err != nil {
		t.Errorf("confirmed, it was still refused: %v", err)
	}
	// Bounded, it never asked.
	if err := g.AllowStatement(AccessWrite, nil, false); err != nil {
		t.Errorf("an ordinary write was refused: %v", err)
	}

	// Read-only comes first: there is nothing to confirm on a connection
	// that will not write at all.
	ro := Guard{ReadOnly: true, Environment: EnvDev}
	if err := ro.AllowStatement(AccessWrite, everything, true); !errors.Is(err, ErrReadOnly) {
		t.Errorf("read-only was talked past with a confirmation: %v", err)
	}
}

func TestAProductionConnectionStillAsksItsOwnQuestionToo(t *testing.T) {
	g := Guard{Environment: EnvProduction}
	// Unconfirmed, production refuses first — the caller has one question to
	// ask and the connection is the broader of the two.
	err := g.AllowStatement(AccessWrite, UnboundedIn(wordsOf("DELETE FROM orders")), false)
	var got *UnboundedError
	if errors.As(err, &got) {
		t.Errorf("production asked about the statement instead of itself: %v", err)
	}
	if !errors.Is(err, ErrConfirmationRequired) {
		t.Errorf("it said %v", err)
	}
}
