package oracle

import (
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing Oracle's own SQL (NFR-S6).

var (
	tsql dialect
	tbl  = model.NewRef(model.KindTable, "SHOP", "PEOPLE")
)

// A name is always quoted: an unquoted one folds to upper case, and a name
// this application was given is the name it was given.
func TestANameIsAlwaysQuoted(t *testing.T) {
	for name, want := range map[string]string{
		"PEOPLE":       `"PEOPLE"`,
		"people":       `"people"`,
		"a b":          `"a b"`,
		"":             `""`,
		"drop table t": `"drop table t"`,
		`a"b`:          `"a""b"`,
	} {
		if got := tsql.QuoteIdentifier(name); got != want {
			t.Errorf("QuoteIdentifier(%q) = %q, want %q", name, got, want)
		}
	}
}

// A reference is written with its schema.
func TestAReferenceIsWrittenSchemaDeep(t *testing.T) {
	cases := []struct {
		ref  model.ObjectRef
		want string
	}{
		{model.NewRef(model.KindTable, "SHOP", "PEOPLE"), `"SHOP"."PEOPLE"`},
		{model.NewRef(model.KindColumn, "SHOP", "PEOPLE", "NAME"), `"SHOP"."PEOPLE"`},
		{model.NewRef(model.KindTable, "PEOPLE"), `"PEOPLE"`},
	}
	for _, c := range cases {
		if got := tsql.QualifyRef(c.ref); got != c.want {
			t.Errorf("QualifyRef(%s) = %q, want %q", c.ref, got, c.want)
		}
	}
}

func TestPlaceholdersAreNumberedFromOne(t *testing.T) {
	if got := tsql.Placeholder(1) + " " + tsql.Placeholder(12); got != ":1 :12" {
		t.Errorf("placeholders are %q", got)
	}
}

func browse(t *testing.T, opt source.BrowseOptions) source.Statement {
	t.Helper()
	st, err := tsql.BuildBrowse(tbl, opt)
	if err != nil {
		t.Fatalf("BuildBrowse: %v", err)
	}
	return st
}

// A browse reads a page, and asks for no more than it was told.
func TestABrowseReadsAPage(t *testing.T) {
	st := browse(t, source.BrowseOptions{Limit: 25, Offset: 50})
	want := `SELECT * FROM "SHOP"."PEOPLE" OFFSET :1 ROWS FETCH NEXT :2 ROWS ONLY`
	if st.SQL != want {
		t.Errorf("it reads\n%s\nwant\n%s", st.SQL, want)
	}
	if len(st.Args) != 2 || st.Args[0] != int64(50) || st.Args[1] != int64(25) {
		t.Errorf("bound %#v, want the offset then the limit", st.Args)
	}
	if st := browse(t, source.BrowseOptions{}); st.Args[1] != int64(DefaultPageSize) {
		t.Errorf("with no limit it binds %#v", st.Args)
	}
	if st := browse(t, source.BrowseOptions{Offset: -5}); st.Args[0] != int64(0) {
		t.Errorf("an offset before the beginning is %#v", st.Args[0])
	}
}

// A row's address is not one of its columns, so it is the one name here
// that is written as itself — and given a name, so a grid reads it as a
// column like any other.
func TestARowsAddressIsSelectedAsItself(t *testing.T) {
	st := browse(t, source.BrowseOptions{Columns: []string{"ROWID", "ID"}})
	want := `SELECT ROWID AS "ROWID", "ID" FROM "SHOP"."PEOPLE"`
	if !strings.HasPrefix(st.SQL, want) {
		t.Errorf("it reads\n%s\nwant it to begin\n%s", st.SQL, want)
	}
	st = browse(t, source.BrowseOptions{Sorts: []source.Sort{{Column: "rowid"}}})
	if !strings.Contains(st.SQL, " ORDER BY ROWID NULLS LAST") {
		t.Errorf("it orders by %s", st.SQL)
	}
	st = browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "ROWID", Op: source.OpEqual, Values: []any{"AAA"}}}})
	if !strings.Contains(st.SQL, " WHERE ROWID = :1") {
		t.Errorf("it filters %s", st.SQL)
	}
}

// Oracle says where the NULLs go in the standard's own words.
func TestASortSaysWhereTheNullsGo(t *testing.T) {
	st := browse(t, source.BrowseOptions{Sorts: []source.Sort{{Column: "NAME", Descending: true}}})
	if !strings.Contains(st.SQL, ` ORDER BY "NAME" DESC NULLS LAST OFFSET`) {
		t.Errorf("it sorts\n%s", st.SQL)
	}
	st = browse(t, source.BrowseOptions{Sorts: []source.Sort{{Column: "NAME", NullsFirst: true}, {Column: "ID"}}})
	if !strings.Contains(st.SQL, ` ORDER BY "NAME" NULLS FIRST, "ID" NULLS LAST OFFSET`) {
		t.Errorf("it sorts\n%s", st.SQL)
	}
}

// Each filter means what it means on every other engine.
func TestEachFilterMeansWhatItMeansEverywhere(t *testing.T) {
	cases := []struct {
		f    source.Filter
		want string
	}{
		{source.Filter{Column: "N", Op: source.OpEqual, Values: []any{int64(1)}}, `"N" = :1`},
		{source.Filter{Column: "N", Op: source.OpNotEqual, Values: []any{int64(1)}}, `"N" <> :1`},
		{source.Filter{Column: "N", Op: source.OpLess, Values: []any{int64(1)}}, `"N" < :1`},
		{source.Filter{Column: "N", Op: source.OpLessEqual, Values: []any{int64(1)}}, `"N" <= :1`},
		{source.Filter{Column: "N", Op: source.OpGreater, Values: []any{int64(1)}}, `"N" > :1`},
		{source.Filter{Column: "N", Op: source.OpGreaterEqual, Values: []any{int64(1)}}, `"N" >= :1`},
		{source.Filter{Column: "N", Op: source.OpEqual, Values: []any{nil}}, `"N" IS NULL`},
		{source.Filter{Column: "N", Op: source.OpNotEqual, Values: []any{nil}}, `"N" IS NOT NULL`},
		{source.Filter{Column: "N", Op: source.OpIsNull}, `"N" IS NULL`},
		{source.Filter{Column: "N", Op: source.OpIsNotNull}, `"N" IS NOT NULL`},
		{source.Filter{Column: "N", Op: source.OpBetween, Values: []any{int64(1), int64(9)}}, `"N" BETWEEN :1 AND :2`},
		{source.Filter{Column: "S", Op: source.OpLike, Values: []any{"a%"}}, `TO_CHAR("S") LIKE :1`},
		{source.Filter{Column: "S", Op: source.OpNotLike, Values: []any{"a%"}}, `TO_CHAR("S") NOT LIKE :1`},
		{source.Filter{Column: "S", Op: source.OpContains, Values: []any{"A%"}},
			`LOWER(TO_CHAR("S")) LIKE :1 ESCAPE '!'`},
		{source.Filter{Column: "S", Op: source.OpRegex, Values: []any{"^a"}}, `REGEXP_LIKE(TO_CHAR("S"), :1)`},
		{source.Filter{Column: "N", Op: source.OpIn, Values: []any{int64(1), int64(2)}}, `"N" IN (:1, :2)`},
		{source.Filter{Column: "N", Op: source.OpNotIn, Values: []any{int64(1)}}, `("N" NOT IN (:1) OR "N" IS NULL)`},
		{source.Filter{Column: "N", Op: source.OpEqual, Values: []any{int64(1)}, Negate: true}, `NOT ("N" = :1)`},
	}
	for _, c := range cases {
		st := browse(t, source.BrowseOptions{Filters: []source.Filter{c.f}})
		if !strings.Contains(st.SQL, " WHERE "+c.want+" OFFSET") {
			t.Errorf("filter %s on %q reads\n%s\nwant it to hold %q", c.f.Op, c.f.Column, st.SQL, c.want)
		}
	}
	// A search is lowered on both sides, Oracle's LIKE being about case
	// and a search somebody types not being.
	st := browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "S", Op: source.OpContains, Values: []any{"A%"}}}})
	if st.Args[0] != `%a!%%` {
		t.Errorf("a search binds %#v", st.Args[0])
	}
}

// A time is bound so that a row read from the grid is found again by the
// value it was read as.
func TestATimeIsBoundWithoutItsZone(t *testing.T) {
	when := time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
	st := browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "BORN", Op: source.OpEqual, Values: []any{when}}}})
	if !strings.Contains(st.SQL, ` WHERE "BORN" = CAST(:1 AS TIMESTAMP)`) {
		t.Errorf("it reads\n%s", st.SQL)
	}
	// Everything else is bound as itself.
	st = browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "N", Op: source.OpEqual, Values: []any{int64(1)}}}})
	if strings.Contains(st.SQL, "CAST(") {
		t.Errorf("a number is cast: %s", st.SQL)
	}
}

// An exact number and a document travel as their own text, which is how
// they arrived.
func TestAnExactNumberIsBoundAsItsText(t *testing.T) {
	st := browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "N", Op: source.OpEqual, Values: []any{model.Decimal("1.50")}}}})
	if len(st.Args) == 0 || st.Args[0] != "1.50" {
		t.Errorf("it binds %#v", st.Args)
	}
	st = browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "J", Op: source.OpEqual, Values: []any{model.JSON(`{"a":1}`)}}}})
	if len(st.Args) == 0 || st.Args[0] != `{"a":1}` {
		t.Errorf("it binds %#v", st.Args)
	}
}

// A picklist of values means what the person ticked.
func TestAPicklistOfValuesMeansWhatWasTicked(t *testing.T) {
	cases := []struct {
		op   source.FilterOp
		vals []any
		want string
	}{
		{source.OpIn, []any{int64(1), nil}, `("N" IN (:1) OR "N" IS NULL)`},
		{source.OpIn, []any{nil}, `"N" IS NULL`},
		{source.OpIn, nil, "1 = 0"},
		{source.OpNotIn, []any{int64(1), nil}, `"N" NOT IN (:1)`},
		{source.OpNotIn, []any{int64(1)}, `("N" NOT IN (:1) OR "N" IS NULL)`},
		{source.OpNotIn, []any{nil}, `"N" IS NOT NULL`},
		{source.OpNotIn, nil, "1 = 1"},
	}
	for _, c := range cases {
		st := browse(t, source.BrowseOptions{Filters: []source.Filter{{Column: "N", Op: c.op, Values: c.vals}}})
		if !strings.Contains(st.SQL, " WHERE "+c.want+" OFFSET") {
			t.Errorf("%s of %#v reads\n%s\nwant it to hold %q", c.op, c.vals, st.SQL, c.want)
		}
	}
}

func TestFiltersAreJoinedByAnd(t *testing.T) {
	st := browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "A", Op: source.OpIsNull}, {Column: "B", Op: source.OpIsNotNull}}})
	if !strings.Contains(st.SQL, ` WHERE "A" IS NULL AND "B" IS NOT NULL OFFSET`) {
		t.Errorf("it reads %s", st.SQL)
	}
}

func TestAFilterThatCannotBeMeantIsRefused(t *testing.T) {
	for _, f := range []source.Filter{
		{Column: "N", Op: source.OpEqual},
		{Column: "N", Op: source.OpEqual, Values: []any{1, 2}},
		{Column: "N", Op: source.OpBetween, Values: []any{1}},
		{Column: "N", Op: source.OpIsNull, Values: []any{1}},
		{Column: "N", Op: source.OpLess, Values: []any{nil}},
		{Column: "S", Op: source.OpRegex},
		{Column: "N", Op: source.FilterOp("wat"), Values: []any{1}},
	} {
		if _, err := tsql.BuildBrowse(tbl, source.BrowseOptions{Filters: []source.Filter{f}}); err == nil {
			t.Errorf("filter %s of %#v was accepted", f.Op, f.Values)
		}
	}
}

func TestTheTextOfASearchIsNotAPattern(t *testing.T) {
	for text, want := range map[string]string{
		"50%": "50!%", "a_b": "a!_b", "a!b": "a!!b", "plain": "plain",
	} {
		if got := escapeLike(text); got != want {
			t.Errorf("escapeLike(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestATypedConditionMayOnlyRead(t *testing.T) {
	st := browse(t, source.BrowseOptions{Where: "id > 10"})
	if !strings.Contains(st.SQL, " WHERE (\nid > 10\n) OFFSET") {
		t.Errorf("it reads %s", st.SQL)
	}
	for _, where := range []string{"1 = 1; SELECT 1", "(1 = 1", "1 = 1)", "'open = 1", "1 = 1 /* open"} {
		if _, err := tsql.BuildBrowse(tbl, source.BrowseOptions{Where: where}); err == nil {
			t.Errorf("the condition %q was accepted", where)
		}
	}
}

func TestOptionsOfAnotherKindOfSourceAreRefused(t *testing.T) {
	if _, err := tsql.BuildBrowse(tbl, source.BrowseOptions{Seek: &source.Seek{}}); err == nil {
		t.Error("a seek was accepted")
	}
	if _, err := tsql.BuildBrowse(tbl, source.BrowseOptions{Follow: true}); err == nil {
		t.Error("following was accepted")
	}
	if _, err := tsql.BuildBrowse(model.NewRef(model.KindRoutine, "SHOP", "P"), source.BrowseOptions{}); err == nil {
		t.Error("a routine was browsable")
	}
}

func TestDistinctValuesComeMostFrequentFirst(t *testing.T) {
	st, err := tsql.buildDistinct(tbl, "NAME", source.BrowseOptions{
		Filters: []source.Filter{{Column: "ID", Op: source.OpGreater, Values: []any{int64(3)}}}}, 20)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "NAME", COUNT(*) FROM "SHOP"."PEOPLE" WHERE "ID" > :1 ` +
		`GROUP BY "NAME" ORDER BY COUNT(*) DESC, "NAME" NULLS LAST FETCH NEXT :2 ROWS ONLY`
	if st.SQL != want {
		t.Errorf("it reads\n%s\nwant\n%s", st.SQL, want)
	}
	if _, err := tsql.buildDistinct(tbl, "NAME", source.BrowseOptions{}, 0); err == nil {
		t.Error("a list of every distinct value was accepted (NFR-P11)")
	}
	if _, err := tsql.buildDistinct(model.NewRef(model.KindRoutine, "SHOP", "P"),
		"NAME", source.BrowseOptions{}, 5); err == nil {
		t.Error("a routine's distinct values were accepted")
	}
}
