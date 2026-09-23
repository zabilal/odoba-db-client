package sqlserver

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing SQL Server's own SQL (NFR-S6).

var (
	tsql dialect
	tbl  = model.NewRef(model.KindTable, "shop", "dbo", "people")
)

// A name goes in brackets, whatever is in it.
func TestANameIsBracketed(t *testing.T) {
	d := tsql
	for name, want := range map[string]string{
		"people":       "[people]",
		"a b":          "[a b]",
		"":             "[]",
		"drop table t": "[drop table t]",
		"a]b":          "[a]]b]",
		"]":            "[]]]",
		`a"b`:          `[a"b]`,
		"a'b":          "[a'b]",
	} {
		if got := d.QuoteIdentifier(name); got != want {
			t.Errorf("QuoteIdentifier(%q) = %q, want %q", name, got, want)
		}
	}
}

// A reference is written with its schema, and without its database: the
// statement is already running on a connection to that one.
func TestAReferenceIsWrittenSchemaDeep(t *testing.T) {
	d := tsql
	cases := []struct {
		ref  model.ObjectRef
		want string
	}{
		{model.NewRef(model.KindTable, "shop", "dbo", "people"), "[dbo].[people]"},
		{model.NewRef(model.KindColumn, "shop", "dbo", "people", "name"), "[people].[name]"},
		{model.NewRef(model.KindTable, "dbo", "people"), "[dbo].[people]"},
		{model.NewRef(model.KindTable, "people"), "[people]"},
		{model.ObjectRef{Kind: model.KindTable}, ""},
	}
	for _, c := range cases {
		if got := d.QualifyRef(c.ref); got != c.want {
			t.Errorf("QualifyRef(%s) = %q, want %q", c.ref, got, c.want)
		}
	}
}

// Placeholders are named, and numbered from one.
func TestPlaceholdersAreNamedAndNumberedFromOne(t *testing.T) {
	d := tsql
	if got, want := d.Placeholder(1)+" "+d.Placeholder(2)+" "+d.Placeholder(10), "@p1 @p2 @p10"; got != want {
		t.Errorf("placeholders are %q, want %q", got, want)
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

// A browse reads a page, in an order, and asks for no more than it was told.
func TestABrowseReadsAPage(t *testing.T) {
	st := browse(t, source.BrowseOptions{Limit: 25, Offset: 50})
	want := "SELECT * FROM [dbo].[people] ORDER BY (SELECT NULL) OFFSET @p1 ROWS FETCH NEXT @p2 ROWS ONLY"
	if st.SQL != want {
		t.Errorf("it reads\n%s\nwant\n%s", st.SQL, want)
	}
	if len(st.Args) != 2 || st.Args[0] != int64(50) || st.Args[1] != int64(25) {
		t.Errorf("bound %#v, want the offset then the limit", st.Args)
	}
}

// Paging needs an order, and rows in no particular order are still rows: a
// browse with nothing to sort by says so rather than refusing.
func TestPagingAlwaysHasAnOrder(t *testing.T) {
	if !strings.Contains(browse(t, source.BrowseOptions{}).SQL, "ORDER BY (SELECT NULL) OFFSET") {
		t.Error("a browse with no sort has no order to page from")
	}
}

// Asked for no page, it reads one page's worth rather than the table
// (NFR-P11).
func TestABrowseWithoutALimitStillHasOne(t *testing.T) {
	st := browse(t, source.BrowseOptions{})
	if len(st.Args) != 2 || st.Args[1] != int64(DefaultPageSize) {
		t.Errorf("bound %#v, want a default page size", st.Args)
	}
	if st := browse(t, source.BrowseOptions{Offset: -5}); st.Args[0] != int64(0) {
		t.Errorf("an offset before the beginning is %#v", st.Args[0])
	}
}

// Only the columns asked for, each named as a name.
func TestABrowseReadsTheColumnsAskedFor(t *testing.T) {
	st := browse(t, source.BrowseOptions{Columns: []string{"id", "full name"}})
	if !strings.HasPrefix(st.SQL, "SELECT [id], [full name] FROM [dbo].[people]") {
		t.Errorf("it reads %s", st.SQL)
	}
}

// A sort says where the NULLs go, which SQL Server has no words for.
func TestASortSaysWhereTheNullsGo(t *testing.T) {
	st := browse(t, source.BrowseOptions{Sorts: []source.Sort{{Column: "name", Descending: true}}})
	want := "ORDER BY CASE WHEN [name] IS NULL THEN 1 ELSE 0 END ASC, [name] DESC OFFSET"
	if !strings.Contains(st.SQL, want) {
		t.Errorf("it sorts\n%s\nwant it to hold\n%s", st.SQL, want)
	}
	st = browse(t, source.BrowseOptions{Sorts: []source.Sort{{Column: "name", NullsFirst: true}, {Column: "id"}}})
	want = "ORDER BY CASE WHEN [name] IS NULL THEN 1 ELSE 0 END DESC, [name], " +
		"CASE WHEN [id] IS NULL THEN 1 ELSE 0 END ASC, [id] OFFSET"
	if !strings.Contains(st.SQL, want) {
		t.Errorf("it sorts\n%s\nwant it to hold\n%s", st.SQL, want)
	}
}

// Each filter means what it means on every other engine.
func TestEachFilterMeansWhatItMeansEverywhere(t *testing.T) {
	cases := []struct {
		f    source.Filter
		want string
		args []any
	}{
		{source.Filter{Column: "n", Op: source.OpEqual, Values: []any{int64(1)}}, "[n] = @p1", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpNotEqual, Values: []any{int64(1)}}, "[n] <> @p1", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpLess, Values: []any{int64(1)}}, "[n] < @p1", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpLessEqual, Values: []any{int64(1)}}, "[n] <= @p1", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpGreater, Values: []any{int64(1)}}, "[n] > @p1", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpGreaterEqual, Values: []any{int64(1)}}, "[n] >= @p1", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpEqual, Values: []any{nil}}, "[n] IS NULL", nil},
		{source.Filter{Column: "n", Op: source.OpNotEqual, Values: []any{nil}}, "[n] IS NOT NULL", nil},
		{source.Filter{Column: "n", Op: source.OpIsNull}, "[n] IS NULL", nil},
		{source.Filter{Column: "n", Op: source.OpIsNotNull}, "[n] IS NOT NULL", nil},
		{source.Filter{Column: "n", Op: source.OpBetween, Values: []any{int64(1), int64(9)}},
			"[n] BETWEEN @p1 AND @p2", []any{int64(1), int64(9)}},
		{source.Filter{Column: "s", Op: source.OpLike, Values: []any{"a%"}},
			"CAST([s] AS nvarchar(max)) LIKE @p1", []any{"a%"}},
		{source.Filter{Column: "s", Op: source.OpNotLike, Values: []any{"a%"}},
			"CAST([s] AS nvarchar(max)) NOT LIKE @p1", []any{"a%"}},
		{source.Filter{Column: "s", Op: source.OpContains, Values: []any{"50%"}},
			"CAST([s] AS nvarchar(max)) LIKE @p1 ESCAPE '!'", []any{"%50!%%"}},
		{source.Filter{Column: "n", Op: source.OpIn, Values: []any{int64(1), int64(2)}},
			"[n] IN (@p1, @p2)", []any{int64(1), int64(2)}},
		{source.Filter{Column: "n", Op: source.OpNotIn, Values: []any{int64(1)}},
			"([n] NOT IN (@p1) OR [n] IS NULL)", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpEqual, Values: []any{int64(1)}, Negate: true},
			"NOT ([n] = @p1)", []any{int64(1)}},
	}
	for _, c := range cases {
		st := browse(t, source.BrowseOptions{Filters: []source.Filter{c.f}})
		if !strings.Contains(st.SQL, " WHERE "+c.want+" ORDER BY") {
			t.Errorf("filter %s on %q reads\n%s\nwant it to hold %q", c.f.Op, c.f.Column, st.SQL, c.want)
		}
		for i, a := range c.args {
			if i >= len(st.Args) || st.Args[i] != a {
				t.Errorf("filter %s bound %#v, want %#v", c.f.Op, st.Args, c.args)
				break
			}
		}
	}
}

// A picklist of values means what the person ticked: NULL is a value, and a
// list of none matches none.
func TestAPicklistOfValuesMeansWhatWasTicked(t *testing.T) {
	cases := []struct {
		op   source.FilterOp
		vals []any
		want string
	}{
		{source.OpIn, []any{int64(1), nil}, "([n] IN (@p1) OR [n] IS NULL)"},
		{source.OpIn, []any{nil}, "[n] IS NULL"},
		{source.OpIn, nil, "1 = 0"},
		{source.OpNotIn, []any{int64(1), nil}, "[n] NOT IN (@p1)"},
		{source.OpNotIn, []any{int64(1)}, "([n] NOT IN (@p1) OR [n] IS NULL)"},
		{source.OpNotIn, []any{nil}, "[n] IS NOT NULL"},
		{source.OpNotIn, nil, "1 = 1"},
	}
	for _, c := range cases {
		st := browse(t, source.BrowseOptions{Filters: []source.Filter{{Column: "n", Op: c.op, Values: c.vals}}})
		if !strings.Contains(st.SQL, " WHERE "+c.want+" ORDER BY") {
			t.Errorf("%s of %#v reads\n%s\nwant it to hold %q", c.op, c.vals, st.SQL, c.want)
		}
	}
}

// Filters are joined by AND, in the order they were given.
func TestFiltersAreJoinedByAnd(t *testing.T) {
	st := browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "a", Op: source.OpIsNull}, {Column: "b", Op: source.OpIsNotNull}}})
	if !strings.Contains(st.SQL, " WHERE [a] IS NULL AND [b] IS NOT NULL ORDER BY") {
		t.Errorf("it reads %s", st.SQL)
	}
}

// A filter that cannot mean what it says is refused rather than guessed at.
func TestAFilterThatCannotBeMeantIsRefused(t *testing.T) {
	for _, f := range []source.Filter{
		{Column: "n", Op: source.OpEqual},
		{Column: "n", Op: source.OpEqual, Values: []any{1, 2}},
		{Column: "n", Op: source.OpBetween, Values: []any{1}},
		{Column: "n", Op: source.OpIsNull, Values: []any{1}},
		{Column: "n", Op: source.OpLess, Values: []any{nil}},
		{Column: "s", Op: source.OpRegex, Values: []any{"^a"}},
		{Column: "n", Op: source.FilterOp("wat"), Values: []any{1}},
	} {
		if _, err := tsql.BuildBrowse(tbl, source.BrowseOptions{Filters: []source.Filter{f}}); err == nil {
			t.Errorf("filter %s of %#v was accepted", f.Op, f.Values)
		}
	}
}

// A bracket is a wildcard in T-SQL and in no other engine's LIKE, so
// searching for one finds it rather than a set of characters.
func TestABracketIsEscapedInASearch(t *testing.T) {
	for text, want := range map[string]string{
		"a[b]c": "a![b]c",
		"50%":   "50!%",
		"a_b":   "a!_b",
		"a!b":   "a!!b",
		"plain": "plain",
	} {
		if got := escapeLike(text); got != want {
			t.Errorf("escapeLike(%q) = %q, want %q", text, got, want)
		}
	}
}

// What somebody typed into the filter bar may read and nothing else
// (FR-3.6).
func TestATypedConditionMayOnlyRead(t *testing.T) {
	st := browse(t, source.BrowseOptions{Where: "id > 10"})
	if !strings.Contains(st.SQL, " WHERE (\nid > 10\n) ORDER BY") {
		t.Errorf("it reads %s", st.SQL)
	}
	for _, where := range []string{"1 = 1; DELETE FROM people", "(1 = 1", "1 = 1)", "'open = 1", "1 = 1 /* open"} {
		if _, err := tsql.BuildBrowse(tbl, source.BrowseOptions{Where: where}); err == nil {
			t.Errorf("the condition %q was accepted", where)
		}
	}
	// One condition that is nevertheless not a read: the statement it would
	// make is weighed, not the words somebody typed.
	if _, err := tsql.BuildBrowse(tbl, source.BrowseOptions{
		Where: "id IN (SELECT id INTO taken FROM people)"}); err == nil {
		t.Error("a condition that would make a table was accepted")
	}
}

// A browse is of rows, so a stream's options mean nothing here and are
// refused rather than ignored.
func TestOptionsOfAnotherKindOfSourceAreRefused(t *testing.T) {
	if _, err := tsql.BuildBrowse(tbl, source.BrowseOptions{Seek: &source.Seek{}}); err == nil {
		t.Error("a seek was accepted")
	}
	if _, err := tsql.BuildBrowse(tbl, source.BrowseOptions{Follow: true}); err == nil {
		t.Error("following was accepted")
	}
	if _, err := tsql.BuildBrowse(model.NewRef(model.KindRoutine, "shop", "dbo", "p"), source.BrowseOptions{}); err == nil {
		t.Error("a routine was browsable")
	}
}

// The distinct values of a column, most frequent first, for the picklist
// (FR-3.4).
func TestDistinctValuesComeMostFrequentFirst(t *testing.T) {
	st, err := tsql.buildDistinct(tbl, "name", source.BrowseOptions{
		Filters: []source.Filter{{Column: "id", Op: source.OpGreater, Values: []any{int64(3)}}}}, 20)
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT [name], count(*) FROM [dbo].[people] WHERE [id] > @p1 " +
		"GROUP BY [name] ORDER BY count(*) DESC, [name] OFFSET 0 ROWS FETCH NEXT @p2 ROWS ONLY"
	if st.SQL != want {
		t.Errorf("it reads\n%s\nwant\n%s", st.SQL, want)
	}
	if len(st.Args) != 2 || st.Args[1] != int64(20) {
		t.Errorf("bound %#v", st.Args)
	}
	if _, err := tsql.buildDistinct(tbl, "name", source.BrowseOptions{}, 0); err == nil {
		t.Error("a list of every distinct value was accepted (NFR-P11)")
	}
	if _, err := tsql.buildDistinct(model.NewRef(model.KindRoutine, "shop", "dbo", "p"),
		"name", source.BrowseOptions{}, 5); err == nil {
		t.Error("a routine's distinct values were accepted")
	}
}
