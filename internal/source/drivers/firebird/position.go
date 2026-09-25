package firebird

import (
	"strconv"
	"strings"
)

// Where an error was, so an editor can underline it (FR-5.10).
//
// Firebird reports the place as a line and a column, in the message text
// rather than in a field of its own: "Dynamic SQL Error / SQL error code =
// -204 / Table unknown / NOPE / At line 1, column 15". The contract asks for
// a character offset into the statement, so the statement is counted through.
//
// Reading a position out of prose is fragile, and the failure is chosen
// accordingly: anything that does not parse, or that points past the end of
// the statement, is no position at all rather than a wrong one. An editor
// that underlines nothing is honest; one that underlines the wrong token
// sends somebody looking in the wrong place.

// positionIn is the 1-based character offset the message points at, or 0 for
// a message that points nowhere this statement has.
func positionIn(message, stmt string) int {
	line, col, ok := lineColumn(message)
	if !ok {
		return 0
	}
	return offsetOf(stmt, line, col)
}

// lineColumn reads "At line L, column C" out of a message. The last such
// place wins: a failure inside a procedure body reports the outer statement
// first and the place in the body after it, and the second is the nearer of
// the two to what went wrong.
func lineColumn(message string) (line, col int, ok bool) {
	const marker = "At line "
	rest := message
	for {
		i := strings.Index(rest, marker)
		if i < 0 {
			return line, col, ok
		}
		rest = rest[i+len(marker):]
		l, after, found := strings.Cut(rest, ", column ")
		if !found {
			continue
		}
		ln, err := strconv.Atoi(strings.TrimSpace(l))
		if err != nil || ln < 1 {
			continue
		}
		cn, err := strconv.Atoi(strings.TrimSpace(leadingDigits(after)))
		if err != nil || cn < 1 {
			continue
		}
		line, col, ok = ln, cn, true
	}
}

// leadingDigits is the run of digits at the start of s, which is the column
// number before whatever punctuation or prose follows it.
func leadingDigits(s string) string {
	s = strings.TrimLeft(s, " ")
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return s[:i]
		}
	}
	return s
}

// offsetOf is the 1-based character offset of a line and column, or 0 where
// the statement does not reach it. Characters, not bytes: an editor counts
// what a person sees, so a statement with an accent earlier in it would
// otherwise be underlined a byte or two to the right.
func offsetOf(stmt string, line, col int) int {
	rs := []rune(stmt)
	start := 0 // where the line begins, counting from zero
	for l := 1; l < line; l++ {
		i := indexRune(rs[start:], '\n')
		if i < 0 {
			return 0 // the statement has no such line
		}
		start += i + 1
	}
	// One past the last character is allowed: an error at the end of a
	// statement is reported at the column after it, and a caret goes there.
	if start+col-1 > len(rs) {
		return 0
	}
	return start + col
}

func indexRune(rs []rune, want rune) int {
	for i, r := range rs {
		if r == want {
			return i
		}
	}
	return -1
}
