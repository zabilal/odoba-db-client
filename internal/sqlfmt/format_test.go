package sqlfmt

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Laying a statement out without changing what it means (FR-5.12).

func pg() *sqllex.Dialect { return sqllex.DialectFor("postgresql") }

func formatted(t *testing.T, d *sqllex.Dialect, in string) string {
	t.Helper()
	out, err := Format(d, in, Default())
	if err != nil {
		t.Fatalf("Format(%q): %v", in, err)
	}
	return out
}

// The shape every hand-written statement has: the clauses down the left,
// and what a clause is made of indented under it when it will not fit.
func TestAStatementIsLaidOutByItsClauses(t *testing.T) {
	got := formatted(t, pg(), `select id, name from orders o join people p on p.id = o.person_id `+
		`where o.total > 100 order by o.total desc limit 10;`)
	want := `SELECT id, name
FROM orders o
JOIN people p ON p.id = o.person_id
WHERE o.total > 100
ORDER BY o.total DESC
LIMIT 10;
`
	if got != want {
		t.Errorf("it laid out\n%s\nwant\n%s", got, want)
	}
}

// A clause that fits is written along a line, because three short columns
// read better on one line than on three.
func TestAClauseThatFitsStaysOnItsLine(t *testing.T) {
	got := formatted(t, pg(), `select a, b, c from t;`)
	if got != "SELECT a, b, c\nFROM t;\n" {
		t.Errorf("it laid out %q", got)
	}
}

// And one that does not fit goes down the page, one item to a line.
func TestAClauseThatDoesNotFitGoesDownThePage(t *testing.T) {
	long := `select alpha_column, bravo_column, charlie_column, delta_column, ` +
		`echo_column, foxtrot_column, golf_column from t;`
	got := formatted(t, pg(), long)
	for _, want := range []string{"SELECT\n", "    alpha_column,\n", "    golf_column\n", "FROM t;\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("it laid out\n%s\nwithout %q", got, want)
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if len(l) > Default().Width {
			t.Errorf("a line is %d long: %q", len(l), l)
		}
	}
}

// A condition breaks where a reader looks for the next thing: at its AND
// and its OR.
func TestALongConditionBreaksAtItsConjunctions(t *testing.T) {
	got := formatted(t, pg(), `select 1 from t where alpha = 'one' and bravo = 'two' `+
		`and charlie = 'three' and delta = 'four' and echo = 'five';`)
	if !strings.Contains(got, "WHERE\n    alpha = 'one'\n    AND bravo = 'two'\n") {
		t.Errorf("it laid out\n%s", got)
	}
}

// A subquery is read as a statement of its own, so it is laid out as one
// however short it is.
func TestASubqueryIsLaidOutAsAStatement(t *testing.T) {
	got := formatted(t, pg(), `select 1 from t where id in (select id from u where n > 1);`)
	want := `SELECT 1
FROM t
WHERE
    id IN (
        SELECT id
        FROM u
        WHERE n > 1
    );
`
	if got != want {
		t.Errorf("it laid out\n%s\nwant\n%s", got, want)
	}
}

// A CASE opens out one arm to a line, which is the only way a long one can
// be read.
func TestACaseOpensOutOneArmToALine(t *testing.T) {
	got := formatted(t, pg(), `select case when total > 100 then 'big' when total > 10 `+
		`then 'middling' else 'small' end as size, count(*) from orders group by 1;`)
	for _, want := range []string{
		"    CASE\n", "        WHEN total > 100 THEN 'big'\n",
		"        ELSE 'small'\n", "    END AS size,\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("it laid out\n%s\nwithout %q", got, want)
		}
	}
}

// A bracket with nothing in it is not opened out: two lines saying nothing
// are worse than one saying something.
func TestAnEmptyBracketIsLeftAlone(t *testing.T) {
	// One condition with nothing to break it at, long enough that something
	// has to give: the only bracket in it is an empty one.
	got := formatted(t, pg(), `select 1 from t where bravo_column_with_a_very_long_name `+
		`> now() - interval '7 days' + another_long_column_name_here;`)
	if strings.Contains(got, "NOW(\n") {
		t.Errorf("it opened an empty bracket out:\n%s", got)
	}
	if !strings.Contains(got, "NOW()") {
		t.Errorf("it laid out\n%s", got)
	}
}

// A table's columns are a list, not arguments to a call.
func TestATablesColumnsStandApartFromItsName(t *testing.T) {
	got := formatted(t, pg(), `insert into writes (name, n) values ('a', 1);`)
	if !strings.Contains(got, "INSERT INTO writes (name, n)") {
		t.Errorf("it laid out %q", got)
	}
	// And a call's bracket touches it.
	if got := formatted(t, pg(), `select count(*) from t;`); !strings.Contains(got, "COUNT(*)") {
		t.Errorf("it laid out %q", got)
	}
}

// SQL's own words are capitals; nobody else's are touched.
func TestOnlySqlsOwnWordsChangeCase(t *testing.T) {
	got := formatted(t, pg(), `select Id, "Mixed Name" from MyTable where Id = 'Value';`)
	if !strings.Contains(got, "SELECT Id, \"Mixed Name\"") {
		t.Errorf("it laid out %q", got)
	}
	if !strings.Contains(got, "FROM MyTable") {
		t.Errorf("it changed a table's name: %q", got)
	}
	if !strings.Contains(got, "'Value'") {
		t.Errorf("it changed a string: %q", got)
	}
	// And it can be asked to leave them as they were.
	out, err := Format(pg(), `select 1;`, Options{Upper: false})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "select 1") {
		t.Errorf("asked to leave the case alone it wrote %q", out)
	}
}

// A comment is kept where it was, and never swallows what follows it.
func TestACommentIsKeptAndNeverSwallowsWhatFollows(t *testing.T) {
	got := formatted(t, pg(), "select a, -- the first\n b from t;")
	if !strings.Contains(got, "-- the first\n") {
		t.Errorf("it laid out\n%s", got)
	}
	if !strings.Contains(got, "\n    b\n") {
		t.Errorf("the comment swallowed what came after it:\n%s", got)
	}
	// A block comment is not a line comment and breaks nothing.
	if got := formatted(t, pg(), `select /* here */ a from t;`); got != "SELECT /* here */ a\nFROM t;\n" {
		t.Errorf("it laid out %q", got)
	}
}

// A script is several statements, each laid out, with a line between them.
func TestAScriptIsLaidOutStatementByStatement(t *testing.T) {
	got := formatted(t, pg(), "select 1; select 2;")
	if got != "SELECT 1;\n\nSELECT 2;\n" {
		t.Errorf("it laid out %q", got)
	}
}

// Laying out what is already laid out changes nothing: a formatter that
// wandered would be one nobody could leave on.
func TestLayingOutTwiceIsLayingOutOnce(t *testing.T) {
	for _, in := range []string{
		`select id, name from orders o join people p on p.id = o.person_id where o.total > 100;`,
		`select case when a then b else c end from t;`,
		`insert into t (a, b) values (1, 2), (3, 4);`,
		"select a, -- note\n b from t;",
		`select 1 from t where id in (select id from u);`,
		`create table t (id int primary key, name text not null, total numeric(12,2));`,
	} {
		once := formatted(t, pg(), in)
		twice := formatted(t, pg(), once)
		if once != twice {
			t.Errorf("%q\n  once:\n%s\n  twice:\n%s", in, once, twice)
		}
	}
}

// Every dialect, in its own words: a quoted identifier is whatever that
// engine quotes with.
func TestEveryDialectIsLaidOutInItsOwnWords(t *testing.T) {
	for _, tc := range []struct{ lang, in, want string }{
		{"mysql", "select `Odd Name` from t;", "SELECT `Odd Name`"},
		{"sqlserver", "select [Odd Name] from t;", "SELECT [Odd Name]"},
		{"postgresql", `select "Odd Name" from t;`, `SELECT "Odd Name"`},
		{"sqlite", `select "Odd Name" from t;`, `SELECT "Odd Name"`},
		{"cql", `select id from t where id = 1;`, "SELECT id"},
	} {
		d := sqllex.DialectFor(tc.lang)
		got, err := Format(d, tc.in, Default())
		if err != nil {
			t.Errorf("%s: %v", tc.lang, err)
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s laid out %q, want %q in it", tc.lang, got, tc.want)
		}
	}
}

// A statement that cannot be laid out safely is handed back as it was
// written: something this cannot read is something it must not rewrite.
func TestWhatCannotBeLaidOutIsHandedBack(t *testing.T) {
	in := "select 'unterminated from t;"
	out, err := Format(pg(), in, Default())
	if err == nil {
		t.Fatal("a statement that ends inside a string was laid out")
	}
	if strings.TrimSpace(out) != strings.TrimSpace(in) {
		t.Errorf("it answered %q, want the text it was given", out)
	}
	var changed *ErrChanged
	if !as(err, &changed) {
		t.Fatalf("it said %v", err)
	}
	if !strings.Contains(changed.Error(), "1 statement") {
		t.Errorf("it said %q, want how many were left", changed.Error())
	}

	// And it counts them.
	_, err = Format(pg(), "select 'un\nterminated' from t;\nselect 'an\nother' from u;", Default())
	if !as(err, &changed) || !strings.Contains(changed.Error(), "2 statements") {
		t.Errorf("two left as written said %v", err)
	}
}

// One statement it will not lay out does not stop the rest of a script
// being readable.
func TestTheRestOfAScriptIsStillLaidOut(t *testing.T) {
	in := "select a,b from t;\nselect 'un\nterminated' from u;\nselect c,d from v;"
	out, err := Format(pg(), in, Default())
	if err == nil {
		t.Fatal("a statement with a string across two lines was laid out")
	}
	if !strings.Contains(out, "SELECT a, b\nFROM t;") {
		t.Errorf("the first statement was not laid out:\n%s", out)
	}
	if !strings.Contains(out, "SELECT c, d\nFROM v;") {
		t.Errorf("the last statement was not laid out:\n%s", out)
	}
	if !strings.Contains(out, "select 'un\nterminated' from u;") {
		t.Errorf("the one in the middle was not handed back as written:\n%s", out)
	}
	var changed *ErrChanged
	if !as(err, &changed) || !strings.Contains(changed.Error(), "1 statement") {
		t.Errorf("it said %v", err)
	}
}

// Nothing at all is nothing to lay out.
func TestNothingToLayOut(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n"} {
		out, err := Format(pg(), in, Default())
		if err != nil {
			t.Errorf("%q: %v", in, err)
		}
		if out != in {
			t.Errorf("%q became %q", in, out)
		}
	}
}

// Two token streams say the same thing when they are the same tokens in the
// same order, with only SQL's own words allowed to differ in case. This is
// the check the formatter makes on its own work, so it is checked here
// rather than through the thing that uses it.
func TestWhenTwoStreamsSayTheSameThing(t *testing.T) {
	d := pg()
	same := func(a, b string) bool { return Same(Lex(d, a), Lex(d, b)) }

	if !same("select 1", "SELECT   1") {
		t.Error("the same statement laid out differently is not the same")
	}
	if !same("select count(*)", "SELECT COUNT(*)") {
		t.Error("SQL's own words may change case")
	}
	if same("select a", "select A") {
		t.Error("an identifier's case is not this program's to change")
	}
	if same("select 'a'", "select 'A'") {
		t.Error("a string's case is not this program's to change")
	}
	if same("select 1", "select 1, 2") {
		t.Error("a stream with more in it says more")
	}
	if same("select 1, 2", "select 1") {
		t.Error("a stream with less in it says less")
	}
	if same("select a", "select 1") {
		t.Error("an identifier and a number are not the same token")
	}
	if same("select a b", "select a, b") {
		t.Error("a comma is something")
	}
	// The same text is not the same token where it is a different kind of
	// thing, which is what tells a name from what it was quoted as.
	name := []Tok{{Kind: sqllex.TokIdentifier, Text: "x"}}
	text := []Tok{{Kind: sqllex.TokString, Text: "x"}}
	if Same(name, text) {
		t.Error("a name and a string that read alike are not the same token")
	}
	if !Same(name, name) {
		t.Error("a stream does not say the same thing as itself")
	}
}

// The defaults are the ones a statement is laid out with when nobody chose.
func TestTheDefaults(t *testing.T) {
	d := Default()
	if d.Indent != "    " || !d.Upper || d.Width != 80 {
		t.Errorf("the defaults are %+v", d)
	}
	// And what is left unsaid is filled in.
	got := (Options{}).with()
	if got.Indent != "    " || got.Width != 80 {
		t.Errorf("an empty Options became %+v", got)
	}
	// An indent somebody chose is the one used.
	out, err := Format(pg(), `select alpha, bravo, charlie, delta, echo, foxtrot, golf, hotel, india from t;`,
		Options{Indent: "\t", Upper: true, Width: 40})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "\n\talpha,") {
		t.Errorf("it laid out\n%s", out)
	}
	// The width is the whole line's, indent and all: a line that fits only
	// because its indent was not counted does not fit.
	deep, err := Format(pg(), `select 1 from t where id in (select aaaaaaaaaa, bbbbbbbbbb from u);`,
		Options{Indent: "        ", Upper: true, Width: 35})
	if err != nil {
		t.Fatal(err)
	}
	// The inner list is 22 characters and sits under sixteen of indent: it
	// fits only if the indent is not counted, and it is.
	if !strings.Contains(deep, "aaaaaaaaaa,\n") {
		t.Errorf("the indent was not counted against the width:\n%s", deep)
	}
}

// A word that is one of SQL's own is not a clause wherever it appears: LEFT
// begins a clause when a JOIN follows it, and is a function otherwise.
func TestAWordThatIsAClauseOnlyWhereItIsOne(t *testing.T) {
	got := formatted(t, pg(), `select left(name, 3) from t;`)
	if got != "SELECT LEFT(name, 3)\nFROM t;\n" {
		t.Errorf("it laid out %q", got)
	}
	joined := formatted(t, pg(), `select 1 from t left outer join u on u.id = t.id;`)
	if !strings.Contains(joined, "\nLEFT OUTER JOIN u ON u.id = t.id") {
		t.Errorf("it laid out\n%s", joined)
	}
}

// as is errors.As, named so the test reads as a sentence.
func as(err error, target **ErrChanged) bool {
	for err != nil {
		if e, ok := err.(*ErrChanged); ok {
			*target = e
			return true
		}
		break
	}
	return false
}
