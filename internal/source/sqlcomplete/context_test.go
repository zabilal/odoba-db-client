package sqlcomplete

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// at analyses text whose cursor is marked with a '|', which is removed first.
func at(t *testing.T, text string) Context {
	t.Helper()
	i := strings.IndexByte(text, '|')
	if i < 0 {
		t.Fatalf("no cursor in %q", text)
	}
	return Analyze(sqllex.PostgreSQL, text[:i]+text[i+1:], i)
}

func TestAnalyzeWantsAKeywordWhereAStatementBegins(t *testing.T) {
	for _, text := range []string{"|", "sel|", "SELECT 1; |", "  |"} {
		if got := at(t, text).Want; got != WantKeyword {
			t.Errorf("%q: want %b, got %b", text, WantKeyword, got)
		}
	}
}

func TestAnalyzeWantsATableAfterFrom(t *testing.T) {
	for _, text := range []string{
		"select * from |",
		"select * from ord|",
		"insert into |",
		"update |",
		"delete from |",
		"select * from a join |",
		"select * from a, |",
	} {
		c := at(t, text)
		if !c.Want.Has(WantTable) || !c.Want.Has(WantSchema) {
			t.Errorf("%q: want tables and schemas, got %b", text, c.Want)
		}
		if c.Want.Has(WantColumn) {
			t.Errorf("%q: a column cannot go there, got %b", text, c.Want)
		}
	}
}

func TestAnalyzeWantsAColumnInAnExpression(t *testing.T) {
	for _, text := range []string{
		"select | from orders",
		"select id, | from orders",
		"select * from orders where |",
		"select * from orders where id = |",
		"select * from a join b on |",
		"select count(|) from orders",
		"select * from orders order by |",
		"update orders set |",
		"insert into orders (id, |",
	} {
		c := at(t, text)
		if !c.Want.Has(WantColumn) || !c.Want.Has(WantFunction) {
			t.Errorf("%q: want columns and functions, got %b", text, c.Want)
		}
	}
}

func TestAnalyzeCommaFollowsItsClause(t *testing.T) {
	// The same comma means a table in one clause and a column in another.
	if c := at(t, "select a, b, | from orders"); c.Want.Has(WantTable) {
		t.Errorf("a select list's comma wants columns, got %b", c.Want)
	}
	if c := at(t, "select * from orders, |"); !c.Want.Has(WantTable) {
		t.Errorf("a from list's comma wants tables, got %b", c.Want)
	}
	// A comma inside a call in the FROM clause is the call's, not the
	// clause's: a value goes there, not a table.
	if c := at(t, "select * from generate_series(1, |)"); c.Want.Has(WantTable) {
		t.Errorf("a call's argument list wants no table, got %b", c.Want)
	}
	// A clause word inside a subquery belongs to the subquery. Here the
	// comma is the FROM list's, and the WHERE two brackets in is not its.
	if c := at(t, "select * from a, (select x from b where y = 1) c, |"); !c.Want.Has(WantTable) {
		t.Errorf("a from list's comma after a subquery wants tables, got %b", c.Want)
	}
	// Brackets belong to what opened them, not to the clause outside.
	if c := at(t, "select * from orders where id in (1, |)"); c.Want.Has(WantTable) {
		t.Errorf("a bracketed list inside WHERE wants no table, got %b", c.Want)
	}
	if c := at(t, "select * from (select a, | from t) x"); c.Want.Has(WantTable) {
		t.Errorf("a subquery's select list wants no table, got %b", c.Want)
	}
}

func TestAnalyzeWantsAKeywordAfterAFinishedThing(t *testing.T) {
	for _, text := range []string{
		"select * from orders |",
		"select id |",
		"select * from orders where id = 1 |",
		"select * from orders where name = 'x' |",
	} {
		if got := at(t, text).Want; got != WantKeyword {
			t.Errorf("%q: want %b, got %b", text, WantKeyword, got)
		}
	}
}

func TestAnalyzeWantsADatabaseAfterUse(t *testing.T) {
	if got := at(t, "use |").Want; got != WantDatabase {
		t.Errorf("want %b, got %b", WantDatabase, got)
	}
}

func TestAnalyzePrefixIsTheWordBeingTyped(t *testing.T) {
	c := at(t, "select * from ord|")
	if c.Prefix != "ord" {
		t.Errorf("prefix %q, want %q", c.Prefix, "ord")
	}
	if c.Start != 14 || c.End != 17 {
		t.Errorf("replaces [%d,%d), want [14,17)", c.Start, c.End)
	}
	// Typed into the middle of a word, the whole word is replaced.
	c = at(t, "select * from ord|ers")
	if c.Prefix != "ord" || c.Start != 14 || c.End != 20 {
		t.Errorf("prefix %q replaces [%d,%d), want \"ord\" [14,20)", c.Prefix, c.Start, c.End)
	}
	// Before a word, nothing is being typed and nothing is replaced.
	c = at(t, "select * from |orders")
	if c.Prefix != "" || c.Start != 14 || c.End != 14 {
		t.Errorf("prefix %q replaces [%d,%d), want \"\" [14,14)", c.Prefix, c.Start, c.End)
	}
}

func TestAnalyzeQuotedPrefix(t *testing.T) {
	c := at(t, `select * from "Ord|`)
	if c.Prefix != "Ord" || !c.Quoted {
		t.Errorf(`prefix %q quoted %v, want "Ord" true`, c.Prefix, c.Quoted)
	}
	if c.Start != 14 {
		t.Errorf("replaces from %d, want 14 (the opening quote)", c.Start)
	}
	// The cursor just after a closed name is past it, not in it.
	c = at(t, `select * from "Orders"|`)
	if c.Prefix != "" || c.Quoted {
		t.Errorf(`prefix %q quoted %v, want "" false`, c.Prefix, c.Quoted)
	}
	// Inside the quotes it is still being typed.
	c = at(t, `select * from "Ord|ers"`)
	if c.Prefix != "Ord" || !c.Quoted || c.End != 22 {
		t.Errorf(`prefix %q quoted %v end %d, want "Ord" true 22`, c.Prefix, c.Quoted, c.End)
	}
}

func TestAnalyzeQualifier(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"select * from public.|", []string{"public"}},
		{"select * from public.ord|", []string{"public"}},
		{"select * from sales.public.|", []string{"sales", "public"}},
		{"select o.| from orders o", []string{"o"}},
		{`select "My Table".| from x`, []string{"My Table"}},
		{`select "a""b".| from x`, []string{`a"b`}},
		{"select * from orders |", nil},
	}
	for _, tc := range cases {
		c := at(t, tc.text)
		if len(c.Qualifier) != len(tc.want) {
			t.Errorf("%q: qualifier %v, want %v", tc.text, c.Qualifier, tc.want)
			continue
		}
		for i := range tc.want {
			if c.Qualifier[i] != tc.want[i] {
				t.Errorf("%q: qualifier %v, want %v", tc.text, c.Qualifier, tc.want)
				break
			}
		}
	}
}

func TestAnalyzeQualifiedWantsTablesAndColumns(t *testing.T) {
	c := at(t, "select * from public.|")
	if !c.Want.Has(WantTable) || !c.Want.Has(WantColumn) {
		t.Errorf("want tables and columns after a name and a dot, got %b", c.Want)
	}
	if c.Want.Has(WantKeyword) {
		t.Errorf("a keyword cannot follow a dot, got %b", c.Want)
	}
}

func TestAnalyzeOffersNothingInsideALiteralOrComment(t *testing.T) {
	for _, text := range []string{
		"select 'ord|",
		"select 'a b|c'",
		"select * from orders -- ord|",
		"select /* ord| */ 1",
		"select * from orders where id = :na|me",
	} {
		if c := at(t, text); c.Want != 0 {
			t.Errorf("%q: want nothing, got %b", text, c.Want)
		}
	}
}

func TestAnalyzeCarriesLexerStateAcrossLines(t *testing.T) {
	// The comment opens on the first line; the cursor on the third is still
	// inside it, which only carrying the lexer's state can tell.
	c := at(t, "select /* one\ntwo\nthr| */ 1")
	if c.Want != 0 {
		t.Errorf("want nothing inside a block comment, got %b", c.Want)
	}
	c = at(t, "select 1;\nselect * from ord|")
	if !c.Want.Has(WantTable) || c.Prefix != "ord" {
		t.Errorf("second line: want %q a table, got %q %b", "ord", c.Prefix, c.Want)
	}
}

func TestAnalyzeClampsTheCursor(t *testing.T) {
	if c := Analyze(sqllex.PostgreSQL, "select", -5); c.Start != 0 {
		t.Errorf("a cursor before the text starts at %d, want 0", c.Start)
	}
	if c := Analyze(sqllex.PostgreSQL, "select", 99); c.Start != 0 || c.End != 6 {
		t.Errorf("a cursor past the end replaces [%d,%d), want [0,6)", c.Start, c.End)
	}
}

func TestUnquote(t *testing.T) {
	cases := map[string]string{
		`"a"`:    "a",
		`"a""b"`: `a"b`,
		"`a`":    "a",
		"[a]":    "a",
		`"open`:  "open",
		"plain":  "plain",
		"a":      "a",
	}
	for in, want := range cases {
		if got := unquote(in); got != want {
			t.Errorf("unquote(%q) = %q, want %q", in, got, want)
		}
	}
}
