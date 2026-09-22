package source

import (
	"fmt"
	"strings"
)

// A DELETE or UPDATE with no WHERE changes every row it can reach. FR-4.9
// requires it to be confirmed on every connection, not only a production one:
// what makes it dangerous is the statement, and a development database that
// somebody has spent a week filling is not cheap to refill either.
//
// This lives beside the guard rather than in the window, for the reason
// NFR-S4 gives about read-only mode: a disabled button is a courtesy, not a
// control. Every path that runs a statement passes through here.

// UnboundedError says a statement changes everything it can reach.
//
// It unwraps to ErrConfirmationRequired, so every caller that already knows
// how to ask about a production write keeps working, and one that wants to
// word it differently can look for this.
type UnboundedError struct {
	// Verb is DELETE or UPDATE, in the statement's own words.
	Verb string

	// Target is the table it changes, where the statement says so plainly
	// enough to be sure. It is empty for the shapes this will not guess at,
	// and whatever asks should fall back to the verb.
	Target string
}

func (e *UnboundedError) Error() string {
	if e.Target != "" {
		return fmt.Sprintf("%s with no WHERE: every row in %s", e.Verb, e.Target)
	}
	return fmt.Sprintf("%s with no WHERE: every row it can reach", e.Verb)
}

// Unwrap keeps this a confirmation, because that is what it is.
func (e *UnboundedError) Unwrap() error { return ErrConfirmationRequired }

// UnboundedIn reports a statement that changes every row, given the keyword
// words a dialect's lexer found in it. It answers nil for everything else.
//
// The words are what each driver's classifier already scans: lowercased
// keywords and identifiers, with comments and string literals left out, so a
// DELETE inside a comment or a string is not one.
func UnboundedIn(words []string) *UnboundedError {
	verb, at := boundlessVerb(words)
	if verb == "" {
		return nil
	}
	for _, w := range words {
		if w == "where" {
			// One WHERE is taken as bounding the statement. A statement with
			// two changes and one WHERE would slip through, which is rare
			// enough to prefer over asking about every ordinary UPDATE.
			return nil
		}
	}
	return &UnboundedError{Verb: strings.ToUpper(verb), Target: targetOf(words, verb, at)}
}

// boundlessVerb finds the DELETE or UPDATE a statement is, or that a CTE in
// front of it hides. It is not looking for the word anywhere: SELECT … FOR
// UPDATE takes a lock and changes nothing, and asking about it would teach
// people to type through the question.
func boundlessVerb(words []string) (verb string, at int) {
	if len(words) == 0 {
		return "", 0
	}
	switch words[0] {
	case "delete", "update":
		return words[0], 0
	case "with":
		for i, w := range words {
			if (w == "delete" || w == "update") && !(i > 0 && words[i-1] == "for") {
				return w, i
			}
		}
	}
	return "", 0
}

// stops end the name: what follows them is no longer the table.
var stops = map[string]bool{
	"as": true, "set": true, "where": true, "using": true, "returning": true,
	"order": true, "limit": true, "join": true, "left": true, "inner": true,
	"cross": true, "from": true,
}

// targetOf reads the table out of the statement, starting at the verb rather
// than at the beginning.
//
// Where it starts matters. In
//
//	WITH x AS (SELECT * FROM a) DELETE FROM b
//
// the first FROM belongs to the SELECT, and a search from the front would
// name a — asking somebody to type the name of a table this statement does
// not touch, while emptying one it does.
//
// A qualified name arrives as its parts, because the punctuation between them
// is not a word, so the last part is taken: for public.orders that is orders,
// which is the name somebody would recognise and type.
//
// Taking the last word before a stop also steps over the modifiers that can
// come between the verb and the table — UPDATE ONLY t, UPDATE LOW_PRIORITY
// IGNORE t, UPDATE OR REPLACE t — without a list of them, which would
// otherwise lose a table actually named "only".
func targetOf(words []string, verb string, at int) string {
	i := at
	switch verb {
	case "delete":
		for i < len(words) && words[i] != "from" {
			i++
		}
		i++ // past FROM
	case "update":
		i = at + 1
	}
	last := ""
	for ; i < len(words); i++ {
		if stops[words[i]] {
			break
		}
		last = words[i]
	}
	return last
}

// AllowStatement is Allow for a statement whose danger is in the statement
// rather than in the connection it runs on (FR-4.9).
//
// unbounded is what UnboundedIn answered, and is nil for an ordinary
// statement. Folding it in here rather than leaving each driver to check
// separately is what stops a driver forgetting.
func (g Guard) AllowStatement(a Access, unbounded *UnboundedError, confirmed bool) error {
	if err := g.Allow(a, confirmed); err != nil {
		return err
	}
	if unbounded != nil && !confirmed {
		return unbounded
	}
	return nil
}
