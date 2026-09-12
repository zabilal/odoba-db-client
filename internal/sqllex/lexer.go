// Package sqllex tokenises SQL, one line at a time, resuming from saved state.
//
// It began inside the query editor (spike W2, ADR-0003) and moved here at the
// start of Phase 1, because the source layer needs the same tokenisation: a
// driver splitting a script into statements (FR-5.4) must not split on a ';'
// inside a string or a dollar-quoted body, and classifying a statement for
// read-only mode (NFR-S4) must not mistake "DELETE" in a comment for a write.
// Those are exactly the traps this lexer is tested against. ARCH-1 forbids the
// source layer importing UI packages, so the lexer could not stay in the UI.
//
// This package must not import any UI package (ARCH-1).
package sqllex

import "strings"

// A hand-written SQL lexer.
//
// Chroma was the obvious choice and was measured first (probe_test.go). It is
// not viable on the keystroke path: 264 ms to lex a 5 000-line buffer, 185 µs
// and 1 262 allocations for a SINGLE line, at 0.97 MB/s. NFR-P5 allows 16 ms
// from keystroke to glyph for everything — lexing, layout and paint.
//
// Chroma also cannot do the thing this editor most needs. Highlighting a line
// correctly requires knowing whether it begins inside a block comment, a
// string, or a dollar-quoted body, and Chroma's API offers no way to resume
// tokenising from a saved state. Every incremental scheme built on it would
// have to re-lex from the top of the file.
//
// So the lexer is ours. SQL tokenisation is a small, well-understood problem —
// identifiers, keywords, literals, comments, operators — and writing it
// directly buys three things Chroma cannot: resumable per-line state, no
// allocation on the hot path, and dialect control.

// TokenKind classifies a lexeme for colouring.
type TokenKind uint8

const (
	TokText TokenKind = iota
	TokKeyword
	TokType
	TokFunction
	TokIdentifier
	TokQuotedIdent
	TokString
	TokNumber
	TokComment
	TokOperator
	TokPunctuation
	TokParameter // :name, $1, ?
	TokError
)

// Token is a lexeme within one line, as byte offsets into that line. Offsets
// rather than substrings keeps the hot path allocation-free.
type Token struct {
	Kind  TokenKind
	Start int32
	End   int32
}

// StateKind is what a line can be in the middle of when it begins.
type StateKind uint8

const (
	StateNormal StateKind = iota
	StateBlockComment
	StateString
	StateDollarQuote
)

// State is the lexer state at a line boundary. Carrying it per line is what
// makes incremental re-lexing correct: edit a line, re-lex it, and only cascade
// to the lines below if its END state changed.
type State struct {
	Kind StateKind

	// Depth is block-comment nesting. PostgreSQL nests /* /* */ */, so a
	// single boolean would mis-colour the remainder of a file.
	Depth uint8

	// Quote is the character a string opened with, where a dialect has more
	// than one (mongosh's ' and "). Zero means the dialect's own.
	Quote byte

	// Tag is the dollar-quote tag body, e.g. "" for $$ or "fn" for $fn$.
	Tag string
}

// Equal reports whether two states are interchangeable, which is how the
// incremental highlighter decides it can stop cascading.
func (s State) Equal(o State) bool {
	return s.Kind == o.Kind && s.Depth == o.Depth && s.Tag == o.Tag && s.Quote == o.Quote
}

// Dialect parameterises the lexer per engine.
type Dialect struct {
	Name string

	// Keywords, Types and Functions are looked up lowercased.
	Keywords  map[string]bool
	Types     map[string]bool
	Functions map[string]bool

	// QuoteIdent opens a quoted identifier: '"' for standard SQL and
	// PostgreSQL, '`' for MySQL.
	QuoteIdent byte

	// BracketIdent enables [bracketed] identifiers (SQL Server).
	BracketIdent bool

	// DollarQuote enables $tag$ ... $tag$ bodies (PostgreSQL).
	DollarQuote bool

	// HashComment enables # line comments (MySQL).
	HashComment bool

	// NestedBlockComments enables /* /* */ */ (PostgreSQL).
	NestedBlockComments bool

	// BackslashEscapes enables \' inside strings (MySQL). Standard SQL
	// escapes a quote by doubling it, which is handled everywhere.
	BackslashEscapes bool

	// DoubleQuotedStrings makes "..." a string rather than an identifier,
	// which is what a JavaScript-shaped language does (mongosh).
	DoubleQuotedStrings bool

	// SlashComment enables // line comments (mongosh).
	SlashComment bool
}

// Lexer tokenises one line at a time, resuming from a caller-held state.
//
// A Lexer is not safe for concurrent use; give each editor its own. It reuses
// its token buffer between calls, so the slice returned by LexLine is valid
// only until the next call.
type Lexer struct {
	d   *Dialect
	buf []Token
}

// NewLexer builds a lexer for a dialect.
func NewLexer(d *Dialect) *Lexer {
	return &Lexer{d: d, buf: make([]Token, 0, 64)}
}

// Dialect returns the lexer's dialect.
func (l *Lexer) Dialect() *Dialect { return l.d }

// LexLine tokenises one line, given the state it begins in, and returns the
// tokens plus the state the next line begins in.
//
// The returned slice aliases the lexer's internal buffer.
func (l *Lexer) LexLine(line string, in State) ([]Token, State) {
	l.buf = l.buf[:0]
	st := in
	i := int32(0)
	n := int32(len(line))

	// Finish whatever construct the previous line left open before any normal
	// tokenising can begin.
	switch st.Kind {
	case StateBlockComment:
		i, st = l.continueBlockComment(line, 0, 0, st)
	case StateString:
		i, st = l.continueString(line, 0, st)
	case StateDollarQuote:
		i, st = l.continueDollarQuote(line, 0, st)
	}

	for i < n {
		c := line[i]

		switch {
		case c == ' ' || c == '\t' || c == '\r':
			start := i
			for i < n && (line[i] == ' ' || line[i] == '\t' || line[i] == '\r') {
				i++
			}
			l.emit(TokText, start, i)

		case c == '-' && i+1 < n && line[i+1] == '-':
			l.emit(TokComment, i, n)
			i = n

		case l.d.HashComment && c == '#':
			l.emit(TokComment, i, n)
			i = n

		case l.d.SlashComment && c == '/' && i+1 < n && line[i+1] == '/':
			l.emit(TokComment, i, n)
			i = n

		case c == '/' && i+1 < n && line[i+1] == '*':
			// Scan from past the opener: continueBlockComment's nesting check
			// would otherwise count this same "/*" a second time.
			st = State{Kind: StateBlockComment, Depth: 1}
			i, st = l.continueBlockComment(line, i, i+2, st)

		case c == '\'':
			st = State{Kind: StateString}
			i, st = l.continueString(line, i, st)

		case l.d.DoubleQuotedStrings && c == '"':
			// A string, not a name: in mongosh every key and value that is
			// text is written this way.
			st = State{Kind: StateString, Quote: '"'}
			i, st = l.continueString(line, i, st)

		case c == l.d.QuoteIdent:
			i = l.lexQuotedIdent(line, i, l.d.QuoteIdent)

		case l.d.BracketIdent && c == '[':
			i = l.lexBracketIdent(line, i)

		case l.d.DollarQuote && c == '$' && isDollarQuoteStart(line, i):
			tag, after := dollarTag(line, i)
			st = State{Kind: StateDollarQuote, Tag: tag}
			// Emit the opening delimiter as part of the quoted body.
			var next int32
			next, st = l.continueDollarQuote(line, after, st)
			l.mergeBack(TokString, i, next)
			i = next

		case c == ':' && i+1 < n && isIdentStart(line[i+1]):
			start := i
			i++
			for i < n && isIdentPart(line[i]) {
				i++
			}
			l.emit(TokParameter, start, i)

		case c == '$' && i+1 < n && isDigit(line[i+1]):
			start := i
			i++
			for i < n && isDigit(line[i]) {
				i++
			}
			l.emit(TokParameter, start, i)

		case c == '?':
			l.emit(TokParameter, i, i+1)
			i++

		case isDigit(c) || (c == '.' && i+1 < n && isDigit(line[i+1])):
			i = l.lexNumber(line, i)

		case isIdentStart(c):
			i = l.lexWord(line, i)

		case isOperator(c):
			start := i
			for i < n && isOperator(line[i]) {
				i++
			}
			l.emit(TokOperator, start, i)

		case c == '(' || c == ')' || c == ',' || c == ';' || c == '.' || c == '[' || c == ']':
			l.emit(TokPunctuation, i, i+1)
			i++

		default:
			l.emit(TokError, i, i+1)
			i++
		}
	}

	return l.buf, st
}

func (l *Lexer) emit(k TokenKind, start, end int32) {
	if end <= start {
		return
	}
	l.buf = append(l.buf, Token{Kind: k, Start: start, End: end})
}

// mergeBack replaces trailing tokens covering [start,end) with a single token,
// used where a construct is recognised only after its opener was consumed.
func (l *Lexer) mergeBack(k TokenKind, start, end int32) {
	for len(l.buf) > 0 && l.buf[len(l.buf)-1].Start >= start {
		l.buf = l.buf[:len(l.buf)-1]
	}
	l.emit(k, start, end)
}

// continueBlockComment consumes a block comment, returning where it stopped
// and the state the next line inherits.
//
// tokenStart is where the emitted token begins and scan is where scanning
// begins; they differ when the opening "/*" has already been consumed, which
// is what keeps the opener from being counted as a nested comment.
func (l *Lexer) continueBlockComment(line string, tokenStart, scan int32, st State) (int32, State) {
	n := int32(len(line))
	start := tokenStart
	i := scan
	for i < n {
		if i+1 < n && line[i] == '*' && line[i+1] == '/' {
			i += 2
			st.Depth--
			if st.Depth == 0 {
				l.emit(TokComment, start, i)
				return i, State{Kind: StateNormal}
			}
			continue
		}
		if l.d.NestedBlockComments && i+1 < n && line[i] == '/' && line[i+1] == '*' {
			i += 2
			st.Depth++
			continue
		}
		i++
	}
	l.emit(TokComment, start, n)
	return n, st
}

// continueString consumes a quoted string. SQL escapes a quote by doubling
// it; some dialects also allow a backslash escape. A dialect with two kinds
// of string carries which one opened it in the state, so a ' inside a
// "…" does not end it.
func (l *Lexer) continueString(line string, i int32, st State) (int32, State) {
	n := int32(len(line))
	start := i
	quote := byte('\'')
	if st.Quote != 0 {
		quote = st.Quote
	}
	if i < n && line[i] == quote {
		i++ // opening quote
	}
	for i < n {
		c := line[i]
		if (l.d.BackslashEscapes || l.d.DoubleQuotedStrings) && c == '\\' && i+1 < n {
			i += 2
			continue
		}
		if c == quote {
			if i+1 < n && line[i+1] == quote {
				i += 2 // doubled quote is an escaped quote, not a terminator
				continue
			}
			i++
			l.emit(TokString, start, i)
			return i, State{Kind: StateNormal}
		}
		i++
	}
	l.emit(TokString, start, n)
	return n, st
}

// continueDollarQuote consumes a $tag$ ... $tag$ body.
func (l *Lexer) continueDollarQuote(line string, i int32, st State) (int32, State) {
	n := int32(len(line))
	start := i
	closer := "$" + st.Tag + "$"

	if idx := strings.Index(line[i:], closer); idx >= 0 {
		end := i + int32(idx) + int32(len(closer))
		l.emit(TokString, start, end)
		return end, State{Kind: StateNormal}
	}
	l.emit(TokString, start, n)
	return n, st
}

func (l *Lexer) lexQuotedIdent(line string, i int32, q byte) int32 {
	n := int32(len(line))
	start := i
	i++
	for i < n {
		if line[i] == q {
			if i+1 < n && line[i+1] == q {
				i += 2
				continue
			}
			i++
			break
		}
		i++
	}
	l.emit(TokQuotedIdent, start, i)
	return i
}

func (l *Lexer) lexBracketIdent(line string, i int32) int32 {
	n := int32(len(line))
	start := i
	i++
	for i < n && line[i] != ']' {
		i++
	}
	if i < n {
		i++
	}
	l.emit(TokQuotedIdent, start, i)
	return i
}

func (l *Lexer) lexNumber(line string, i int32) int32 {
	n := int32(len(line))
	start := i
	for i < n && (isDigit(line[i]) || line[i] == '.' || line[i] == '_') {
		i++
	}
	// Exponent.
	if i < n && (line[i] == 'e' || line[i] == 'E') {
		j := i + 1
		if j < n && (line[j] == '+' || line[j] == '-') {
			j++
		}
		if j < n && isDigit(line[j]) {
			i = j
			for i < n && isDigit(line[i]) {
				i++
			}
		}
	}
	l.emit(TokNumber, start, i)
	return i
}

// lexWord reads an identifier and classifies it. Classification is a map
// lookup on a lowercased key; the lowering is done without allocating for the
// common case of an already-lowercase word.
func (l *Lexer) lexWord(line string, i int32) int32 {
	n := int32(len(line))
	start := i
	for i < n && isIdentPart(line[i]) {
		i++
	}
	word := line[start:i]

	kind := TokIdentifier
	switch {
	case lookup(l.d.Keywords, word):
		kind = TokKeyword
	case lookup(l.d.Types, word):
		kind = TokType
	case lookup(l.d.Functions, word):
		// Only a function if actually called, so that a column named "count"
		// is not coloured as a function.
		j := i
		for j < n && (line[j] == ' ' || line[j] == '\t') {
			j++
		}
		if j < n && line[j] == '(' {
			kind = TokFunction
		}
	}
	l.emit(kind, start, i)
	return i
}

// lookup tests a word case-insensitively without allocating when the word is
// already lowercase, which the overwhelming majority are.
func lookup(set map[string]bool, word string) bool {
	if set == nil {
		return false
	}
	if set[word] {
		return true
	}
	hasUpper := false
	for i := 0; i < len(word); i++ {
		if word[i] >= 'A' && word[i] <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return false
	}
	return set[strings.ToLower(word)]
}

func isDollarQuoteStart(line string, i int32) bool {
	_, after := dollarTag(line, i)
	return after > i
}

// dollarTag reads a $tag$ opener at i, returning the tag body and the offset
// just past the opener. after == i means this is not a dollar-quote opener.
//
// The tag may not contain '$', even though isIdentPart accepts it inside an
// ordinary identifier. Allowing it makes the bare "$$" form unparseable: the
// scan swallows the closing '$' as part of the tag and then finds no
// terminator.
func dollarTag(line string, i int32) (string, int32) {
	n := int32(len(line))
	j := i + 1
	for j < n && isDollarTagPart(line[j]) {
		j++
	}
	if j < n && line[j] == '$' {
		return line[i+1 : j], j + 1
	}
	return "", i
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentPart(c byte) bool { return isIdentStart(c) || isDigit(c) || c == '$' }

// isDollarTagPart is isIdentPart without '$'. See dollarTag.
func isDollarTagPart(c byte) bool { return isIdentStart(c) || isDigit(c) }

func isOperator(c byte) bool {
	switch c {
	case '+', '-', '*', '/', '%', '<', '>', '=', '!', '|', '&', '^', '~', '@', ':':
		return true
	}
	return false
}
