package clickhouse

import (
	"math/big"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Narrowing what the driver hands over to the model's closed set.

func TestAValueTakesTheModelsShape(t *testing.T) {
	when := time.Date(2024, 3, 9, 14, 5, 6, 0, time.UTC)
	cases := []struct {
		v    any
		dt   model.DataType
		want any
	}{
		{nil, dataType("UInt8"), nil},
		{true, dataType("Bool"), true},
		{"hello", dataType("String"), "hello"},
		{when, dataType("DateTime"), when},
		{int8(-1), dataType("Int8"), int64(-1)},
		{int16(-1), dataType("Int16"), int64(-1)},
		{int32(-1), dataType("Int32"), int64(-1)},
		{int64(-1), dataType("Int64"), int64(-1)},
		{int(-1), dataType("Int64"), int64(-1)},
		{uint8(1), dataType("UInt8"), int64(1)},
		{uint16(1), dataType("UInt16"), int64(1)},
		{uint32(1), dataType("UInt32"), int64(1)},
		{uint64(1), dataType("UInt64"), int64(1)},
		{float32(1.5), dataType("Float32"), float64(1.5)},
		{float64(1.5), dataType("Float64"), float64(1.5)},
		// Past what an int64 holds, so it travels as exact text.
		{uint64(18446744073709551615), dataType("UInt64"), model.Decimal("18446744073709551615")},
		// An exact number arrives as its own text and is read as one.
		{"1.5", dataType("Decimal(10, 2)"), model.Decimal("1.50")},
		{"6f9619ff-8b86-d011-b42d-00c04fc964ff", dataType("UUID"), "6f9619ff-8b86-d011-b42d-00c04fc964ff"},
		{`{"a":1}`, dataType("JSON"), model.JSON(`{"a":1}`)},
		{`not json`, dataType("JSON"), "not json"},
		// An address writes itself, and is four bytes if it is walked.
		{net.IPv4(10, 0, 0, 2), dataType("IPv4"), "10.0.0.2"},
		// math/big writes itself through a pointer.
		{*big.NewInt(7), dataType("Int128"), model.Decimal("7")},
		{big.NewInt(7), dataType("Int128"), model.Decimal("7")},
	}
	for _, c := range cases {
		if got := normalize(c.v, c.dt); !reflect.DeepEqual(got, c.want) {
			t.Errorf("normalize(%#v, %s) = %#v, want %#v", c.v, c.dt.Native, got, c.want)
		}
	}
}

// A value made of other values is walked, and each of them narrowed.
func TestAValueMadeOfOthersIsWalked(t *testing.T) {
	got := normalize([]uint8{1, 2}, dataType("Array(UInt8)"))
	if !reflect.DeepEqual(got, []any{int64(1), int64(2)}) {
		t.Errorf("an array of numbers came back as %#v", got)
	}
	got = normalize([]string{"a"}, dataType("Array(LowCardinality(String))"))
	if !reflect.DeepEqual(got, []any{"a"}) {
		t.Errorf("an array of names came back as %#v", got)
	}
	got = normalize(map[string]uint8{"a": 1}, dataType("Map(String, UInt8)"))
	if !reflect.DeepEqual(got, map[string]any{"a": int64(1)}) {
		t.Errorf("a map came back as %#v", got)
	}
	// A list of lists, each element narrowed by what the type says it is.
	got = normalize([][]uint8{{1}}, dataType("Array(Array(UInt8))"))
	if !reflect.DeepEqual(got, []any{[]any{int64(1)}}) {
		t.Errorf("a list of lists came back as %#v", got)
	}
	// A column that may hold nothing arrives as a pointer to what it holds.
	n := uint8(3)
	if got := normalize(&n, dataType("Nullable(UInt8)")); got != int64(3) {
		t.Errorf("a pointer came back as %#v", got)
	}
	var none *uint8
	if got := normalize(none, dataType("Nullable(UInt8)")); got != nil {
		t.Errorf("a pointer to nothing came back as %#v", got)
	}
	// An Array(UInt8) is a []byte in Go as much as a String is, and only
	// the column says which is which.
	if got := normalize([]byte{1, 2}, dataType("Array(UInt8)")); !reflect.DeepEqual(got, []any{int64(1), int64(2)}) {
		t.Errorf("a list of small numbers came back as %#v", got)
	}
	if got := normalize([]byte("hi"), dataType("String")); got != "hi" {
		t.Errorf("a byte string came back as %#v", got)
	}
}

// An exact number keeps the places its column has: one shown with fewer is
// a different number to anybody reading it.
func TestAnExactNumberKeepsItsPlaces(t *testing.T) {
	cases := []struct {
		s      string
		places int32
		want   string
	}{
		{"1.5", 2, "1.50"},
		{"1", 2, "1.00"},
		{"1.50", 2, "1.50"},
		{"1.555", 2, "1.555"},
		{"1.5", 0, "1.5"},
		{"", 2, ""},
		{"-1.5", 3, "-1.500"},
	}
	for _, c := range cases {
		if got := scaled(c.s, c.places); got != c.want {
			t.Errorf("scaled(%q, %d) = %q, want %q", c.s, c.places, got, c.want)
		}
	}
}

// A sorting key's parts that are a column are what paging may order by; one
// that is an expression over a column is not a name to write into a
// statement (NFR-S6).
func TestOnlyASortingKeysColumnsAreOrderedBy(t *testing.T) {
	cases := []struct {
		key  string
		want []string
	}{
		{"id", []string{"id"}},
		{"(person_id, id)", []string{"person_id", "id"}},
		{"toYYYYMM(d), id", []string{"id"}},
		{"`odd name`, id", []string{"odd name", "id"}},
		{"`a,b`", []string{"a,b"}},
		{"", nil},
		{"tuple()", nil},
		{"cityHash64(a) % 8", nil},
	}
	for _, c := range cases {
		got := plainNames(c.key)
		if len(got) != len(c.want) {
			t.Errorf("plainNames(%q) = %q, want %q", c.key, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("plainNames(%q) = %q, want %q", c.key, got, c.want)
				break
			}
		}
	}
}

func TestWhatIsOneWord(t *testing.T) {
	for s, want := range map[string]bool{
		"id": true, "person_id": true, "_x": true, "a1": true,
		"1a": false, "a b": false, "f(a)": false, "a-b": false, "": true,
	} {
		if got := isPlainName(s); got != want {
			t.Errorf("isPlainName(%q) = %v, want %v", s, got, want)
		}
	}
}

// Paging ends its order with the sorting key, and never names a column
// somebody already sorted by.
func TestPagingEndsItsOrderWithTheSortingKey(t *testing.T) {
	got := tiebreak([]source.Sort{{Column: "Name", Descending: true}}, []string{"nAmE", "id"})
	if len(got) != 2 || got[0].Column != "Name" || got[1].Column != "id" {
		t.Errorf("the order is %+v, want the sort then what it does not already name", got)
	}
	if got := tiebreak(nil, []string{"id"}); len(got) != 1 || got[0].Column != "id" {
		t.Errorf("with no sort of its own the order is %+v", got)
	}
	if got := tiebreak(nil, nil); len(got) != 0 {
		t.Errorf("with nothing to order by the order is %+v", got)
	}
}

// A badge has room for a few characters, so a count is abbreviated.
func TestACountIsAbbreviatedForABadge(t *testing.T) {
	cases := map[int64]string{
		0: "0", 1: "1", 999: "999", 1000: "1K", 1234: "1.2K",
		1_000_000: "1M", 12_300_000: "12.3M", 1_000_000_000: "1B", 2_500_000_000: "2.5B",
	}
	for n, want := range cases {
		if got := humanCount(n); got != want {
			t.Errorf("humanCount(%d) = %q, want %q", n, got, want)
		}
	}
}
