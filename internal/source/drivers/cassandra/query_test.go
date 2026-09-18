package cassandra

import (
	"math/big"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"gopkg.in/inf.v0"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// native is a CQL type as the protocol names it.
func native(t gocql.Type) gocql.NativeType { return gocql.NewNativeType(4, t, "") }

func TestATypeIsWrittenAsCQLWritesOne(t *testing.T) {
	for want, info := range map[string]gocql.TypeInfo{
		// The protocol calls text varchar; a person reads text.
		"text":      native(gocql.TypeVarchar),
		"int":       native(gocql.TypeInt),
		"bigint":    native(gocql.TypeBigInt),
		"timestamp": native(gocql.TypeTimestamp),
		"list<text>": gocql.CollectionType{
			NativeType: native(gocql.TypeList), Elem: native(gocql.TypeVarchar)},
		"set<int>": gocql.CollectionType{
			NativeType: native(gocql.TypeSet), Elem: native(gocql.TypeInt)},
		"map<text, int>": gocql.CollectionType{
			NativeType: native(gocql.TypeMap), Key: native(gocql.TypeVarchar), Elem: native(gocql.TypeInt)},
		"tuple<int, text>": gocql.TupleTypeInfo{
			NativeType: native(gocql.TypeTuple),
			Elems:      []gocql.TypeInfo{native(gocql.TypeInt), native(gocql.TypeVarchar)}},
		// A keyspace's own type is its name here; its fields are the
		// structure tab's business, not a column heading's.
		"address": gocql.UDTTypeInfo{NativeType: native(gocql.TypeUDT), Name: "address", KeySpace: "shop"},
	} {
		if got := typeText(info); got != want {
			t.Errorf("a %v is written %s, want %s", info.Type(), got, want)
		}
	}
	// And what is written is then classified as the model's own.
	if got := cqlDataType(typeText(gocql.CollectionType{
		NativeType: native(gocql.TypeList), Elem: native(gocql.TypeVarchar)})); got.Class != model.TypeArray {
		t.Errorf("a list is classified %v", got.Class)
	}
}

func TestUSEMovesAConsoleAndNothingElse(t *testing.T) {
	for text, want := range map[string]string{
		"USE shop":     "shop",
		"use shop;":    "shop",
		"  USE Shop  ": "shop", // CQL folds an unquoted name
		`USE "Shop"`:   "Shop", // and a quoted one keeps its case
	} {
		got, ok := useKeyspace(text)
		if !ok || got != want {
			t.Errorf("%q moves to %q (%v), want %q", text, got, ok, want)
		}
	}
	for _, text := range []string{
		"SELECT * FROM t", "USE", "USE shop extra", "USER shop", "",
	} {
		if got, ok := useKeyspace(text); ok {
			t.Errorf("%q was read as a move to %q", text, got)
		}
	}
}

func TestAValueIsOneTheModelHolds(t *testing.T) {
	uuid, err := gocql.ParseUUID("9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d")
	if err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 9, 18, 10, 30, 0, 0, time.UTC)
	for what, c := range map[string]struct {
		in   any
		want any
	}{
		"nothing":          {nil, nil},
		"a word":           {"Ada", "Ada"},
		"a whole number":   {int(7), int64(7)},
		"a small one":      {int16(7), int64(7)},
		"a tiny one":       {int8(7), int64(7)},
		"a wide one":       {int32(7), int64(7)},
		"one already wide": {int64(7), int64(7)},
		"a short float":    {float32(2.5), float64(2.5)},
		"a float":          {float64(2.5), float64(2.5)},
		"a truth":          {true, true},
		"an instant":       {when, when},
		"a uuid":           {uuid, "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d"},
		"a decimal":        {inf.NewDec(2545, 2), model.Decimal("25.45")},
		"a varint past a whole number": {new(big.Int).SetUint64(18446744073709551615),
			model.Decimal("18446744073709551615")},
		"a decimal that is nothing": {(*inf.Dec)(nil), nil},
		"a varint that is nothing":  {(*big.Int)(nil), nil},
		"a time of day":             {13*time.Hour + 30*time.Minute, "13:30:00"},
		"a duration":                {gocql.Duration{Months: 1, Days: 2, Nanoseconds: int64(3 * time.Hour)}, "1mo2d3h"},
	} {
		if got := normalize(c.in); got != c.want {
			t.Errorf("%s reads as %v (%T), want %v", what, got, got, c.want)
		}
	}
	// Bytes are copied: a driver reuses the buffer it scanned into.
	raw := []byte{1, 2, 3}
	got, ok := normalize(raw).([]byte)
	if !ok || len(got) != 3 {
		t.Fatalf("bytes read as %v", got)
	}
	raw[0] = 9
	if got[0] != 1 {
		t.Error("the bytes read were the driver's own buffer")
	}
	// A collection is a structure, which the cell viewer reads as JSON.
	if got := normalize([]string{"go", "cql"}); string(got.(model.JSON)) != `["go","cql"]` {
		t.Errorf("a set reads as %v", got)
	}
	if got := normalize(map[string]int{"a": 1}); string(got.(model.JSON)) != `{"a":1}` {
		t.Errorf("a map reads as %v", got)
	}
	// A map's keys are values of their own, and JSON's are text.
	if got := normalize(map[int]string{7: "x"}); string(got.(model.JSON)) != `{"7":"x"}` {
		t.Errorf("a map keyed by a number reads as %v", got)
	}
}

func TestADurationIsWrittenAsCQLWritesOne(t *testing.T) {
	for want, d := range map[string]gocql.Duration{
		"0s":     {},
		"1mo":    {Months: 1},
		"1y2mo":  {Months: 14},
		"3d":     {Days: 3},
		"1h":     {Nanoseconds: int64(time.Hour)},
		"90ns":   {Nanoseconds: 90},
		"1ms":    {Nanoseconds: int64(time.Millisecond)},
		"1m30s":  {Nanoseconds: int64(90 * time.Second)},
		"-1mo2d": {Months: -1, Days: -2},
	} {
		if got := durationText(d); got != want {
			t.Errorf("%+v is written %s, want %s", d, got, want)
		}
	}
}

func TestATimeIsAReadingOfTheClock(t *testing.T) {
	for want, d := range map[string]time.Duration{
		"00:00:00":           0,
		"13:30:00":           13*time.Hour + 30*time.Minute,
		"23:59:59":           23*time.Hour + 59*time.Minute + 59*time.Second,
		"00:00:00.000000001": 1,
		"01:02:03.5":         time.Hour + 2*time.Minute + 3*time.Second + 500*time.Millisecond,
	} {
		if got := timeOfDay(d); got != want {
			t.Errorf("%v reads as %s, want %s", d, got, want)
		}
	}
}
