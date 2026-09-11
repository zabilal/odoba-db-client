package postgres

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

var d = dialect{}

func TestQuoteIdentifier(t *testing.T) {
	cases := map[string]string{
		"orders":         `"orders"`,
		"Orders":         `"Orders"`, // case preserved; unquoted would fold
		"select":         `"select"`, // reserved word safe
		`a"b`:            `"a""b"`,   // embedded quote doubled
		"x\x00y":         `"xy"`,     // NUL stripped
		`x"; DROP TABLE`: `"x""; DROP TABLE"`,
	}
	for in, want := range cases {
		if got := d.QuoteIdentifier(in); got != want {
			t.Errorf("QuoteIdentifier(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestQualifyRefDropsDatabase(t *testing.T) {
	// A three-part name is a cross-database reference in PostgreSQL.
	got := d.QualifyRef(model.NewRef(model.KindTable, "ikigai_test", "public", "orders"))
	if got != `"public"."orders"` {
		t.Errorf("got %s", got)
	}
	if got := d.QualifyRef(model.NewRef(model.KindTable, "orders")); got != `"orders"` {
		t.Errorf("single-element path: got %s", got)
	}
}

func browse(t *testing.T, opt source.BrowseOptions) source.Statement {
	t.Helper()
	st, err := d.BuildBrowse(model.NewRef(model.KindTable, "db", "public", "orders"), opt)
	if err != nil {
		t.Fatalf("BuildBrowse: %v", err)
	}
	return st
}

func TestBuildBrowseBasic(t *testing.T) {
	st := browse(t, source.BrowseOptions{Limit: 50})
	if st.SQL != `SELECT * FROM "public"."orders" LIMIT $1` {
		t.Errorf("SQL = %s", st.SQL)
	}
	if !reflect.DeepEqual(st.Args, []any{int64(50)}) {
		t.Errorf("args = %#v", st.Args)
	}
}

func TestBuildBrowseNeverUnbounded(t *testing.T) {
	// NFR-P11: a missing limit must not mean "everything".
	st := browse(t, source.BrowseOptions{})
	if !strings.Contains(st.SQL, "LIMIT $1") || st.Args[0] != int64(DefaultPageSize) {
		t.Errorf("no default limit: %s %v", st.SQL, st.Args)
	}
}

func TestBuildBrowseFullShape(t *testing.T) {
	st := browse(t, source.BrowseOptions{
		Columns: []string{"id", "status"},
		Filters: []source.Filter{
			{Column: "status", Op: source.OpEqual, Values: []any{"paid"}},
			{Column: "total", Op: source.OpBetween, Values: []any{10, 20}},
		},
		Sorts:  []source.Sort{{Column: "placed_at", Descending: true}, {Column: "id"}},
		Limit:  100,
		Offset: 200,
	})
	want := `SELECT "id", "status" FROM "public"."orders" ` +
		`WHERE "status" = $1 AND "total" BETWEEN $2 AND $3 ` +
		`ORDER BY "placed_at" DESC NULLS LAST, "id" NULLS LAST LIMIT $4 OFFSET $5`
	if st.SQL != want {
		t.Errorf("SQL =\n  %s\nwant\n  %s", st.SQL, want)
	}
	if !reflect.DeepEqual(st.Args, []any{"paid", 10, 20, int64(100), int64(200)}) {
		t.Errorf("args = %#v", st.Args)
	}
}

func TestValuesNeverReachStatementText(t *testing.T) {
	// NFR-S6: values are bound, never interpolated, whatever they contain.
	evil := `'; DROP TABLE users; --`
	st := browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "a", Op: source.OpEqual, Values: []any{evil}},
		{Column: "b", Op: source.OpContains, Values: []any{evil}},
		{Column: "c", Op: source.OpIn, Values: []any{evil, "x"}},
		{Column: "d", Op: source.OpLike, Values: []any{evil}},
	}})
	if strings.Contains(st.SQL, "DROP TABLE users") {
		t.Fatalf("a value reached the statement text:\n%s", st.SQL)
	}
}

func TestHostileColumnNameIsQuotedNotExecuted(t *testing.T) {
	st := browse(t, source.BrowseOptions{
		Filters: []source.Filter{{Column: `x"; DROP TABLE t; --`, Op: source.OpIsNull}},
	})
	if !strings.Contains(st.SQL, `"x""; DROP TABLE t; --" IS NULL`) {
		t.Errorf("column not safely quoted: %s", st.SQL)
	}
	if got := d.Classify(st.SQL); got != source.AccessRead {
		t.Errorf("builder output classified as %v; the quoted name leaked as syntax", got)
	}
}

func TestEqualityWithNullMeansIsNull(t *testing.T) {
	// "col = NULL" is never true, so a literal translation returns no rows.
	st := browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "a", Op: source.OpEqual, Values: []any{nil}},
		{Column: "b", Op: source.OpNotEqual, Values: []any{nil}},
	}})
	if !strings.Contains(st.SQL, `"a" IS NULL AND "b" IS NOT NULL`) {
		t.Errorf("SQL = %s", st.SQL)
	}
	if _, err := d.BuildBrowse(model.NewRef(model.KindTable, "t"), source.BrowseOptions{
		Filters: []source.Filter{{Column: "a", Op: source.OpLess, Values: []any{nil}}},
	}); err == nil {
		t.Error("ordering comparison with NULL should be refused")
	}
}

func TestInSemanticsKeepNullsUnlessAsked(t *testing.T) {
	// Unticking "paid" in a status picklist must not also hide NULL-status
	// rows, which is what raw SQL NOT IN does.
	cases := []struct {
		op   source.FilterOp
		vals []any
		want string
	}{
		{source.OpIn, []any{"a", "b"}, `"s" IN ($1, $2)`},
		{source.OpIn, []any{"a", nil}, `("s" IN ($1) OR "s" IS NULL)`},
		{source.OpIn, []any{nil}, `"s" IS NULL`},
		{source.OpIn, []any{}, `FALSE`},
		{source.OpNotIn, []any{"a"}, `("s" NOT IN ($1) OR "s" IS NULL)`},
		{source.OpNotIn, []any{"a", nil}, `"s" NOT IN ($1)`},
		{source.OpNotIn, []any{nil}, `"s" IS NOT NULL`},
		{source.OpNotIn, []any{}, `TRUE`},
	}
	for _, c := range cases {
		st := browse(t, source.BrowseOptions{Filters: []source.Filter{{Column: "s", Op: c.op, Values: c.vals}}})
		if !strings.Contains(st.SQL, "WHERE "+c.want+" LIMIT") {
			t.Errorf("%s %v:\n  got  %s\n  want WHERE %s", c.op, c.vals, st.SQL, c.want)
		}
	}
}

func TestContainsEscapesPatternCharacters(t *testing.T) {
	// Searching for "50%" must not match everything that begins with 50.
	st := browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "n", Op: source.OpContains, Values: []any{`50%_off\`}},
	}})
	if st.Args[0] != `%50\%\_off\\%` {
		t.Errorf("pattern = %q", st.Args[0])
	}
	if !strings.Contains(st.SQL, `ILIKE $1 ESCAPE '\'`) {
		t.Errorf("SQL = %s", st.SQL)
	}
}

func TestNegateWraps(t *testing.T) {
	st := browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "a", Op: source.OpLike, Values: []any{"x%"}, Negate: true},
	}})
	if !strings.Contains(st.SQL, `NOT ("a"::text LIKE $1)`) {
		t.Errorf("SQL = %s", st.SQL)
	}
}

func TestBuildBrowseRefusesWhatItCannotHonour(t *testing.T) {
	// REQ-DRV-3: refuse, never silently ignore.
	ref := model.NewRef(model.KindTable, "db", "public", "t")
	for name, opt := range map[string]source.BrowseOptions{
		"seek":       {Seek: &source.Seek{Mode: source.SeekBeginning}},
		"follow":     {Follow: true},
		"bad arity":  {Filters: []source.Filter{{Column: "a", Op: source.OpBetween, Values: []any{1}}}},
		"unknown op": {Filters: []source.Filter{{Column: "a", Op: "sounds like", Values: []any{1}}}},
	} {
		if _, err := d.BuildBrowse(ref, opt); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := d.BuildBrowse(model.NewRef(model.KindRoutine, "db", "public", "f"), source.BrowseOptions{}); err == nil {
		t.Error("a routine is not browsable")
	}
}

// --- script splitting ---

func texts(stmts []source.ScriptStatement) []string {
	out := make([]string, len(stmts))
	for i, s := range stmts {
		out[i] = s.Text
	}
	return out
}

func TestSplitScript(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   []string
	}{
		{"basic", "SELECT 1; SELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{"no trailing semicolon", "SELECT 1;\nSELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"semicolon in string", "SELECT 'a;b'; SELECT 2", []string{"SELECT 'a;b'", "SELECT 2"}},
		{"semicolon in comment", "SELECT 1 -- x; y\n; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"semicolon in block comment", "SELECT /* ; */ 1; SELECT 2", []string{"SELECT /* ; */ 1", "SELECT 2"}},
		{"empty and comment-only dropped", ";; -- nothing\n; SELECT 1;;", []string{"SELECT 1"}},
		{"dollar-quoted body",
			"CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; SELECT 2; $$ LANGUAGE sql; SELECT 3",
			[]string{"CREATE FUNCTION f() RETURNS int AS $$ SELECT 1; SELECT 2; $$ LANGUAGE sql", "SELECT 3"}},
		{"begin atomic body with case",
			"CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT CASE WHEN true THEN 1 END; SELECT 2; END; SELECT 3",
			[]string{"CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT CASE WHEN true THEN 1 END; SELECT 2; END", "SELECT 3"}},
		{"transaction END is not an atomic close", "BEGIN; SELECT 1; END; SELECT 2", []string{"BEGIN", "SELECT 1", "END", "SELECT 2"}},
	}
	for _, c := range cases {
		got := texts(d.SplitScript(c.script))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n  got  %q\n  want %q", c.name, got, c.want)
		}
	}
}

func TestSplitScriptOffsetsAreCharacters(t *testing.T) {
	// PostgreSQL reports error positions in characters, so offsets must be
	// characters too, or an error after a multi-byte character lands on the
	// wrong token (FR-5.10).
	script := "SELECT 'héllo';\nSELECT 2"
	stmts := d.SplitScript(script)
	if len(stmts) != 2 {
		t.Fatalf("got %d statements", len(stmts))
	}
	if want := len([]rune("SELECT 'héllo';\n")); stmts[1].Offset != want {
		t.Errorf("offset = %d, want %d (characters, not bytes)", stmts[1].Offset, want)
	}
}

// --- classification ---

func TestClassify(t *testing.T) {
	R, W, D, A := source.AccessRead, source.AccessWrite, source.AccessDDL, source.AccessAdmin
	cases := []struct {
		sql  string
		want source.Access
	}{
		// Plainly read.
		{"SELECT 1", R},
		{"select * from orders where total > 100", R},
		{"VALUES (1), (2)", R},
		{"TABLE orders", R},
		{"SHOW search_path", R},
		{"EXPLAIN SELECT * FROM t", R},
		{"EXPLAIN DELETE FROM t", R}, // plans without running
		{"BEGIN", R},
		{"COMMIT", R},
		{"SET search_path = public", R},
		{"", R},
		{"-- just a comment", R},

		// Things that only look like writes.
		{"SELECT 'DELETE FROM users'", R},
		{"SELECT 1 -- DROP TABLE users", R},
		{"SELECT /* DELETE */ 1", R},
		{`SELECT "delete" FROM t`, R},
		{"SELECT insert_date, update_count FROM t", R},

		// Writes.
		{"INSERT INTO t VALUES (1)", W},
		{"update t set a = 1", W},
		{"DELETE FROM t", W},
		{"MERGE INTO t USING s ON true WHEN MATCHED THEN DELETE", W},
		{"COPY t TO STDOUT", W}, // all COPY, by design
		{"CALL p()", W},
		{"DO $$ BEGIN PERFORM 1; END $$", W},
		{"EXECUTE prepared_thing", W},

		// Writes hidden in read-shaped statements.
		{"WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d", W},
		{"SELECT * FROM t FOR UPDATE", W},
		{"SELECT * FROM t FOR NO KEY UPDATE", W},
		{"SELECT * FROM t FOR SHARE", W},
		{"SELECT * FROM t FOR KEY SHARE", W},
		{"SELECT nextval('s')", W},
		{"SELECT * INTO new_t FROM t", D},
		{"EXPLAIN ANALYZE DELETE FROM t", W},
		{"EXPLAIN (ANALYZE, BUFFERS) UPDATE t SET a = 1", W},

		// DDL and admin.
		{"CREATE TABLE x (a int)", D},
		{"DROP TABLE x", D},
		{"TRUNCATE t", D},
		{"VACUUM t", A},
		{"LOCK TABLE t", A},

		// Escapes from read-only mode.
		{"SELECT set_config('default_transaction_read_only', 'off', false)", A},
		{`SELECT "set_config"('default_transaction_read_only', 'off', false)`, A},
		{"SELECT pg_catalog.set_config('transaction_read_only', 'off', true)", A},
		{"SELECT dblink_exec('dbname=x', 'DELETE FROM t')", A},
		{"SET default_transaction_read_only = off", A},
		{"SET SESSION CHARACTERISTICS AS TRANSACTION READ WRITE", A},
		{"SET TRANSACTION READ WRITE", A},
		{"BEGIN READ WRITE", A},
		{"START TRANSACTION ISOLATION LEVEL SERIALIZABLE, READ WRITE", A},
		{"RESET ALL", A},
		{"DISCARD ALL", A},
		{"SET ROLE admin", A},
		{"SELECT pg_terminate_backend(123)", A},

		// Two statements in one string: the worst one wins.
		{"SELECT 1; DROP TABLE users", D},
		{"SELECT 1; SELECT set_config('default_transaction_read_only','off',false)", A},

		// Unrecognised: not confidently read-only.
		{"FROBNICATE t", W},
	}
	for _, c := range cases {
		if got := d.Classify(c.sql); got != c.want {
			t.Errorf("Classify(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

func TestPlaceholder(t *testing.T) {
	if d.Placeholder(1) != "$1" || d.Placeholder(12) != "$12" {
		t.Error("placeholder format")
	}
}

func TestATypedWhereIsOneReadOnlyCondition(t *testing.T) {
	ref := model.NewRef(model.KindTable, "db", "public", "orders")
	st, err := d.BuildBrowse(ref, source.BrowseOptions{Where: "total > 100 -- the big ones",
		Filters: []source.Filter{{Column: "status", Op: source.OpEqual, Values: []any{"paid"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.SQL, `WHERE "status" = $1 AND (`+"\ntotal > 100 -- the big ones\n)") {
		t.Errorf("the condition is not ANDed in brackets on lines of its own: %s", st.SQL)
	}
	if !strings.Contains(st.SQL, "LIMIT $2") {
		t.Errorf("the comment swallowed the LIMIT: %s", st.SQL)
	}
	for _, w := range []string{"pg_terminate_backend(1234)", "1 = 1; DROP TABLE orders", "id = $1"} {
		if _, err := d.BuildBrowse(ref, source.BrowseOptions{Where: w}); err == nil {
			t.Errorf("WHERE %q should be refused", w)
		}
	}
}
