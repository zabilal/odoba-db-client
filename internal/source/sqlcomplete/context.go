package sqlcomplete

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Want is the set of things a position in a statement can take.
//
// It is the whole of the engine's grammar. A statement being typed is not a
// statement yet — half of it is missing — so nothing here parses. It reads
// the token before the cursor, and the clause that token is in, which is what
// decides whether a name would be a table's or a column's.
type Want uint8

const (
	WantKeyword Want = 1 << iota
	WantTable        // tables and views
	WantSchema
	WantDatabase
	WantFunction
	WantColumn
)

// Has reports whether every bit of o is set.
func (w Want) Has(o Want) bool { return w&o == o }

// Context is where completion was asked for: what is being typed, what it is
// qualified by, and what may go there.
type Context struct {
	Want Want

	// Prefix is the word being typed, without its opening quote where it is
	// a quoted identifier, and "" where nothing is being typed.
	Prefix string

	// Quoted reports that the prefix opened with the dialect's identifier
	// quote, so what is inserted must be quoted whether it needs it or not.
	Quoted bool

	// Qualifier is the dotted chain before the prefix, outermost first:
	// {"public"} in `public.ord|`, {"sales", "public"} in `sales.public.|`.
	Qualifier []string

	// Start and End are byte offsets into the text: the word being typed,
	// with its opening quote, which accepting a candidate replaces. They are
	// equal where nothing is being typed.
	Start, End int
}

// token is one lexeme, as byte offsets into the whole text rather than into
// its line.
type token struct {
	kind       sqllex.TokenKind
	start, end int
}

// Analyze reads the text around a cursor, at a byte offset into it.
//
// A statement is re-read from its start on every keystroke. That is affordable
// because the lexer is: 5 000 lines cost under a millisecond (sqllex's probe),
// and completion is asked for over one statement, not a whole script.
func Analyze(d *sqllex.Dialect, text string, cursor int) Context {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(text) {
		cursor = len(text)
	}
	toks := lexAll(d, text)
	c := Context{Start: cursor, End: cursor}

	// The token the cursor is typing in: the one it is strictly inside, or
	// at the end of. A cursor at a token's start is typing before it, not in
	// it, and the token there is the whitespace that precedes it.
	at := -1
	for i, t := range toks {
		if t.start < cursor && cursor <= t.end {
			at = i
			break
		}
	}
	if at >= 0 {
		t := toks[at]
		switch t.kind {
		case sqllex.TokComment, sqllex.TokString, sqllex.TokParameter, sqllex.TokNumber:
			// Inside a comment, a literal or a parameter, nothing is offered.
			// Completing there would write SQL into a string.
			return Context{Start: cursor, End: cursor}
		case sqllex.TokIdentifier, sqllex.TokKeyword, sqllex.TokType, sqllex.TokFunction:
			c.Prefix = text[t.start:cursor]
			c.Start, c.End = t.start, t.end
		case sqllex.TokQuotedIdent:
			if closedQuote(text[t.start:t.end]) && cursor == t.end {
				break // the name is finished; the cursor is past it
			}
			c.Quoted = true
			c.Start, c.End = t.start, t.end
			if inner := t.start + 1; cursor > inner {
				c.Prefix = text[inner:cursor]
			}
		}
	}

	// The chain of names before what is being typed.
	j := prevSignificant(toks, c.Start)
	for j >= 0 && text[toks[j].start:toks[j].end] == "." {
		k := prevSignificant(toks, toks[j].start)
		if k < 0 || !isName(toks[k].kind) {
			break
		}
		c.Qualifier = append([]string{unquote(text[toks[k].start:toks[k].end])}, c.Qualifier...)
		j = prevSignificant(toks, toks[k].start)
	}
	if len(c.Qualifier) > 0 {
		// A name before the dot is a schema, or a table, or an alias for
		// one; which it is, the catalog says, and it may be asked for both.
		c.Want = WantTable | WantFunction | WantColumn
		return c
	}

	prev := ""
	if j >= 0 {
		prev = strings.ToLower(text[toks[j].start:toks[j].end])
	}
	switch {
	case j < 0 || prev == ";":
		// The start of a statement: only a keyword begins one.
		c.Want = WantKeyword
	case tablePosition[prev]:
		c.Want = WantTable | WantSchema
	case prev == "use" || prev == "database":
		c.Want = WantDatabase
	case prev == "(":
		c.Want = WantColumn | WantFunction | WantKeyword
		if w := clauseWant(toks, text, j-1); w.Has(WantTable) {
			c.Want |= WantTable // a subquery's FROM, or a joined table list
		}
	case prev == ",":
		c.Want = clauseWant(toks, text, j)
	case valuePosition[prev] || toks[j].kind == sqllex.TokOperator:
		c.Want = WantColumn | WantFunction | WantKeyword
	default:
		// A name, a literal or a closing bracket is a finished thing; what
		// follows one carries the statement on.
		c.Want = WantKeyword
	}
	return c
}

// tablePosition holds the words a table's name follows.
var tablePosition = words(`from join into table update truncate analyze describe`)

// valuePosition holds the words an expression follows, where a column, a
// function call or a keyword can all go.
var valuePosition = words(`
	select where on having by set and or not when then else case
	distinct in between like ilike similar exists using values returning
	as asc desc limit offset all any some
`)

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// clauseKeyword names the clause a token is in, found by walking back to the
// nearest word that opens one.
var clauseKeyword = map[string]Want{
	"from":      WantTable | WantSchema,
	"join":      WantTable | WantSchema,
	"into":      WantColumn | WantFunction | WantKeyword,
	"update":    WantColumn | WantFunction | WantKeyword,
	"select":    WantColumn | WantFunction | WantKeyword,
	"where":     WantColumn | WantFunction | WantKeyword,
	"on":        WantColumn | WantFunction | WantKeyword,
	"having":    WantColumn | WantFunction | WantKeyword,
	"by":        WantColumn | WantFunction | WantKeyword,
	"set":       WantColumn | WantFunction | WantKeyword,
	"values":    WantColumn | WantFunction | WantKeyword,
	"returning": WantColumn | WantFunction | WantKeyword,
	"using":     WantColumn | WantFunction | WantKeyword,
}

// clauseWant is what the clause containing the token at i takes. A comma
// means another of whatever the clause is a list of, so INSERT INTO t (a, b|
// wants a column where SELECT * FROM a, b| wants a table.
//
// The walk back stops at an unmatched '(': a list inside brackets belongs to
// what opened them, not to the clause outside.
func clauseWant(toks []token, text string, i int) Want {
	depth := 0
	for ; i >= 0; i-- {
		switch text[toks[i].start:toks[i].end] {
		case ")":
			depth++
			continue
		case "(":
			if depth == 0 {
				// INSERT INTO t (a, | — the brackets hold a column list.
				return WantColumn | WantFunction | WantKeyword
			}
			depth--
			continue
		case ";":
			return WantKeyword
		}
		if depth > 0 {
			continue
		}
		if w, ok := clauseKeyword[strings.ToLower(text[toks[i].start:toks[i].end])]; ok {
			return w
		}
	}
	return WantColumn | WantFunction | WantKeyword
}

// prevSignificant is the index of the last token that ends at or before the
// byte offset and is neither whitespace nor a comment, or -1.
func prevSignificant(toks []token, offset int) int {
	for i := len(toks) - 1; i >= 0; i-- {
		if toks[i].end > offset {
			continue
		}
		if toks[i].kind == sqllex.TokText || toks[i].kind == sqllex.TokComment {
			continue
		}
		return i
	}
	return -1
}

func isName(k sqllex.TokenKind) bool {
	switch k {
	case sqllex.TokIdentifier, sqllex.TokQuotedIdent, sqllex.TokKeyword,
		sqllex.TokType, sqllex.TokFunction:
		return true
	}
	return false
}

// closedQuote reports whether a quoted identifier has its closing quote. One
// being typed has not.
func closedQuote(s string) bool {
	if len(s) < 2 {
		return false
	}
	closer := s[0]
	if closer == '[' {
		closer = ']'
	}
	return s[len(s)-1] == closer
}

// unquote strips a quoted identifier's quotes and undoubles the quote
// characters inside it, leaving a plain name as it is.
func unquote(s string) string {
	if len(s) < 2 {
		return s
	}
	open := s[0]
	closer := open
	if open == '[' {
		closer = ']'
	} else if open != '"' && open != '`' {
		return s
	}
	end := len(s)
	if s[end-1] == closer && end >= 2 {
		end--
	}
	inner := s[1:end]
	if open != '[' {
		inner = strings.ReplaceAll(inner, string([]byte{open, open}), string(open))
	}
	return inner
}

// lexAll tokenises the whole text, carrying the lexer's state across lines so
// that a cursor inside a block comment or a dollar-quoted body is seen to be
// there.
func lexAll(d *sqllex.Dialect, text string) []token {
	lx := sqllex.NewLexer(d)
	var out []token
	var st sqllex.State
	for base := 0; base <= len(text); {
		line := text[base:]
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		var ts []sqllex.Token
		ts, st = lx.LexLine(line, st)
		for _, t := range ts {
			out = append(out, token{kind: t.Kind, start: base + int(t.Start), end: base + int(t.End)})
		}
		base += len(line) + 1
	}
	return out
}
