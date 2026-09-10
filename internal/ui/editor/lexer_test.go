package editor

import (
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
