package clickhouse

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing ClickHouse's own SQL (NFR-S6).

var (
	tsql dialect
	tbl  = model.NewRef(model.KindTable, "shop", "people")
)

// A name goes in backquotes, whatever is in it.
func TestANameIsBackquoted(t *testing.T) {
	d := tsql
	for name, want := range map[string]string{
		"people":       "`people`",
		"a b":          "`a b`",
		"":             "``",
		"drop table t": "`drop table t`",
		"a`b":          "`a``b`",
		"`":            "````",
		`a"b`:          "`a\"b`",
	} {
		if got := d.QuoteIdentifier(name); got != want {
			t.Errorf("QuoteIdentifier(%q) = %q, want %q", name, got, want)
		}
	}
}

// A reference is written with its database. There is no schema between the
// two, so two parts is all there is.
func TestAReferenceIsWrittenDatabaseDeep(t *testing.T) {
	d := tsql
	cases := []struct {
		ref  model.ObjectRef
		want string
	}{
		{model.NewRef(model.KindTable, "shop", "people"), "`shop`.`people`"},
		{model.NewRef(model.KindColumn, "shop", "people", "name"), "`shop`.`people`"},
		{model.NewRef(model.KindTable, "people"), "`people`"},
	}
	for _, c := range cases {
		if got := d.QualifyRef(c.ref); got != c.want {
			t.Errorf("QualifyRef(%s) = %q, want %q", c.ref, got, c.want)
		}
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
	want := "SELECT * FROM `shop`.`people` LIMIT ? OFFSET ?"
	if st.SQL != want {
		t.Errorf("it reads\n%s\nwant\n%s", st.SQL, want)
	}
	if len(st.Args) != 2 || st.Args[0] != int64(25) || st.Args[1] != int64(50) {
		t.Errorf("bound %#v, want the limit then the offset", st.Args)
	}
	// Nothing to skip is no OFFSET at all.
	if st := browse(t, source.BrowseOptions{Limit: 5}); strings.Contains(st.SQL, "OFFSET") {
		t.Errorf("it reads %s", st.SQL)
	}
}

// Asked for no page, it reads one page's worth rather than the table
// (NFR-P11).
func TestABrowseWithoutALimitStillHasOne(t *testing.T) {
	st := browse(t, source.BrowseOptions{})
	if len(st.Args) != 1 || st.Args[0] != int64(DefaultPageSize) {
		t.Errorf("bound %#v, want a default page size", st.Args)
	}
}

// Only the columns asked for, each named as a name.
func TestABrowseReadsTheColumnsAskedFor(t *testing.T) {
	st := browse(t, source.BrowseOptions{Columns: []string{"id", "full name"}})
	if !strings.HasPrefix(st.SQL, "SELECT `id`, `full name` FROM `shop`.`people`") {
		t.Errorf("it reads %s", st.SQL)
	}
}

// ClickHouse says where the NULLs go in the standard's own words, so there
// is no expression standing in for it.
func TestASortSaysWhereTheNullsGo(t *testing.T) {
	st := browse(t, source.BrowseOptions{Sorts: []source.Sort{{Column: "name", Descending: true}}})
	if !strings.Contains(st.SQL, " ORDER BY `name` DESC NULLS LAST LIMIT") {
		t.Errorf("it sorts\n%s", st.SQL)
	}
	st = browse(t, source.BrowseOptions{Sorts: []source.Sort{{Column: "name", NullsFirst: true}, {Column: "id"}}})
	if !strings.Contains(st.SQL, " ORDER BY `name` NULLS FIRST, `id` NULLS LAST LIMIT") {
		t.Errorf("it sorts\n%s", st.SQL)
	}
	// Nothing to sort by is no ORDER BY: the engine's own order is what a
	// table without one has.
	if st := browse(t, source.BrowseOptions{}); strings.Contains(st.SQL, "ORDER BY") {
		t.Errorf("it sorts by nothing and says so: %s", st.SQL)
	}
}

// Each filter means what it means on every other engine.
func TestEachFilterMeansWhatItMeansEverywhere(t *testing.T) {
	cases := []struct {
		f    source.Filter
		want string
		args []any
	}{
		{source.Filter{Column: "n", Op: source.OpEqual, Values: []any{int64(1)}}, "`n` = ?", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpNotEqual, Values: []any{int64(1)}}, "`n` != ?", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpLess, Values: []any{int64(1)}}, "`n` < ?", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpLessEqual, Values: []any{int64(1)}}, "`n` <= ?", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpGreater, Values: []any{int64(1)}}, "`n` > ?", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpGreaterEqual, Values: []any{int64(1)}}, "`n` >= ?", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpEqual, Values: []any{nil}}, "`n` IS NULL", nil},
		{source.Filter{Column: "n", Op: source.OpNotEqual, Values: []any{nil}}, "`n` IS NOT NULL", nil},
		{source.Filter{Column: "n", Op: source.OpIsNull}, "`n` IS NULL", nil},
		{source.Filter{Column: "n", Op: source.OpIsNotNull}, "`n` IS NOT NULL", nil},
		{source.Filter{Column: "n", Op: source.OpBetween, Values: []any{int64(1), int64(9)}},
			"`n` BETWEEN ? AND ?", []any{int64(1), int64(9)}},
		{source.Filter{Column: "s", Op: source.OpLike, Values: []any{"a%"}},
			"toString(`s`) LIKE ?", []any{"a%"}},
		{source.Filter{Column: "s", Op: source.OpNotLike, Values: []any{"a%"}},
			"toString(`s`) NOT LIKE ?", []any{"a%"}},
		{source.Filter{Column: "s", Op: source.OpContains, Values: []any{"50%"}},
			"toString(`s`) ILIKE ?", []any{`%50\%%`}},
		{source.Filter{Column: "s", Op: source.OpRegex, Values: []any{"^a"}},
			"match(toString(`s`), ?)", []any{"^a"}},
		{source.Filter{Column: "n", Op: source.OpIn, Values: []any{int64(1), int64(2)}},
			"`n` IN (?, ?)", []any{int64(1), int64(2)}},
		{source.Filter{Column: "n", Op: source.OpNotIn, Values: []any{int64(1)}},
			"(`n` NOT IN (?) OR `n` IS NULL)", []any{int64(1)}},
		{source.Filter{Column: "n", Op: source.OpEqual, Values: []any{int64(1)}, Negate: true},
			"NOT (`n` = ?)", []any{int64(1)}},
	}
	for _, c := range cases {
		st := browse(t, source.BrowseOptions{Filters: []source.Filter{c.f}})
		if !strings.Contains(st.SQL, " WHERE "+c.want+" LIMIT") {
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
		{source.OpIn, []any{int64(1), nil}, "(`n` IN (?) OR `n` IS NULL)"},
		{source.OpIn, []any{nil}, "`n` IS NULL"},
		{source.OpIn, nil, "1 = 0"},
		{source.OpNotIn, []any{int64(1), nil}, "`n` NOT IN (?)"},
		{source.OpNotIn, []any{int64(1)}, "(`n` NOT IN (?) OR `n` IS NULL)"},
		{source.OpNotIn, []any{nil}, "`n` IS NOT NULL"},
		{source.OpNotIn, nil, "1 = 1"},
	}
	for _, c := range cases {
		st := browse(t, source.BrowseOptions{Filters: []source.Filter{{Column: "n", Op: c.op, Values: c.vals}}})
		if !strings.Contains(st.SQL, " WHERE "+c.want+" LIMIT") {
			t.Errorf("%s of %#v reads\n%s\nwant it to hold %q", c.op, c.vals, st.SQL, c.want)
		}
	}
}

// Filters are joined by AND, in the order they were given.
func TestFiltersAreJoinedByAnd(t *testing.T) {
	st := browse(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "a", Op: source.OpIsNull}, {Column: "b", Op: source.OpIsNotNull}}})
	if !strings.Contains(st.SQL, " WHERE `a` IS NULL AND `b` IS NOT NULL LIMIT") {
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
		{Column: "s", Op: source.OpRegex},
		{Column: "s", Op: source.OpLike, Values: []any{"a", "b"}},
		{Column: "n", Op: source.FilterOp("wat"), Values: []any{1}},
	} {
		if _, err := tsql.BuildBrowse(tbl, source.BrowseOptions{Filters: []source.Filter{f}}); err == nil {
			t.Errorf("filter %s of %#v was accepted", f.Op, f.Values)
		}
	}
}

// A search finds the text somebody typed, and not a pattern made of it.
// ClickHouse escapes with a backslash and has no ESCAPE clause to name
// another character.
func TestTheTextOfASearchIsNotAPattern(t *testing.T) {
	for text, want := range map[string]string{
		"50%":   `50\%`,
		"a_b":   `a\_b`,
		`a\b`:   `a\\b`,
		"plain": "plain",
		`\%`:    `\\\%`,
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
	if !strings.Contains(st.SQL, " WHERE (\nid > 10\n) LIMIT") {
		t.Errorf("it reads %s", st.SQL)
	}
	for _, where := range []string{"1 = 1; SELECT 1", "(1 = 1", "1 = 1)", "'open = 1", "1 = 1 /* open"} {
		if _, err := tsql.BuildBrowse(tbl, source.BrowseOptions{Where: where}); err == nil {
			t.Errorf("the condition %q was accepted", where)
		}
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
	if _, err := tsql.BuildBrowse(model.NewRef(model.KindRoutine, "shop", "d"), source.BrowseOptions{}); err == nil {
		t.Error("a dictionary was browsable")
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
	want := "SELECT `name`, count() FROM `shop`.`people` WHERE `id` > ? " +
		"GROUP BY `name` ORDER BY count() DESC, `name` NULLS LAST LIMIT ?"
	if st.SQL != want {
		t.Errorf("it reads\n%s\nwant\n%s", st.SQL, want)
	}
	if len(st.Args) != 2 || st.Args[1] != int64(20) {
		t.Errorf("bound %#v", st.Args)
	}
	if _, err := tsql.buildDistinct(tbl, "name", source.BrowseOptions{}, 0); err == nil {
		t.Error("a list of every distinct value was accepted (NFR-P11)")
	}
	if _, err := tsql.buildDistinct(model.NewRef(model.KindRoutine, "shop", "d"),
		"name", source.BrowseOptions{}, 5); err == nil {
		t.Error("a dictionary's distinct values were accepted")
	}
}

// A script is cut at its semicolons, and at nothing else: ClickHouse has no
// body to keep whole.
func TestAScriptIsCutAtItsSemicolons(t *testing.T) {
	cases := []struct {
		script string
		want   []string
	}{
		{"SELECT 1; SELECT 2", []string{"SELECT 1", "SELECT 2"}},
		{"SELECT 1", []string{"SELECT 1"}},
		{"SELECT ';'", []string{"SELECT ';'"}},
		{"SELECT `a;b`", []string{"SELECT `a;b`"}},
		{`SELECT "a;b"`, []string{`SELECT "a;b"`}},
		{"SELECT 1 -- ; a note\n", []string{"SELECT 1"}},
		{"-- only a note", nil},
		{"", nil},
	}
	for _, c := range cases {
		got := tsql.SplitScript(c.script)
		if len(got) != len(c.want) {
			t.Errorf("%q split into %d statements, want %d", c.script, len(got), len(c.want))
			continue
		}
		for i := range got {
			if got[i].Text != c.want[i] {
				t.Errorf("%q gave %q, want %q", c.script, got[i].Text, c.want[i])
			}
		}
	}
}
