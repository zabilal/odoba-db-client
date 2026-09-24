//go:build duckdb

package duckdb

import (
	"math/big"
	"testing"
	"time"

	duck "github.com/marcboeker/go-duckdb/v2"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A type is read the way it was declared, and keeps what was declared as
// the name beside it.
func TestATypeIsReadAsItWasDeclared(t *testing.T) {
	for _, c := range []struct {
		declared string
		class    model.TypeClass
	}{
		{"BOOLEAN", model.TypeBool},
		{"TINYINT", model.TypeInteger},
		{"SMALLINT", model.TypeInteger},
		{"INTEGER", model.TypeInteger},
		{"BIGINT", model.TypeInteger},
		{"UBIGINT", model.TypeInteger},
		{"FLOAT", model.TypeFloat},
		{"DOUBLE", model.TypeFloat},
		{"VARCHAR", model.TypeString},
		{"BLOB", model.TypeBytes},
		{"DATE", model.TypeDate},
		{"TIME", model.TypeTime},
		{"TIMESTAMP", model.TypeTimestamp},
		{"TIMESTAMPTZ", model.TypeTimestamp},
		{"TIMESTAMP WITH TIME ZONE", model.TypeTimestamp},
		{"INTERVAL", model.TypeInterval},
		{"UUID", model.TypeUUID},
		{"JSON", model.TypeJSON},
		{"BIT", model.TypeBit},
		{"STRUCT(a INTEGER)", model.TypeStruct},
		{"MAP(VARCHAR, INTEGER)", model.TypeStruct},
		{"UNION(a INTEGER)", model.TypeStruct},
		{"ENUM('x','y')", model.TypeEnum},
		// A HUGEINT holds more than an int64 does, so it is kept exact
		// rather than as an integer this application could not carry.
		{"HUGEINT", model.TypeDecimal},
		{"UHUGEINT", model.TypeDecimal},
	} {
		got := dataType(c.declared)
		if got.Class != c.class {
			t.Errorf("%s reads as %v, want %v", c.declared, got.Class, c.class)
		}
		if got.Native != c.declared {
			t.Errorf("%s kept the name %q", c.declared, got.Native)
		}
	}
	// A type this driver has not been taught keeps the name it was
	// declared with, which says more than "unknown" does.
	if got := dataType("SOMETHING_ELSE"); got.Class != model.TypeUnknown || got.Native != "SOMETHING_ELSE" {
		t.Errorf("an unknown type reads as %v %q", got.Class, got.Native)
	}
	if got := dataType(""); got.Class != model.TypeUnknown {
		t.Errorf("nothing reads as %v", got.Class)
	}
}

// A number's width is part of its type, because a column of ten places is
// not the same column as one of thirty.
func TestANumbersWidthIsPartOfIt(t *testing.T) {
	got := dataType("DECIMAL(30,10)")
	if got.Class != model.TypeDecimal || got.Precision != 30 || got.Scale != 10 {
		t.Errorf("DECIMAL(30,10) reads as %v (%d,%d)", got.Class, got.Precision, got.Scale)
	}
	if got := dataType("DECIMAL"); got.Precision != 0 || got.Scale != 0 {
		t.Errorf("an unconstrained number reads as (%d,%d)", got.Precision, got.Scale)
	}
	if got := dataType("VARCHAR(20)"); got.Class != model.TypeString || got.Length != 20 {
		t.Errorf("VARCHAR(20) reads as %v length %d", got.Class, got.Length)
	}
	if got := dataType("VARCHAR"); got.Length != -1 {
		t.Errorf("an unbounded string reads as length %d", got.Length)
	}
}

// A time that carries a zone says so, whichever way it is spelled.
func TestATimeSaysWhetherItCarriesAZone(t *testing.T) {
	for _, name := range []string{"TIMESTAMPTZ", "TIMESTAMP WITH TIME ZONE", "TIMETZ"} {
		if !dataType(name).TimeZone {
			t.Errorf("%s does not say it carries a zone", name)
		}
	}
	for _, name := range []string{"TIMESTAMP", "TIME", "DATE"} {
		if dataType(name).TimeZone {
			t.Errorf("%s claims to carry a zone", name)
		}
	}
}

// A list says what it is a list of, however deep.
func TestAListSaysWhatItHolds(t *testing.T) {
	got := dataType("VARCHAR[]")
	if got.Class != model.TypeArray || got.Element == nil || got.Element.Class != model.TypeString {
		t.Fatalf("a list of text reads as %v %+v", got.Class, got.Element)
	}
	deep := dataType("INTEGER[][]")
	if deep.Class != model.TypeArray || deep.Element == nil || deep.Element.Class != model.TypeArray {
		t.Errorf("a list of lists reads as %v %+v", deep.Class, deep.Element)
	}
}

// An exact number is written with the places its column has: the library
// hands one over as an integer and a scale, so the point has to be put
// back (FR-3.8).
func TestAnExactNumberKeepsItsPlaces(t *testing.T) {
	for _, c := range []struct {
		digits string
		scale  int32
		want   string
	}{
		{"150", 2, "1.50"},
		{"15", 1, "1.5"},
		{"123456789012345678901234567890", 10, "12345678901234567890.1234567890"},
		{"5", 3, "0.005"},
		{"0", 2, "0.00"},
		{"-150", 2, "-1.50"},
		{"-5", 3, "-0.005"},
		{"42", 0, "42"},
	} {
		n, _ := new(big.Int).SetString(c.digits, 10)
		got := exact(duck.Decimal{Value: n, Scale: uint8(c.scale)}, c.scale)
		if got != c.want {
			t.Errorf("%s at scale %d reads %q, want %q", c.digits, c.scale, got, c.want)
		}
	}
}

// Every value the grid holds is one of a closed set (model.Row).
func TestAValueIsNarrowedToWhatARowMayHold(t *testing.T) {
	for _, c := range []struct {
		in   any
		want any
	}{
		{nil, nil},
		{true, true},
		{int8(1), int64(1)},
		{int16(1), int64(1)},
		{int32(1), int64(1)},
		{int(1), int64(1)},
		{int64(1), int64(1)},
		{uint8(1), int64(1)},
		{uint16(1), int64(1)},
		{uint32(1), int64(1)},
		{float32(1.5), float64(1.5)},
		{"x", "x"},
	} {
		if got := normalize(c.in); got != c.want {
			t.Errorf("normalize(%#v) = %#v, want %#v", c.in, got, c.want)
		}
	}
	// A number bigger than an int64 keeps every digit.
	if got := normalize(uint64(18446744073709551615)); got != model.Decimal("18446744073709551615") {
		t.Errorf("a large unsigned number reads %#v", got)
	}
	big128, _ := new(big.Int).SetString("170141183460469231731687303715884105727", 10)
	if got := normalize(big128); got != model.Decimal("170141183460469231731687303715884105727") {
		t.Errorf("a huge integer reads %#v", got)
	}
	if got := normalize(time.Unix(0, 0).UTC()); got != time.Unix(0, 0).UTC() {
		t.Errorf("a time was changed on the way through: %#v", got)
	}
	// A list and a struct are narrowed all the way down, not only at
	// their surface.
	list, ok := normalize([]any{int32(1), int16(2)}).([]any)
	if !ok || len(list) != 2 || list[0] != int64(1) || list[1] != int64(2) {
		t.Errorf("a list reads as %#v", list)
	}
	m, ok := normalize(map[string]any{"a": int32(1)}).(map[string]any)
	if !ok || m["a"] != int64(1) {
		t.Errorf("a struct reads as %#v", m)
	}
}

// What a column was declared to hold decides how its value is read.
func TestAValueIsReadByWhatItsColumnHolds(t *testing.T) {
	// A document read as text is a document.
	got := narrow(`{"z":1.0,"a":2}`, model.DataType{Class: model.TypeJSON})
	if doc, ok := got.(model.JSON); !ok || string(doc) != `{"z":1.0,"a":2}` {
		t.Errorf("a document reads as %#v", got)
	}
	// One read any other way is the map the library made of it, and is
	// shown as one rather than as something it is not.
	got = narrow(map[string]any{"a": int32(2)}, model.DataType{Class: model.TypeJSON})
	if _, ok := got.(map[string]any); !ok {
		t.Errorf("a decoded document reads as %#v", got)
	}
	// A UUID is written the way everything else writes one.
	uuid := []byte{0x6f, 0x96, 0x19, 0xff, 0x8b, 0x86, 0xd0, 0x11,
		0xb4, 0x2d, 0x00, 0xc0, 0x4f, 0xc9, 0x64, 0xff}
	if got := narrow(uuid, model.DataType{Class: model.TypeUUID}); got != "6f9619ff-8b86-d011-b42d-00c04fc964ff" {
		t.Errorf("a UUID reads as %#v", got)
	}
	// Sixteen bytes that are not a UUID column stay bytes.
	if got := narrow(uuid, model.DataType{Class: model.TypeBytes}); got == nil {
		t.Error("a blob read as nothing")
	}
	// An exact number takes its column's scale, not the wire's.
	n, _ := new(big.Int).SetString("150", 10)
	got = narrow(duck.Decimal{Value: n, Scale: 2}, model.DataType{Class: model.TypeDecimal, Scale: 2})
	if got != model.Decimal("1.50") {
		t.Errorf("an exact number reads as %#v", got)
	}
	// Its column's places, not the wire's: a column of three places
	// holding 0.150 is not the same number as 1.50.
	got = narrow(duck.Decimal{Value: n, Scale: 2}, model.DataType{Class: model.TypeDecimal, Scale: 3})
	if got != model.Decimal("0.150") {
		t.Errorf("an exact number took the wire's places: %#v", got)
	}
}

// An interval is written the way the engine writes one.
func TestAnIntervalIsWrittenAsTheEngineWritesIt(t *testing.T) {
	for _, c := range []struct {
		in   duck.Interval
		want string
	}{
		{duck.Interval{Months: 1, Days: 2, Micros: 3723000000}, "1 mons 2 days 01:02:03"},
		{duck.Interval{Micros: 1500000}, "0 mons 0 days 00:00:01.500000"},
		{duck.Interval{Days: 1}, "0 mons 1 days 00:00:00"},
	} {
		if got := normalize(c.in); got != c.want {
			t.Errorf("%+v reads as %q, want %q", c.in, got, c.want)
		}
	}
}
