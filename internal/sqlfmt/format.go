package sqlfmt

import (
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Options are how a statement is laid out.
type Options struct {
	// Indent is one level of it. Spaces rather than a tab by default,
	// because SQL is read in places that do not agree about a tab's width:
	// a plan, a log, an error message quoting the statement back.
	Indent string

	// Upper writes SQL's own words in capitals. Identifiers are never
	// touched: an unquoted one is folded by the server in its own
	// direction, and a quoted one means exactly what it says.
	Upper bool

	// Width is the line length a list is broken over. A select list of
	// three short columns reads better on one line than on three.
	Width int
}

// Default is how a statement is laid out when nobody said otherwise.
func Default() Options { return Options{Indent: "    ", Upper: true, Width: 80} }

// with fills in whatever was left unsaid.
func (o Options) with() Options {
	if o.Indent == "" {
		o.Indent = "    "
	}
	if o.Width <= 0 {
		o.Width = 80
	}
	return o
}

// ErrChanged is a formatting that would have said something else.
//
// It cannot happen for a statement the lexer reads the same way twice, and
// it is checked anyway: the whole claim this package makes is that laying a
// statement out does not change it.
type ErrChanged struct{ Why string }

func (e *ErrChanged) Error() string {
	return "sqlfmt: laying this out would have changed it: " + e.Why
}

// Format lays out a script, statement by statement.
//
// A statement that cannot be laid out safely is handed back exactly as it
// was written, and the others are still laid out: one function body with a
// string across three lines should not stop the rest of a script being
// readable. The error says how many were left, so that nobody is left
// wondering why one of them did not move.
func Format(d *sqllex.Dialect, text string, opt Options) (string, error) {
	opt = opt.with()
	in, end := lexOn(d, text)
	if len(in) == 0 {
		return text, nil
	}
	markColumnLists(in)

	var b strings.Builder
	left := 0
	stmts := statements(in)
	for i, stmt := range stmts {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		out, ok := one(d, stmt, opt)
		if !ok || (i == len(stmts)-1 && end.Kind != sqllex.StateNormal) {
			// Ending inside a string or a comment means the last statement
			// is not finished being written, and a half-written statement
			// is not one to rewrite.
			// Exactly the bytes, not a trimmed copy of them: the first and
			// last token are where they are, and whitespace inside one of
			// them is part of what it says.
			b.WriteString(text[stmt[0].From:stmt[len(stmt)-1].To] + "\n")
			left++
			continue
		}
		b.WriteString(out)
	}
	if left > 0 {
		return b.String(), &ErrChanged{Why: plural(left) + " left as written: laying them out would have changed them"}
	}
	return b.String(), nil
}

// one lays out a single statement, and says whether it came back saying the
// same thing.
func one(d *sqllex.Dialect, stmt []Tok, opt Options) (string, bool) {
	for _, t := range stmt {
		if t.Kind == sqllex.TokError {
			return "", false
		}
	}
	out := writeOne(stmt, opt)
	return out, Same(stmt, Lex(d, out))
}

// plural counts statements.
func plural(n int) string {
	if n == 1 {
		return "1 statement"
	}
	return strconv.Itoa(n) + " statements"
}

// w builds the formatted text a token at a time.
type w struct {
	b     strings.Builder
	opt   Options
	depth int
	// atLineStart is true where nothing has been written on this line yet,
	// so that indentation is written once and a break asked for twice is
	// one break.
	atLineStart bool
	// blank is how many blank lines are owed before the next token.
	blank int
}

func (x *w) indent() {
	x.b.WriteString(strings.Repeat(x.opt.Indent, max(x.depth, 0)))
}

// newline ends the line, unless nothing is on it.
func (x *w) newline() {
	if x.atLineStart {
		return
	}
	x.b.WriteByte('\n')
	x.atLineStart = true
}

// space asks for one space before the next token, where anything is on the
// line already.
func (x *w) space() {
	if !x.atLineStart && !strings.HasSuffix(x.b.String(), " ") {
		x.b.WriteByte(' ')
	}
}

// write puts a token down, indenting first where it begins a line.
func (x *w) write(s string) {
	if x.atLineStart {
		for ; x.blank > 0; x.blank-- {
			x.b.WriteByte('\n')
		}
		x.indent()
		x.atLineStart = false
	}
	x.b.WriteString(s)
}

// line is the text written on the line being built, for deciding whether
// something still fits.
func (x *w) lineSoFar() string {
	s := x.b.String()
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
