package dynamodb

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The grid's filters as a filter expression.
//
// Every attribute name and every value goes in through a placeholder, and the
// reason is not tidiness: "name" and "size" are two of DynamoDB's several
// hundred reserved words, and a name written into an expression is a name
// interpolated into a statement. So each of these checks the expression's text
// and what was bound beside it.

func TestEveryFilterBindsItsNamesAndValues(t *testing.T) {
	for name, c := range map[string]struct {
		f     source.Filter
		want  string
		attrs []string // the attribute names bound, in placeholder order
		vals  []string // the values bound, as kind and text
	}{
		"equal": {source.Filter{Column: "id", Op: source.OpEqual, Values: []any{int64(7)}},
			"#c0 = :v0", []string{"id"}, []string{"N:7"}},
		"a reserved word": {source.Filter{Column: "name", Op: source.OpEqual, Values: []any{"x"}},
			"#c0 = :v0", []string{"name"}, []string{"S:x"}},
		"not equal": {source.Filter{Column: "n", Op: source.OpNotEqual, Values: []any{int64(1)}},
			"#c0 <> :v0", []string{"n"}, []string{"N:1"}},
		"less": {source.Filter{Column: "n", Op: source.OpLess, Values: []any{int64(3)}},
			"#c0 < :v0", []string{"n"}, []string{"N:3"}},
		"at most": {source.Filter{Column: "n", Op: source.OpLessEqual, Values: []any{int64(3)}},
			"#c0 <= :v0", []string{"n"}, []string{"N:3"}},
		"more": {source.Filter{Column: "n", Op: source.OpGreater, Values: []any{int64(3)}},
			"#c0 > :v0", []string{"n"}, []string{"N:3"}},
		"at least": {source.Filter{Column: "n", Op: source.OpGreaterEqual, Values: []any{int64(3)}},
			"#c0 >= :v0", []string{"n"}, []string{"N:3"}},
		"between": {source.Filter{Column: "n", Op: source.OpBetween, Values: []any{int64(1), int64(9)}},
			"#c0 BETWEEN :v0 AND :v1", []string{"n"}, []string{"N:1", "N:9"}},
		// contains() is the only text search a filter expression has: no case
		// folding and no patterns, which is what the grid's filter means.
		"contains": {source.Filter{Column: "name", Op: source.OpContains, Values: []any{"50%"}},
			"contains(#c0, :v0)", []string{"name"}, []string{"S:50%"}},
		"a prefix pattern": {source.Filter{Column: "name", Op: source.OpLike, Values: []any{"per%"}},
			"begins_with(#c0, :v0)", []string{"name"}, []string{"S:per"}},
		"not a prefix pattern": {source.Filter{Column: "name", Op: source.OpNotLike, Values: []any{"per%"}},
			"NOT begins_with(#c0, :v0)", []string{"name"}, []string{"S:per"}},
		// An attribute nobody wrote and one set to NULL are both "empty" to
		// the grid, so empty means either of them.
		"equal to nothing": {source.Filter{Column: "n", Op: source.OpEqual, Values: []any{nil}},
			"(attribute_not_exists(#c0) OR attribute_type(#c0, :null))", []string{"n"}, []string{"S:NULL"}},
		"not equal to nothing": {source.Filter{Column: "n", Op: source.OpNotEqual, Values: []any{nil}},
			"(attribute_exists(#c0) AND NOT attribute_type(#c0, :null))", []string{"n"}, []string{"S:NULL"}},
		"is null": {source.Filter{Column: "n", Op: source.OpIsNull},
			"attribute_not_exists(#c0)", []string{"n"}, nil},
		"is not null": {source.Filter{Column: "n", Op: source.OpIsNotNull},
			"attribute_exists(#c0)", []string{"n"}, nil},
		"in": {source.Filter{Column: "n", Op: source.OpIn, Values: []any{int64(1), int64(2)}},
			"#c0 IN (:v0, :v1)", []string{"n"}, []string{"N:1", "N:2"}},
		"in, with nothing among them": {source.Filter{Column: "n", Op: source.OpIn, Values: []any{int64(1), nil}},
			"(#c0 IN (:v0) OR attribute_not_exists(#c0))", []string{"n"}, []string{"N:1"}},
		"in nothing at all": {source.Filter{Column: "n", Op: source.OpIn},
			"(attribute_exists(#c0) AND attribute_not_exists(#c0))", []string{"n"}, nil},
		"in only nothing": {source.Filter{Column: "n", Op: source.OpIn, Values: []any{nil}},
			"attribute_not_exists(#c0)", []string{"n"}, nil},
		"not in": {source.Filter{Column: "n", Op: source.OpNotIn, Values: []any{int64(1)}},
			"(NOT #c0 IN (:v0) OR attribute_not_exists(#c0))", []string{"n"}, []string{"N:1"}},
		"not in, nothing among them": {source.Filter{Column: "n", Op: source.OpNotIn, Values: []any{int64(1), nil}},
			"(NOT #c0 IN (:v0) AND attribute_exists(#c0))", []string{"n"}, []string{"N:1"}},
		"not in nothing at all": {source.Filter{Column: "n", Op: source.OpNotIn},
			"(attribute_exists(#c0) OR attribute_not_exists(#c0))", []string{"n"}, nil},
		"not in only nothing": {source.Filter{Column: "n", Op: source.OpNotIn, Values: []any{nil}},
			"attribute_exists(#c0)", []string{"n"}, nil},
		"negated": {source.Filter{Column: "n", Op: source.OpEqual, Values: []any{int64(1)}, Negate: true},
			"NOT (#c0 = :v0)", []string{"n"}, []string{"N:1"}},
	} {
		t.Run(name, func(t *testing.T) {
			e := &expression{}
			got, err := e.filter(c.f)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("it reads %s, want %s", got, c.want)
			}
			for i, attr := range c.attrs {
				placeholder := "#c" + itoa(int64(i))
				if e.names[placeholder] != attr {
					t.Errorf("%s stands for %q, want %q", placeholder, e.names[placeholder], attr)
				}
			}
			if len(e.names) != len(c.attrs) {
				t.Errorf("it bound the names %v, want %v", e.names, c.attrs)
			}
			for i, want := range c.vals {
				placeholder := ":v" + itoa(int64(i))
				if want == "S:NULL" {
					placeholder = ":null"
				}
				av, ok := e.values[placeholder]
				if !ok {
					t.Fatalf("%s was not bound; bound: %v", placeholder, e.values)
				}
				if describeAV(av) != want {
					t.Errorf("%s is %s, want %s", placeholder, describeAV(av), want)
				}
			}
			if len(e.values) != len(c.vals) {
				t.Errorf("it bound %d values, want %d", len(e.values), len(c.vals))
			}
			// Nothing a person typed reaches the expression's text.
			if strings.Contains(got, "name") || strings.Contains(got, "50%") {
				t.Errorf("a name or a value is in the expression: %s", got)
			}
		})
	}
}

// One name used twice is one placeholder: an expression may not bind the same
// name under two, and a filter pair on one attribute is the commonest thing
// the grid builds.
func TestOneNameIsOnePlaceholder(t *testing.T) {
	e := &expression{}
	got, err := e.filters([]source.Filter{
		{Column: "n", Op: source.OpGreater, Values: []any{int64(1)}},
		{Column: "n", Op: source.OpLess, Values: []any{int64(9)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "#c0 > :v0 AND #c0 < :v1" {
		t.Errorf("it reads %s", got)
	}
	if len(e.names) != 1 {
		t.Errorf("it bound the names %v", e.names)
	}
}

func TestFiltersAreJoinedByAnd(t *testing.T) {
	e := &expression{}
	got, err := e.filters([]source.Filter{
		{Column: "a", Op: source.OpEqual, Values: []any{int64(1)}},
		{Column: "b", Op: source.OpEqual, Values: []any{int64(2)}},
		{Column: "c", Op: source.OpIsNull},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "#c0 = :v0 AND #c1 = :v1 AND attribute_not_exists(#c2)" {
		t.Errorf("it reads %s", got)
	}
	if empty, err := (&expression{}).filters(nil); err != nil || empty != "" {
		t.Errorf("no filters render %q, %v", empty, err)
	}
}

func TestAFilterThatCannotBeWrittenIsRefused(t *testing.T) {
	for name, f := range map[string]source.Filter{
		"a regular expression":    {Column: "n", Op: source.OpRegex, Values: []any{"^a"}},
		"an operator nobody has":  {Column: "n", Op: source.FilterOp("~~")},
		"no attribute at all":     {Op: source.OpIsNull},
		"equal to two things":     {Column: "n", Op: source.OpEqual, Values: []any{1, 2}},
		"between one thing":       {Column: "n", Op: source.OpBetween, Values: []any{1}},
		"null with a value":       {Column: "n", Op: source.OpIsNull, Values: []any{1}},
		"ordered against nothing": {Column: "n", Op: source.OpLess, Values: []any{nil}},
		"a value it cannot hold":  {Column: "n", Op: source.OpEqual, Values: []any{struct{}{}}},
		// A pattern whose wildcards are anywhere but the end: DynamoDB has
		// begins_with and contains and no pattern language at all, so matching
		// it would mean matching it somewhere it cannot be matched.
		"a pattern in the middle":     {Column: "n", Op: source.OpLike, Values: []any{"a%b"}},
		"a pattern at the front":      {Column: "n", Op: source.OpLike, Values: []any{"%b"}},
		"a single-character wildcard": {Column: "n", Op: source.OpLike, Values: []any{"a_%"}},
		"no pattern at all":           {Column: "n", Op: source.OpLike, Values: []any{"plain"}},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := (&expression{}).filter(f); err == nil {
				t.Errorf("it rendered %s", got)
			}
		})
	}
}

// The service takes at most a hundred values in an IN, which is said here
// rather than sent and refused.
func TestAListOfMoreThanTheServiceTakesIsRefused(t *testing.T) {
	vals := make([]any, maxIn)
	for i := range vals {
		vals[i] = int64(i)
	}
	if _, err := (&expression{}).filter(source.Filter{Column: "n", Op: source.OpIn, Values: vals}); err != nil {
		t.Fatalf("a list of exactly %d: %v", maxIn, err)
	}
	if _, err := (&expression{}).filter(source.Filter{Column: "n", Op: source.OpIn,
		Values: append(vals, int64(maxIn))}); err == nil {
		t.Errorf("a list of %d was accepted", maxIn+1)
	}
}

func TestWhichLikePatternsAreAPrefix(t *testing.T) {
	for pattern, want := range map[string]string{
		"per%":  "per",
		"%":     "",
		"a_b%":  "",
		"a%b":   "",
		"%b":    "",
		"plain": "",
	} {
		got, ok := prefixOf(pattern)
		if want == "" && pattern != "%" {
			if ok {
				t.Errorf("%q was read as the prefix %q", pattern, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("%q reads as %q (%v), want %q", pattern, got, ok, want)
		}
	}
}
