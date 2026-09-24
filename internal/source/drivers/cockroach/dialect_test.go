package cockroach

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

var d dialect

func table(name string) model.ObjectRef {
	return model.NewRef(model.KindTable, "shop", "public", name)
}

// A name is always quoted, so that its case is the case it was given and a
// reserved word is safe as a column.
func TestAnIdentifierKeepsItsCase(t *testing.T) {
	for in, want := range map[string]string{
		"orders": `"orders"`,
		"Orders": `"Orders"`,
		"select": `"select"`,
		`we"ird`: `"we""ird"`,
		"a\x00b": `"ab"`, // a NUL truncates the statement at the server
		"café":   `"café"`,
	} {
		if got := d.QuoteIdentifier(in); got != want {
			t.Errorf("QuoteIdentifier(%q) = %s, want %s", in, got, want)
		}
	}
}

// A name here is three parts long. That is the whole difference from
// PostgreSQL, which reads a three-part name as a cross-database reference
// and refuses it (ADR-0145).
func TestANameCarriesItsDatabase(t *testing.T) {
	if got := d.QualifyRef(table("orders")); got != `"shop"."public"."orders"` {
		t.Errorf("a table is named %s", got)
	}
	// Shorter paths are named with what they have rather than refused: a
	// reference the explorer never built is still a reference.
	if got := d.QualifyRef(model.NewRef(model.KindTable, "public", "orders")); got != `"public"."orders"` {
		t.Errorf("a two-part reference is named %s", got)
	}
	if got := d.QualifyRef(model.NewRef(model.KindTable, "orders")); got != `"orders"` {
		t.Errorf("a one-part reference is named %s", got)
	}
	if got := d.QualifyRef(model.ObjectRef{Kind: model.KindTable}); got != "" {
		t.Errorf("a reference to nothing is named %s", got)
	}
}

func TestAPlaceholderIsNumbered(t *testing.T) {
	if got := d.Placeholder(3); got != "$3" {
		t.Errorf("the third placeholder is %s", got)
	}
}

// Nothing a person typed is written into a statement: every value is bound
// (NFR-S6).
func TestEveryValueIsBound(t *testing.T) {
	st, err := d.BuildBrowse(table("orders"), source.BrowseOptions{
		Limit: 10, Offset: 20,
		Filters: []source.Filter{
			{Column: "status", Op: source.OpEqual, Values: []any{"'; DROP TABLE orders --"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(st.SQL, "DROP TABLE") {
		t.Fatalf("a value reached the statement: %s", st.SQL)
	}
	if len(st.Args) != 3 {
		t.Fatalf("the statement binds %d values: %s %v", len(st.Args), st.SQL, st.Args)
	}
	if st.Args[0] != "'; DROP TABLE orders --" {
		t.Errorf("the value bound is %#v", st.Args[0])
	}
}

// A browse reads the columns it was asked for, and every column when it
// was asked for none.
func TestABrowseReadsTheColumnsItWasGiven(t *testing.T) {
	st, err := d.BuildBrowse(table("orders"), source.BrowseOptions{Limit: 1,
		Columns: []string{"total", "id"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.SQL, `SELECT "total", "id" FROM `) {
		t.Errorf("the statement reads %s", st.SQL)
	}
	st, err = d.BuildBrowse(table("orders"), source.BrowseOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.SQL, "SELECT * FROM ") {
		t.Errorf("a browse asked for no columns reads %s", st.SQL)
	}
}

// A browse says where the NULLs go, both ways round. The engine's own
// default puts them last ascending and first descending, so flipping a
// sort would move every NULL row from one end of the grid to the other.
func TestASortSaysWhereTheNullsGo(t *testing.T) {
	st, err := d.BuildBrowse(table("orders"), source.BrowseOptions{
		Limit: 5,
		Sorts: []source.Sort{{Column: "total", Descending: true}, {Column: "id", NullsFirst: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := ` ORDER BY "total" DESC NULLS LAST, "id" NULLS FIRST`
	if !strings.Contains(st.SQL, want) {
		t.Errorf("the sort reads %s", st.SQL)
	}
}

// A browse with no limit still has one: an unbounded read is forbidden
// (NFR-P11).
func TestABrowseIsAlwaysBounded(t *testing.T) {
	st, err := d.BuildBrowse(table("orders"), source.BrowseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.SQL, " LIMIT $1") {
		t.Fatalf("the statement reads %s", st.SQL)
	}
	if st.Args[0] != int64(DefaultPageSize) {
		t.Errorf("the default page is %v rows", st.Args[0])
	}
	// No OFFSET at all when there is nothing to skip.
	if strings.Contains(st.SQL, "OFFSET") {
		t.Errorf("a first page asks the server to skip nothing: %s", st.SQL)
	}
}

// Options that cannot be honoured are refused, never ignored (REQ-DRV-3).
func TestWhatCannotBeHonouredIsRefused(t *testing.T) {
	if _, err := d.BuildBrowse(table("orders"), source.BrowseOptions{Follow: true}); err == nil {
		t.Error("a table was followed like a stream")
	}
	if _, err := d.BuildBrowse(table("orders"), source.BrowseOptions{Seek: &source.Seek{}}); err == nil {
		t.Error("a table was sought into like a stream")
	}
	if _, err := d.BuildBrowse(model.NewRef(model.KindSequence, "shop", "public", "s"),
		source.BrowseOptions{}); err == nil {
		t.Error("a sequence was browsed as rows")
	}
}

// Every filter means what it means on every engine.
func TestAFilterMeansWhatItMeans(t *testing.T) {
	cases := []struct {
		name string
		f    source.Filter
		want string
	}{
		{"equal", source.Filter{Column: "a", Op: source.OpEqual, Values: []any{1}}, `"a" = $1`},
		{"not equal", source.Filter{Column: "a", Op: source.OpNotEqual, Values: []any{1}}, `"a" <> $1`},
		{"less", source.Filter{Column: "a", Op: source.OpLess, Values: []any{1}}, `"a" < $1`},
		{"less or equal", source.Filter{Column: "a", Op: source.OpLessEqual, Values: []any{1}}, `"a" <= $1`},
		{"greater", source.Filter{Column: "a", Op: source.OpGreater, Values: []any{1}}, `"a" > $1`},
		{"greater or equal", source.Filter{Column: "a", Op: source.OpGreaterEqual, Values: []any{1}}, `"a" >= $1`},
		// Equality with NULL means IS NULL: "col = NULL" is never true, and
		// NULL is a value somebody can see and pick in the grid.
		{"equal to nothing", source.Filter{Column: "a", Op: source.OpEqual, Values: []any{nil}}, `"a" IS NULL`},
		{"not equal to nothing", source.Filter{Column: "a", Op: source.OpNotEqual, Values: []any{nil}}, `"a" IS NOT NULL`},
		{"is null", source.Filter{Column: "a", Op: source.OpIsNull}, `"a" IS NULL`},
		{"is not null", source.Filter{Column: "a", Op: source.OpIsNotNull}, `"a" IS NOT NULL`},
		{"between", source.Filter{Column: "a", Op: source.OpBetween, Values: []any{1, 2}}, `"a" BETWEEN $1 AND $2`},
		{"like", source.Filter{Column: "a", Op: source.OpLike, Values: []any{"x%"}}, `"a"::text LIKE $1`},
		{"not like", source.Filter{Column: "a", Op: source.OpNotLike, Values: []any{"x%"}}, `"a"::text NOT LIKE $1`},
		// A search is not about case, and is over the column's text so that
		// it works on a column of numbers too.
		{"contains", source.Filter{Column: "a", Op: source.OpContains, Values: []any{"x"}}, `"a"::text ILIKE $1 ESCAPE '\'`},
		{"regex", source.Filter{Column: "a", Op: source.OpRegex, Values: []any{"^x"}}, `"a"::text ~ $1`},
		{"in", source.Filter{Column: "a", Op: source.OpIn, Values: []any{1, 2}}, `"a" IN ($1, $2)`},
		{"not in", source.Filter{Column: "a", Op: source.OpNotIn, Values: []any{1, 2}}, `("a" NOT IN ($1, $2) OR "a" IS NULL)`},
		{"negated", source.Filter{Column: "a", Op: source.OpEqual, Values: []any{1}, Negate: true}, `NOT ("a" = $1)`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, err := d.BuildBrowse(table("t"), source.BrowseOptions{Limit: 1, Filters: []source.Filter{c.f}})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(st.SQL, " WHERE "+c.want) {
				t.Errorf("reads %s\nwant  WHERE %s", st.SQL, c.want)
			}
		})
	}
}

// A picklist can tick NULL, and SQL's IN cannot say so by itself.
func TestNullIsOneOfThePicklistsValues(t *testing.T) {
	for _, c := range []struct {
		name   string
		op     source.FilterOp
		values []any
		want   string
	}{
		{"in with a null", source.OpIn, []any{1, nil}, `("a" IN ($1) OR "a" IS NULL)`},
		{"in only a null", source.OpIn, []any{nil}, `"a" IS NULL`},
		{"in nothing at all", source.OpIn, nil, `false`},
		{"not in with a null", source.OpNotIn, []any{1, nil}, `"a" NOT IN ($1)`},
		{"not in only a null", source.OpNotIn, []any{nil}, `"a" IS NOT NULL`},
		{"not in nothing at all", source.OpNotIn, nil, `true`},
	} {
		t.Run(c.name, func(t *testing.T) {
			st, err := d.BuildBrowse(table("t"), source.BrowseOptions{Limit: 1,
				Filters: []source.Filter{{Column: "a", Op: c.op, Values: c.values}}})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(st.SQL, " WHERE "+c.want) {
				t.Errorf("reads %s\nwant  WHERE %s", st.SQL, c.want)
			}
		})
	}
}

// A per cent somebody typed is a per cent, not a pattern.
func TestASearchIsTextAndNotAPattern(t *testing.T) {
	st, err := d.BuildBrowse(table("t"), source.BrowseOptions{Limit: 1,
		Filters: []source.Filter{{Column: "a", Op: source.OpContains, Values: []any{`50% a_b\c`}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Args[0]; got != `%50\% a\_b\\c%` {
		t.Errorf("the pattern bound is %q", got)
	}
}

// A filter that cannot mean anything is refused rather than guessed at.
func TestAFilterThatMakesNoSenseIsRefused(t *testing.T) {
	for _, f := range []source.Filter{
		{Column: "a", Op: source.OpEqual},                                // no value
		{Column: "a", Op: source.OpBetween, Values: []any{1}},            // one bound
		{Column: "a", Op: source.OpIsNull, Values: []any{1}},             // a value it has no use for
		{Column: "a", Op: source.OpLess, Values: []any{nil}},             // ordered against nothing
		{Column: "a", Op: source.FilterOp("sideways"), Values: []any{1}}, // no such thing
	} {
		if _, err := d.BuildBrowse(table("t"), source.BrowseOptions{Limit: 1,
			Filters: []source.Filter{f}}); err == nil {
			t.Errorf("%+v was accepted", f)
		}
	}
}

// A typed condition may only read (FR-3.6).
func TestATypedConditionMayOnlyRead(t *testing.T) {
	if _, err := d.BuildBrowse(table("t"), source.BrowseOptions{Limit: 1,
		Where: "1 = 1"}); err != nil {
		t.Errorf("a plain condition was refused: %v", err)
	}
	// A condition that ends in a comment is allowed, and cannot comment out
	// what follows it: it is wrapped in brackets of its own lines, so the
	// LIMIT that bounds the read is still there (NFR-P11).
	st, err := d.BuildBrowse(table("t"), source.BrowseOptions{Limit: 1, Where: "a = 1 --"})
	if err != nil {
		t.Fatalf("a condition ending in a comment was refused: %v", err)
	}
	i := strings.Index(st.SQL, "--\n)")
	if i < 0 {
		t.Fatalf("the condition was not closed off: %s", st.SQL)
	}
	if !strings.Contains(st.SQL[i:], "LIMIT") {
		t.Errorf("the bound was commented out: %s", st.SQL)
	}
	for _, where := range []string{
		"1 = 1); DROP TABLE t --",
		"a IN (SELECT nextval('s'))",
		// A comment or a bracket left open takes the rest of the statement
		// with it.
		"a = 1 /* and the rest",
		"(a = 1",
		"a = 1)",
		// One condition, not two statements.
		"a = 1; DELETE FROM t",
	} {
		if _, err := d.BuildBrowse(table("t"), source.BrowseOptions{Limit: 1, Where: where}); err == nil {
			t.Errorf("a condition that could write was accepted: %s", where)
		}
	}
}

// The picklist asks for the values that are there, most frequent first,
// and always with a bound.
func TestTheDistinctValuesAreBounded(t *testing.T) {
	st, err := d.buildDistinct(table("t"), "status", source.BrowseOptions{}, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.SQL, `GROUP BY "status" ORDER BY count(*) DESC, "status" NULLS LAST LIMIT $1`) {
		t.Errorf("reads %s", st.SQL)
	}
	if st.Args[0] != int64(20) {
		t.Errorf("the bound is %v", st.Args[0])
	}
	if _, err := d.buildDistinct(table("t"), "status", source.BrowseOptions{}, 0); err == nil {
		t.Error("a list of distinct values was asked for without a bound")
	}
	if _, err := d.buildDistinct(model.NewRef(model.KindSequence, "a", "b", "c"), "x",
		source.BrowseOptions{}, 5); err == nil {
		t.Error("a sequence was asked for its distinct values")
	}
	// Its filters are the grid's, so the picklist offers the values among
	// the rows somebody has already narrowed to.
	st, err = d.buildDistinct(table("t"), "status", source.BrowseOptions{
		Filters: []source.Filter{{Column: "a", Op: source.OpEqual, Values: []any{1}}}}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.SQL, `WHERE "a" = $1`) {
		t.Errorf("the filters were dropped: %s", st.SQL)
	}
}
