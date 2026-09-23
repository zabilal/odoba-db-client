package sqllex

import (
	"fmt"
	"strings"
	"testing"
)

// lex is a test helper returning (kind, text) pairs for one line.
func lex(t *testing.T, d *Dialect, line string, in State) ([]string, State) {
	t.Helper()
	l := NewLexer(d)
	toks, out := l.LexLine(line, in)
	got := make([]string, 0, len(toks))
	for _, tk := range toks {
		if tk.Kind == TokText && strings.TrimSpace(line[tk.Start:tk.End]) == "" {
			continue // whitespace is noise in these assertions
		}
		got = append(got, kindName(tk.Kind)+":"+line[tk.Start:tk.End])
	}
	return got, out
}

func kindName(k TokenKind) string {
	return [...]string{"text", "kw", "type", "fn", "ident", "qident",
		"str", "num", "comment", "op", "punct", "param", "err"}[k]
}

func TestLexBasicSelect(t *testing.T) {
	got, st := lex(t, PostgreSQL, "SELECT count(*) FROM orders WHERE total > 100;", State{})
	want := []string{"kw:SELECT", "fn:count", "punct:(", "op:*", "punct:)",
		"kw:FROM", "ident:orders", "kw:WHERE", "ident:total", "op:>", "num:100", "punct:;"}
	assertTokens(t, got, want)
	if st.Kind != StateNormal {
		t.Errorf("state = %v, want normal", st.Kind)
	}
}

func TestLexKeywordsAreCaseInsensitive(t *testing.T) {
	for _, s := range []string{"select", "SELECT", "SeLeCt"} {
		got, _ := lex(t, PostgreSQL, s+" 1", State{})
		if len(got) == 0 || !strings.HasPrefix(got[0], "kw:") {
			t.Errorf("%q not recognised as a keyword: %v", s, got)
		}
	}
}

func TestFunctionOnlyWhenCalled(t *testing.T) {
	// A column named "count" must not be coloured as a function; that would
	// make ordinary schemas look wrong.
	got, _ := lex(t, PostgreSQL, "SELECT count FROM t", State{})
	assertContains(t, got, "ident:count")

	got, _ = lex(t, PostgreSQL, "SELECT count(*) FROM t", State{})
	assertContains(t, got, "fn:count")

	// Whitespace before the paren still counts as a call.
	got, _ = lex(t, PostgreSQL, "SELECT count (*) FROM t", State{})
	assertContains(t, got, "fn:count")
}

func TestStringWithEmbeddedCommentMarkers(t *testing.T) {
	// The trap that breaks naive highlighters: comment syntax inside a string.
	line := `UPDATE t SET note = 'contains /* not a comment */ and -- not one either'`
	got, st := lex(t, PostgreSQL, line, State{})
	assertContains(t, got, `str:'contains /* not a comment */ and -- not one either'`)
	if st.Kind != StateNormal {
		t.Errorf("string should have closed on the same line, got %v", st.Kind)
	}
	for _, g := range got {
		if strings.HasPrefix(g, "comment:") {
			t.Errorf("comment token found inside a string literal: %q", g)
		}
	}
}

func TestDoubledQuoteIsEscape(t *testing.T) {
	got, st := lex(t, PostgreSQL, `SELECT 'it''s fine' AS x`, State{})
	assertContains(t, got, `str:'it''s fine'`)
	if st.Kind != StateNormal {
		t.Error("doubled quote must not leave the string open")
	}
}

func TestUnterminatedStringCarriesToNextLine(t *testing.T) {
	_, st := lex(t, PostgreSQL, "SELECT 'unterminated", State{})
	if st.Kind != StateString {
		t.Fatalf("state = %v, want StateString", st.Kind)
	}
	got, st2 := lex(t, PostgreSQL, "still in string' , 1", st)
	assertContains(t, got, `str:still in string'`)
	if st2.Kind != StateNormal {
		t.Error("string should close on the second line")
	}
}

func TestBlockCommentSpansLines(t *testing.T) {
	_, st := lex(t, PostgreSQL, "/* opening", State{})
	if st.Kind != StateBlockComment {
		t.Fatalf("state = %v, want StateBlockComment", st.Kind)
	}
	_, st = lex(t, PostgreSQL, "   middle", st)
	if st.Kind != StateBlockComment {
		t.Error("comment closed early")
	}
	got, st := lex(t, PostgreSQL, "closing */ SELECT 1", st)
	assertContains(t, got, "comment:closing */")
	assertContains(t, got, "kw:SELECT")
	if st.Kind != StateNormal {
		t.Error("comment should have closed")
	}
}

func TestNestedBlockComments(t *testing.T) {
	// PostgreSQL nests. Treating /* */ as non-nesting mis-colours everything
	// after an inner comment closes.
	_, st := lex(t, PostgreSQL, "/* outer /* inner", State{})
	if st.Kind != StateBlockComment || st.Depth != 2 {
		t.Fatalf("depth = %d, want 2", st.Depth)
	}
	_, st = lex(t, PostgreSQL, "*/ still in outer", st)
	if st.Kind != StateBlockComment || st.Depth != 1 {
		t.Fatalf("after inner close: kind=%v depth=%d, want comment/1", st.Kind, st.Depth)
	}
	_, st = lex(t, PostgreSQL, "*/", st)
	if st.Kind != StateNormal {
		t.Error("outer comment should have closed")
	}

	// MySQL does not nest.
	_, st = lex(t, MySQL, "/* outer /* inner", State{})
	if st.Depth != 1 {
		t.Errorf("MySQL depth = %d, want 1 (no nesting)", st.Depth)
	}
}

func TestDollarQuotedBody(t *testing.T) {
	_, st := lex(t, PostgreSQL, "CREATE FUNCTION f() RETURNS int AS $$", State{})
	if st.Kind != StateDollarQuote || st.Tag != "" {
		t.Fatalf("state = %+v, want dollar-quote with empty tag", st)
	}
	// Inside the body, SQL keywords are part of the string, not highlighted.
	_, st = lex(t, PostgreSQL, "  SELECT 1;", st)
	if st.Kind != StateDollarQuote {
		t.Error("body closed early")
	}
	_, st = lex(t, PostgreSQL, "$$ LANGUAGE sql;", st)
	if st.Kind != StateNormal {
		t.Error("dollar quote should have closed")
	}
}

func TestTaggedDollarQuoteIgnoresOtherTags(t *testing.T) {
	_, st := lex(t, PostgreSQL, "AS $body$", State{})
	if st.Tag != "body" {
		t.Fatalf("tag = %q, want body", st.Tag)
	}
	// A different tag inside must not close the body.
	_, st = lex(t, PostgreSQL, "  x := $other$ literal $other$;", st)
	if st.Kind != StateDollarQuote {
		t.Error("a different dollar tag closed the body")
	}
	_, st = lex(t, PostgreSQL, "$body$ LANGUAGE plpgsql;", st)
	if st.Kind != StateNormal {
		t.Error("matching tag should have closed the body")
	}
}

func TestQuotedIdentifiersPerDialect(t *testing.T) {
	got, _ := lex(t, PostgreSQL, `SELECT "my col" FROM t`, State{})
	assertContains(t, got, `qident:"my col"`)

	got, _ = lex(t, MySQL, "SELECT `my col` FROM t", State{})
	assertContains(t, got, "qident:`my col`")

	got, _ = lex(t, SQLServer, "SELECT [my col] FROM t", State{})
	assertContains(t, got, "qident:[my col]")
}

func TestParameters(t *testing.T) {
	got, _ := lex(t, PostgreSQL, "WHERE id = $1 AND name = :name AND x = ?", State{})
	assertContains(t, got, "param:$1")
	assertContains(t, got, "param::name")
	assertContains(t, got, "param:?")
}

func TestNumbers(t *testing.T) {
	got, _ := lex(t, PostgreSQL, "SELECT 1, 2.5, .5, 1e10, 1.2e-3", State{})
	for _, want := range []string{"num:1", "num:2.5", "num:.5", "num:1e10", "num:1.2e-3"} {
		assertContains(t, got, want)
	}
}

func TestMySQLHashCommentAndBackslash(t *testing.T) {
	got, _ := lex(t, MySQL, "SELECT 1 # trailing comment", State{})
	assertContains(t, got, "comment:# trailing comment")

	// PostgreSQL has no # comment; it must not swallow the line.
	got, _ = lex(t, PostgreSQL, "SELECT 1 # 2", State{})
	for _, g := range got {
		if strings.HasPrefix(g, "comment:") {
			t.Errorf("PostgreSQL treated # as a comment: %v", got)
		}
	}

	_, st := lex(t, MySQL, `SELECT 'a\' still in string`, State{})
	if st.Kind != StateString {
		t.Error("MySQL backslash escape did not keep the string open")
	}
}

func TestStateEqualDrivesCascadeStop(t *testing.T) {
	// The incremental highlighter stops cascading when a line's end state is
	// unchanged, so Equal must be exact about depth and tag.
	a := State{Kind: StateBlockComment, Depth: 1}
	if !a.Equal(State{Kind: StateBlockComment, Depth: 1}) {
		t.Error("identical states not equal")
	}
	if a.Equal(State{Kind: StateBlockComment, Depth: 2}) {
		t.Error("differing depth reported equal")
	}
	if (State{Kind: StateDollarQuote, Tag: "a"}).Equal(State{Kind: StateDollarQuote, Tag: "b"}) {
		t.Error("differing dollar tag reported equal")
	}
	// A language with two kinds of string carries which one opened it: a
	// line left inside "…" is not the same state as one inside '…'.
	if (State{Kind: StateString, Quote: '"'}).Equal(State{Kind: StateString, Quote: '\''}) {
		t.Error("two kinds of open string reported equal")
	}
}

func TestTokensCoverTheLineExactly(t *testing.T) {
	// Every byte must belong to exactly one token, in order. A gap silently
	// drops characters from the display; an overlap double-draws them.
	lines := []string{
		"SELECT count(*) FROM orders WHERE total > 100;",
		`UPDATE t SET note = 'has /* and -- inside' WHERE id = $1`,
		"  /* comment */ SELECT \"quoted col\", 1.5e3 FROM t; -- trailing",
		"",
		"   ",
	}
	l := NewLexer(PostgreSQL)
	for _, line := range lines {
		toks, _ := l.LexLine(line, State{})
		var pos int32
		for _, tk := range toks {
			if tk.Start != pos {
				t.Errorf("line %q: gap or overlap at %d (token starts %d)", line, pos, tk.Start)
			}
			if tk.End <= tk.Start {
				t.Errorf("line %q: empty token at %d", line, tk.Start)
			}
			pos = tk.End
		}
		if pos != int32(len(line)) {
			t.Errorf("line %q: tokens cover %d of %d bytes", line, pos, len(line))
		}
	}
}

func TestUTF8IsNotSplit(t *testing.T) {
	line := "SELECT 'héllo wörld' AS 日本語"
	l := NewLexer(PostgreSQL)
	toks, _ := l.LexLine(line, State{})
	for _, tk := range toks {
		s := line[tk.Start:tk.End]
		if strings.ContainsRune(s, '�') {
			t.Errorf("token %q contains a replacement rune; a multi-byte rune was split", s)
		}
	}
}

func TestDialectForFallsBackRatherThanFailing(t *testing.T) {
	// An unknown dialect should give approximate highlighting, never none.
	if DialectFor("nonesuch") == nil {
		t.Fatal("DialectFor returned nil")
	}
	if DialectFor("MySQL").Name != "mysql" {
		t.Error("DialectFor is not case-insensitive")
	}
}

func assertTokens(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d tokens %v,\nwant %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func assertContains(t *testing.T, got []string, want string) {
	t.Helper()
	for _, g := range got {
		if g == want {
			return
		}
	}
	t.Errorf("missing %q in %v", want, got)
}

func TestMongoshIsLexedAsItsOwnLanguage(t *testing.T) {
	lx := NewLexer(Mongosh)
	line := `db.people.find({"name": "Ada's", 'n': 1}) // a comment`
	toks, st := lx.LexLine(line, State{})
	kinds := map[TokenKind]int{}
	for _, tk := range toks {
		kinds[tk.Kind]++
	}
	// A double-quoted run is a string, not a name: mongosh writes every key
	// and every text value that way.
	if kinds[TokString] != 3 {
		t.Errorf("%d strings, want three: %s", kinds[TokString], render(line, toks))
	}
	if kinds[TokQuotedIdent] != 0 {
		t.Errorf("a name was found where there are none: %s", render(line, toks))
	}
	if kinds[TokComment] != 1 {
		t.Errorf("%d comments, want the one after //: %s", kinds[TokComment], render(line, toks))
	}
	if st.Kind != StateNormal {
		t.Errorf("the line ends in %v", st.Kind)
	}
	// A quote of one kind inside the other is text, not an end.
	const mixed = `x = "it's here"`
	toks, _ = lx.LexLine(mixed, State{})
	found := ""
	for _, tk := range toks {
		if tk.Kind == TokString {
			found = mixed[tk.Start:tk.End]
		}
	}
	if found != `"it's here"` {
		t.Errorf("the string is %q, want the whole of it", found)
	}
	// A string left open carries to the next line, with the quote it opened.
	_, st = lx.LexLine(`db.x.find({"a": "open`, State{})
	if st.Kind != StateString || st.Quote != '"' {
		t.Errorf("an unterminated double-quoted string leaves %+v", st)
	}
	toks, st = lx.LexLine(`still text"})`, st)
	if st.Kind != StateNormal || toks[0].Kind != TokString {
		t.Errorf("the next line is %+v %v", st, toks[0].Kind)
	}
	// The words of the shell are its own.
	if !Known("mongosh") {
		t.Error("the lexer does not know mongosh")
	}
	if DialectFor("mongosh") != Mongosh {
		t.Error("mongosh resolves elsewhere")
	}
}

// render shows a line's tokens, for a failure to read.
func render(line string, toks []Token) string {
	var b strings.Builder
	for _, t := range toks {
		fmt.Fprintf(&b, "[%d %q]", t.Kind, line[t.Start:t.End])
	}
	return b.String()
}

func TestRedisIsItsOwnLanguage(t *testing.T) {
	// A console's script is commands, one to a line: the words of a command
	// are its own, a # line is a comment, and "…" is a string rather than a
	// name in quotes.
	if DialectFor("redis") != Redis || DialectFor("valkey") != Redis {
		t.Fatal("a Redis console resolves elsewhere")
	}
	if !Known("redis") || !Known("valkey") {
		t.Error("the lexer does not know what a Redis console speaks")
	}
	lx := NewLexer(Redis)
	line := `SET greeting "hello world" # said once`
	toks, _ := lx.LexLine(line, State{})
	kinds := map[TokenKind]int{}
	for _, tk := range toks {
		kinds[tk.Kind]++
	}
	// A double-quoted argument is a string: a command's arguments are text,
	// and nothing here is a name in quotes.
	if kinds[TokString] != 1 {
		t.Errorf("%d strings, want one: %s", kinds[TokString], render(line, toks))
	}
	if kinds[TokQuotedIdent] != 0 {
		t.Errorf("a name in quotes: %s", render(line, toks))
	}
	// A # line is a comment, as it is in a Redis configuration file.
	if kinds[TokComment] != 1 {
		t.Errorf("%d comments, want one: %s", kinds[TokComment], render(line, toks))
	}
	if kinds[TokKeyword] == 0 {
		t.Errorf("SET is one of the language's own words: %s", render(line, toks))
	}
}

// ClickHouse reads a name written either way, `like this` or "like this",
// so a semicolon inside one is part of the name (T3.31).
func TestClickHouseReadsANameWrittenEitherWay(t *testing.T) {
	got, _ := lex(t, ClickHouse, "SELECT `a;b` FROM t", State{})
	if len(got) < 2 || got[1] != "qident:`a;b`" {
		t.Errorf("a backquoted name gave %v", got)
	}
	got, _ = lex(t, ClickHouse, `SELECT "a;b" FROM t`, State{})
	if len(got) < 2 || got[1] != `qident:"a;b"` {
		t.Errorf("a double-quoted name gave %v", got)
	}
	// A backslash escapes a quote inside a string, as it does in MySQL, so
	// the string does not end there.
	got, st := lex(t, ClickHouse, `SELECT 'it\'s' AS a`, State{})
	if !st.Equal(State{}) {
		t.Errorf("the line ended inside %v: %v", st, got)
	}
	if len(got) < 2 || got[1] != `str:'it\'s'` {
		t.Errorf("an escaped quote gave %v", got)
	}
}

// A dialect that takes one way of writing a name does not take another's.
func TestOnlyClickHouseTakesBothQuotes(t *testing.T) {
	got, _ := lex(t, MySQL, `SELECT "a" FROM t`, State{})
	for _, g := range got {
		if strings.HasPrefix(g, "qident:") {
			t.Errorf("MySQL read a double-quoted name: %v", got)
		}
	}
}
