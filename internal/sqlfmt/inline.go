package sqlfmt

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Writing tokens along a line.
//
// The spacing is the spacing everybody writes by hand: a qualified name has
// no spaces in it, a call's bracket touches its name, nothing is pushed away
// from a comma or a semicolon. Everything else gets one space, which is the
// only amount of space that means anything.

// inline writes tokens along one line, in the case asked for.
func inline(toks []Tok, opt Options) string {
	var b strings.Builder
	for i, t := range toks {
		if i > 0 && spaceBetween(toks[i-1], t) {
			b.WriteByte(' ')
		}
		b.WriteString(cased(t, opt))
	}
	return b.String()
}

// cased is a token as it is written out: SQL's own words in the case that
// was asked for, and everything else exactly as it was.
func cased(t Tok, opt Options) string {
	if casefree(t.Kind) && opt.Upper {
		return strings.ToUpper(t.Text)
	}
	return t.Text
}

// spaceBetween reports whether two tokens are written with a space between
// them.
func spaceBetween(a, b Tok) bool {
	switch b.Text {
	case ",", ";", ")", ".", "::":
		return false
	case "(":
		// A bracket touches the name it calls, and stands apart from a word
		// that is not a name: IN (…), VALUES (…). A list of a table's
		// columns is not a call, whatever it looks like.
		return b.Spaced || !callable(a)
	}
	switch a.Text {
	case "(", ".", "::":
		return false
	}
	if a.Kind == sqllex.TokParameter && b.Text == "(" {
		return false
	}
	return true
}

// callable reports a token a bracket would be calling or qualifying.
func callable(t Tok) bool {
	switch t.Kind {
	case sqllex.TokFunction, sqllex.TokIdentifier, sqllex.TokQuotedIdent, sqllex.TokType:
		return true
	case sqllex.TokKeyword:
		return callKeywords[strings.ToLower(t.Text)]
	}
	return t.Text == ")" || t.Text == "]"
}

// callKeywords are SQL's own words that are the names of functions: LEFT(x,
// 3) is a call, and IN (1, 2) is a list.
//
// A word missed here is written with a space before its bracket, which is
// untidy and nothing more; a word wrongly here is written without one,
// which is the same. There is no way to be wrong about what a statement
// means, so the list is the short one of what is actually written.
var callKeywords = words(`left right char position substring trim extract cast
	overlay treat grouping row nullif coalesce greatest least`)

// hasLineComment reports a comment that swallows the rest of its line, which
// is what stops a group being written along one.
func hasLineComment(toks []Tok) bool {
	for _, t := range toks {
		if t.Kind == sqllex.TokComment && !strings.HasPrefix(t.Text, "/*") {
			return true
		}
	}
	return false
}
