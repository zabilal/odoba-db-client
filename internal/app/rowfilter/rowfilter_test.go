package rowfilter

import (
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Filtering rows here rather than on the server (T2.67, FR-13.9). What these
// hold is that the same filter means the same thing here as it does there.

var cols = []model.ColumnDef{
	{Name: "partition", Type: model.DataType{Class: model.TypeInteger}},
	{Name: "offset", Type: model.DataType{Class: model.TypeInteger}},
	{Name: "timestamp", Type: model.DataType{Class: model.TypeTimestamp}},
	{Name: "key", Type: model.DataType{Class: model.TypeBytes, Nullable: true}},
	{Name: "value", Type: model.DataType{Class: model.TypeBytes, Nullable: true}},
	{Name: "headers", Type: model.DataType{Class: model.TypeArray, Nullable: true}},
}

var when = time.Date(2026, 9, 19, 10, 30, 0, 0, time.UTC)

func record(key, value string, headers ...string) model.Row {
	row := model.Row{int64(0), int64(10), when, []byte(key), []byte(value), nil}
	if key == "" {
		row[3] = nil // a record with no key at all
	}
	if len(headers) > 0 {
		var list []any
		for i := 0; i+1 < len(headers); i += 2 {
			list = append(list, map[string]any{"key": headers[i], "value": []byte(headers[i+1])})
		}
		row[5] = list
	}
	return row
}

func matches(t *testing.T, row model.Row, filters ...source.Filter) bool {
	t.Helper()
	m, err := Compile(cols, filters)
	if err != nil {
		t.Fatalf("compiling %v: %v", filters, err)
	}
	return m.Match(row)
}

func f(col string, op source.FilterOp, vals ...any) source.Filter {
	return source.Filter{Column: col, Op: op, Values: vals}
}

func TestBytesAreFilteredAsTheTextTheyCarry(t *testing.T) {
	row := record("order-1", `{"total":12.50}`)

	// A person types a word, not its bytes.
	if !matches(t, row, f("key", source.OpEqual, "order-1")) {
		t.Error("a key did not equal the word it carries")
	}
	if matches(t, row, f("key", source.OpEqual, "order-2")) {
		t.Error("a key equalled a word it does not carry")
	}
	// Contains ignores case, as it does on every other source.
	if !matches(t, row, f("value", source.OpContains, "TOTAL")) {
		t.Error("contains did not ignore case")
	}
	if !matches(t, row, f("value", source.OpRegex, `total":\s*12\.50`)) {
		t.Error("a regular expression did not match the value")
	}
}

func TestAHeaderIsFoundByItsNameOrItsPair(t *testing.T) {
	row := record("k", "v", "trace-id", "abc123", "trace-id", "second", "retry", "1")

	// Typing the name finds the header.
	if !matches(t, row, f("headers", source.OpEqual, "trace-id")) {
		t.Error("a header was not found by its name")
	}
	// Typing the pair finds that pairing, and a name that repeats keeps both
	// of its values findable.
	if !matches(t, row, f("headers", source.OpContains, "trace-id=abc123")) {
		t.Error("the first of a repeated header was not found")
	}
	if !matches(t, row, f("headers", source.OpContains, "trace-id=second")) {
		t.Error("the second of a repeated header was not found")
	}
	if matches(t, row, f("headers", source.OpContains, "trace-id=third")) {
		t.Error("a header pairing that was never sent was found")
	}
	// A record with no headers has none of them.
	if matches(t, record("k", "v"), f("headers", source.OpEqual, "trace-id")) {
		t.Error("a record with no headers had one")
	}
}

func TestNullFollowsThePicklistsRulesAndNotSQLs(t *testing.T) {
	none := record("", "v") // no key
	some := record("order-1", "v")

	if !matches(t, none, f("key", source.OpIsNull)) || matches(t, some, f("key", source.OpIsNull)) {
		t.Error("IS NULL did not pick out the record with no key")
	}
	// !=x parses to NOT IN precisely so that a record with no key is kept:
	// nobody typing !=order-1 asked to hide the keyless ones.
	if !matches(t, none, f("key", source.OpNotIn, "order-1")) {
		t.Error("a record with no key was hidden by !=order-1")
	}
	if matches(t, some, f("key", source.OpNotIn, "order-1")) {
		t.Error("the record it names was not excluded")
	}
	// Listing NULL among the values is how somebody asks to drop them.
	if matches(t, none, f("key", source.OpNotIn, "order-1", nil)) {
		t.Error("a record with no key survived being unticked")
	}
	// IN with NULL among the values picks the keyless ones up.
	if !matches(t, none, f("key", source.OpIn, "other", nil)) {
		t.Error("IN with NULL listed did not match the record with no key")
	}
	// An empty list selects nothing; excluding nothing keeps everything.
	if matches(t, some, f("key", source.OpIn)) {
		t.Error("an empty list selected something")
	}
	if !matches(t, some, f("key", source.OpNotIn)) {
		t.Error("excluding nothing did not keep everything")
	}
	// And NULL is not greater, less or contained.
	for _, flt := range []source.Filter{
		f("key", source.OpGreater, "a"), f("key", source.OpContains, "a"), f("key", source.OpEqual, "a"),
		f("key", source.OpLess, "zzz"), f("key", source.OpLessEqual, "zzz"),
		f("key", source.OpBetween, "a", "zzz"), f("key", source.OpLike, "%"),
	} {
		if matches(t, none, flt) {
			t.Errorf("a record with no key matched %s", flt.Op)
		}
	}
}

func TestNumbersAndTimesCompareAsThemselves(t *testing.T) {
	row := record("k", "v")

	if !matches(t, row, f("offset", source.OpGreaterEqual, int64(10))) {
		t.Error("an offset did not compare as a number")
	}
	if matches(t, row, f("offset", source.OpGreater, int64(10))) {
		t.Error("an offset compared as greater than itself")
	}
	if !matches(t, row, f("offset", source.OpBetween, int64(5), int64(10))) {
		t.Error("between is inclusive at its ends")
	}
	if !matches(t, row, f("timestamp", source.OpGreaterEqual, "2026-09-19")) {
		t.Error("a time did not compare with a date")
	}
	// Two filters are AND, as they are on a server.
	if matches(t, row, f("offset", source.OpEqual, int64(10)), f("key", source.OpEqual, "other")) {
		t.Error("a row matched when only one of two filters held")
	}
}

func TestAPatternMeansWhatItMeansEverywhereElse(t *testing.T) {
	row := record("order-1", "v")

	if !matches(t, row, f("key", source.OpLike, "order%")) {
		t.Error("% did not stand for a run of characters")
	}
	if !matches(t, row, f("key", source.OpLike, "order-_")) {
		t.Error("_ did not stand for one character")
	}
	if matches(t, row, f("key", source.OpLike, "order")) {
		t.Error("a pattern matched more than the whole value")
	}
	// Anchored at the start as well as the end: a pattern describes the whole
	// value, so one matching its tail matches nothing.
	if matches(t, row, f("key", source.OpLike, "rder%")) {
		t.Error("a pattern matched from the middle of a value")
	}
	// A dot is a dot, not any character: the pattern is not a regular
	// expression, and escaping is what keeps it from becoming one.
	if matches(t, record("axb", "v"), f("key", source.OpLike, "a.b")) {
		t.Error("a dot in a pattern matched any character")
	}
	if !matches(t, record("a.b", "v"), f("key", source.OpLike, "a.b")) {
		t.Error("a dot did not match itself")
	}
	if matches(t, row, f("key", source.OpNotLike, "order%")) {
		t.Error("NOT LIKE matched what LIKE matched")
	}
}

func TestWhatCannotBeAnsweredIsNotPretendedTo(t *testing.T) {
	row := record("k", "v")

	// A filter on a column the row has not got matches nothing: it was asked
	// and cannot be answered, which is not the same as being satisfied.
	if matches(t, row, f("nosuch", source.OpEqual, "x")) {
		t.Error("a filter on a column that is not there matched")
	}
	// A pattern that is not a pattern is said now, not left to match nothing.
	if _, err := Compile(cols, []source.Filter{f("key", source.OpRegex, "(")}); err == nil {
		t.Error("a broken regular expression compiled")
	}
	// Negate inverts the whole predicate.
	m, err := Compile(cols, []source.Filter{{Column: "key", Op: source.OpContains, Values: []any{"k"}, Negate: true}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Match(row) {
		t.Error("a negated filter matched what it names")
	}
}
