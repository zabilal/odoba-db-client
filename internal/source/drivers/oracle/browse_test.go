package oracle

import (
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading an Oracle type, and the values that come back in it.

// A NUMBER is a whole number where it was declared as one and fits, and an
// exact number otherwise: nothing is rounded to a float on the way.
func TestWhatANumberHolds(t *testing.T) {
	cases := []struct {
		precision, scale int64
		want             model.TypeClass
	}{
		{10, 0, model.TypeInteger},
		{18, 0, model.TypeInteger},
		{19, 0, model.TypeDecimal}, // past what an int64 holds
		{38, 0, model.TypeDecimal},
		{30, 10, model.TypeDecimal},
		{0, 0, model.TypeDecimal}, // declared with neither
		{38, 255, model.TypeDecimal},
	}
	for _, c := range cases {
		if got := numberType(c.precision, c.scale).Class; got != c.want {
			t.Errorf("NUMBER(%d,%d) is %v, want %v", c.precision, c.scale, got, c.want)
		}
	}
	// A scale of 255 is Oracle's word for "none given", not for a number
	// with two hundred and fifty-five places.
	if got := numberType(38, 255).Scale; got != 0 {
		t.Errorf("a NUMBER with no scale has %d places", got)
	}
	if got := numberType(30, 10).Scale; got != 10 {
		t.Errorf("NUMBER(30,10) has %d places", got)
	}
}

// The wire says less than the catalogue, and what it says is what a query's
// own columns are read by.
func TestWhatTheWireSays(t *testing.T) {
	cases := map[string]model.TypeClass{
		"NUMBER": model.TypeDecimal, "IBDouble": model.TypeFloat, "IBFloat": model.TypeFloat,
		"NCHAR": model.TypeString, "CHAR": model.TypeString, "LongVarChar": model.TypeString,
		"RAW": model.TypeBytes, "LongRaw": model.TypeBytes,
		"DATE": model.TypeTimestamp, "TimeStampDTY": model.TypeTimestamp,
		"TimeStampTZ_DTY": model.TypeTimestamp, "IntervalDS_DTY": model.TypeInterval,
		"ROWID": model.TypeString, "OCIBlobLocator": model.TypeJSON,
		"SomethingElse": model.TypeUnknown,
	}
	for name, want := range cases {
		if got := wireType(name).Class; got != want {
			t.Errorf("wireType(%q) is %v, want %v", name, got, want)
		}
	}
	if !wireType("TimeStampTZ_DTY").TimeZone {
		t.Error("a timestamp with a zone was said to carry none")
	}
	if wireType("TimeStampDTY").TimeZone {
		t.Error("a timestamp without a zone was said to carry one")
	}
}

// What the catalogue says a column holds, which is more than the wire says.
func TestWhatTheCatalogueSays(t *testing.T) {
	num := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
	none := sql.NullInt64{}
	cases := []struct {
		typ    string
		length int64
		p, s   sql.NullInt64
		want   model.TypeClass
		native string
	}{
		{"NUMBER", 22, num(10), num(0), model.TypeInteger, "NUMBER(10)"},
		{"NUMBER", 22, num(30), num(10), model.TypeDecimal, "NUMBER(30,10)"},
		{"NUMBER", 22, none, none, model.TypeDecimal, "NUMBER"},
		{"VARCHAR2", 20, none, none, model.TypeString, "VARCHAR2(20)"},
		{"NVARCHAR2", 20, none, none, model.TypeString, "NVARCHAR2(20)"},
		{"CHAR", 4, none, none, model.TypeString, "CHAR(4)"},
		{"CLOB", 0, none, none, model.TypeString, "CLOB"},
		{"BLOB", 0, none, none, model.TypeBytes, "BLOB"},
		{"RAW", 16, none, none, model.TypeBytes, "RAW(16)"},
		{"DATE", 7, none, none, model.TypeTimestamp, "DATE"},
		{"TIMESTAMP(6)", 11, none, num(6), model.TypeTimestamp, "TIMESTAMP(6)"},
		{"TIMESTAMP(6) WITH TIME ZONE", 13, none, num(6), model.TypeTimestamp, "TIMESTAMP(6) WITH TIME ZONE"},
		{"INTERVAL DAY(2) TO SECOND(6)", 11, num(2), num(6), model.TypeInterval, "INTERVAL DAY(2) TO SECOND(6)"},
		{"BOOLEAN", 1, none, none, model.TypeBool, "BOOLEAN"},
		{"JSON", 8200, none, none, model.TypeJSON, "JSON"},
		{"XMLTYPE", 0, none, none, model.TypeXML, "XMLTYPE"},
		{"BINARY_DOUBLE", 8, none, none, model.TypeFloat, "BINARY_DOUBLE"},
		{"SOMETHING", 0, none, none, model.TypeUnknown, "SOMETHING"},
	}
	for _, c := range cases {
		dt := declaredType(c.typ, c.length, c.p, c.s)
		if dt.Class != c.want {
			t.Errorf("%s is %v, want %v", c.typ, dt.Class, c.want)
		}
		if dt.Native != c.native {
			t.Errorf("%s is shown as %q, want %q", c.typ, dt.Native, c.native)
		}
	}
	// Only a timestamp that says it carries a zone does.
	if !declaredType("TIMESTAMP(6) WITH TIME ZONE", 13, none, num(6)).TimeZone {
		t.Error("a timestamp with a zone was said to carry none")
	}
	if declaredType("TIMESTAMP(6)", 11, none, num(6)).TimeZone {
		t.Error("a timestamp without a zone was said to carry one")
	}
	// A character column says how long it is, in characters.
	if got := declaredType("VARCHAR2", 20, none, none).Length; got != 20 {
		t.Errorf("a VARCHAR2(20) is %d long", got)
	}
	if got := declaredType("CLOB", 0, none, none).Length; got != -1 {
		t.Errorf("a CLOB is %d long, want no length at all", got)
	}
}

// A value keeps the shape the model gives it.
func TestAValueTakesTheModelsShape(t *testing.T) {
	when := time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		v    any
		dt   model.DataType
		want any
	}{
		{nil, numberType(10, 0), nil},
		{"7", numberType(10, 0), int64(7)},
		// Past what an int64 holds, so it stays exact.
		{"123456789012345678901234567890", numberType(10, 0), model.Decimal("123456789012345678901234567890")},
		{"1.5", numberType(30, 10), model.Decimal("1.5000000000")},
		{"1.5", numberType(0, 0), model.Decimal("1.5")},
		{"text", wireType("NCHAR"), "text"},
		{when, wireType("DATE"), when},
		{float64(2.5), wireType("IBDouble"), float64(2.5)},
		{`{"a":1}`, model.DataType{Class: model.TypeJSON}, model.JSON(`{"a":1}`)},
		{`not json`, model.DataType{Class: model.TypeJSON}, "not json"},
		{int32(3), numberType(10, 0), int64(3)},
		{float32(1.5), wireType("IBFloat"), float64(1.5)},
	}
	for _, c := range cases {
		if got := normalize(c.v, c.dt); !reflect.DeepEqual(got, c.want) {
			t.Errorf("normalize(%#v, %s) = %#v, want %#v", c.v, c.dt.Native, got, c.want)
		}
	}
	// Bytes stay bytes, in a copy of their own: the driver reuses its
	// buffer.
	buf := []byte{1, 2, 3}
	out, ok := normalize(buf, wireType("RAW")).([]byte)
	if !ok || len(out) != 3 || &out[0] == &buf[0] {
		t.Errorf("binary data came back as %#v sharing the driver's buffer", out)
	}
}

// An exact number keeps the places its column has.
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

// Paging ends its order with the key, and never names a column somebody
// already sorted by.
func TestPagingEndsItsOrderWithTheKey(t *testing.T) {
	got := tiebreak([]source.Sort{{Column: "Name", Descending: true}}, []string{"nAmE", "ID"})
	if len(got) != 2 || got[0].Column != "Name" || got[1].Column != "ID" {
		t.Errorf("the order is %+v", got)
	}
	if got := tiebreak(nil, []string{"ROWID"}); len(got) != 1 || got[0].Column != "ROWID" {
		t.Errorf("with no sort of its own the order is %+v", got)
	}
	if got := tiebreak(nil, nil); len(got) != 0 {
		t.Errorf("with nothing to order by the order is %+v", got)
	}
}

// A count arrives as an exact number, whatever its width.
func TestACountIsReadWhateverItArrivesAs(t *testing.T) {
	for v, want := range map[any]int64{
		int64(3): 3, model.Decimal("4"): 4, "5": 5, float64(6): 6, nil: 0, true: 0,
	} {
		if got := countOf(v); got != want {
			t.Errorf("countOf(%#v) = %d, want %d", v, got, want)
		}
	}
}

func TestACountIsAbbreviatedForABadge(t *testing.T) {
	cases := map[int64]string{
		0: "0", 999: "999", 1000: "1K", 1234: "1.2K",
		1_000_000: "1M", 12_300_000: "12.3M", 1_000_000_000: "1B", 2_500_000_000: "2.5B",
	}
	for n, want := range cases {
		if got := humanCount(n); got != want {
			t.Errorf("humanCount(%d) = %q, want %q", n, got, want)
		}
	}
}
