// Package sqlfmt lays a statement out without changing what it means
// (FR-5.12).
//
// Everything here works from the lexer's tokens (internal/sqllex), which is
// what makes the one rule keepable: the formatter may move whitespace and
// change the case of a keyword, and may do nothing else. It cannot drop a
// comment, reorder a clause, or touch a string, because it never writes
// anything but the tokens it was given.
//
// And it checks its own work. Before answering, the formatted text is lexed
// again and its tokens compared with the ones that went in; where they
// differ the original is handed back with an error. A formatter that
// silently changed a statement would be worse than one that refused, and
// the check costs one more pass over a statement somebody is looking at.
package sqlfmt

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Tok is one token of a whole text, with the text it stands for.
type Tok struct {
	Kind sqllex.TokenKind
	Text string

	// Line is which line it was on, and NewlinesBefore how many line
	// endings separate it from the token before it. A blank line somebody
	// left between two statements is theirs, and is kept.
	Line           int
	NewlinesBefore int
	SpaceBefore    bool

	// Spaced marks a bracket that stands apart from the name before it: the
	// columns of an INSERT are a list, not arguments to a call.
	Spaced bool

	// From and To are where the token is in the whole text, so that a
	// statement this will not lay out can be handed back exactly as it was
	// written.
	From, To int
}

// Lex reads a whole text into tokens.
//
// The lexer works a line at a time and carries its state across, which is
// what lets a dollar-quoted body or a block comment span lines.
//
// Whitespace does not come back as a token. The lexer has to emit it,
// because a highlighter colours every byte of a line; a formatter is the
// other way round — whitespace is the one thing it is allowed to decide, so
// what is kept of it is only how many line endings stood before each token
// and whether anything at all did.
func Lex(d *sqllex.Dialect, text string) []Tok {
	out, _ := lexOn(d, text)
	return out
}

// lexOn reads a whole text and says what it was in the middle of at the end
// of it: a text ending inside a string or a comment is one to hand back
// rather than rewrite.
func lexOn(d *sqllex.Dialect, text string) ([]Tok, sqllex.State) {
	lx := sqllex.NewLexer(d)
	var out []Tok
	var st sqllex.State
	newlines, space, base := 0, false, 0

	for i, line := range strings.Split(text, "\n") {
		if i > 0 {
			newlines++
		}
		toks, next := lx.LexLine(line, st)
		at := int32(0)
		for _, t := range toks {
			text := line[t.Start:t.End]
			if t.Start > at || blank(text) {
				space = true
			}
			if blank(text) {
				at = t.End
				continue
			}
			out = append(out, Tok{Kind: t.Kind, Text: text,
				Line: i, NewlinesBefore: newlines, SpaceBefore: space,
				From: base + int(t.Start), To: base + int(t.End)})
			at, newlines, space = t.End, 0, false
		}
		if at < int32(len(line)) {
			space = true
		}
		st = next
		base += len(line) + 1
	}
	return out, st
}

// blank reports a run of whitespace, which is what the formatter decides
// and so is not part of what a statement says.
func blank(s string) bool { return strings.TrimSpace(s) == "" }

// Same reports whether two token streams say the same thing.
//
// The same tokens in the same order, with the same text — except a keyword,
// a type or a function name, where case is the one thing the formatter is
// allowed to change. Nothing else is compared, because nothing else is
// allowed to differ.
func Same(a, b []Tok) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind {
			return false
		}
		if a[i].Text == b[i].Text {
			continue
		}
		if !casefree(a[i].Kind) || !strings.EqualFold(a[i].Text, b[i].Text) {
			return false
		}
	}
	return true
}

// casefree reports the kinds whose case says nothing: SQL's own words.
//
// An identifier is not among them. A quoted one is case-sensitive
// everywhere, and an unquoted one is folded by the server in its own
// direction — which is the server's business and not a formatter's.
func casefree(k sqllex.TokenKind) bool {
	switch k {
	case sqllex.TokKeyword, sqllex.TokType, sqllex.TokFunction:
		return true
	}
	return false
}
