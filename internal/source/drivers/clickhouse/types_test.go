package clickhouse

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Reading a ClickHouse type, which is a small language rather than a word.

func TestATypeIsClassified(t *testing.T) {
	cases := map[string]model.TypeClass{
		"UInt8": model.TypeInteger, "UInt64": model.TypeInteger,
		"Int8": model.TypeInteger, "Int64": model.TypeInteger,
		"Int128": model.TypeDecimal, "UInt256": model.TypeDecimal,
		"Float32": model.TypeFloat, "Float64": model.TypeFloat,
		"Decimal(30, 10)": model.TypeDecimal, "Decimal64(4)": model.TypeDecimal,
		"Bool": model.TypeBool, "String": model.TypeString, "FixedString(16)": model.TypeString,
		"UUID": model.TypeUUID, "Date": model.TypeDate, "Date32": model.TypeDate,
		"DateTime": model.TypeTimestamp, "DateTime64(3)": model.TypeTimestamp,
		"Time": model.TypeTime, "Time64(3)": model.TypeTime,
		"Enum8('a' = 1)": model.TypeEnum, "IPv4": model.TypeNetwork, "IPv6": model.TypeNetwork,
		"JSON": model.TypeJSON, "Point": model.TypeGeometry, "Polygon": model.TypeGeometry,
		"Array(String)": model.TypeArray, "Map(String, UInt8)": model.TypeStruct,
		"Tuple(a String)": model.TypeStruct, "Nested(a String)": model.TypeStruct,
		"AggregateFunction(sum, UInt64)": model.TypeUnknown,
		"Variant(String, UInt8)":         model.TypeUnknown,
	}
	for decl, want := range cases {
		if got := dataType(decl).Class; got != want {
			t.Errorf("dataType(%q) is %v, want %v", decl, got, want)
		}
	}
}

// A wrapper says how a column is stored or whether it may hold nothing;
// what it holds is inside.
func TestAWrapperIsNotWhatAColumnHolds(t *testing.T) {
	cases := []struct {
		decl     string
		want     model.TypeClass
		nullable bool
	}{
		{"Nullable(String)", model.TypeString, true},
		{"LowCardinality(String)", model.TypeString, false},
		{"LowCardinality(Nullable(String))", model.TypeString, true},
		{"Nullable(Decimal(30, 10))", model.TypeDecimal, true},
		{"SimpleAggregateFunction(sum, UInt64)", model.TypeInteger, false},
		{"String", model.TypeString, false},
	}
	for _, c := range cases {
		dt := dataType(c.decl)
		if dt.Class != c.want || dt.Nullable != c.nullable {
			t.Errorf("dataType(%q) is %v (nullable %v), want %v (nullable %v)",
				c.decl, dt.Class, dt.Nullable, c.want, c.nullable)
		}
		// However deeply it is wrapped, what is shown is what was declared.
		if dt.Native != c.decl {
			t.Errorf("dataType(%q) shows itself as %q", c.decl, dt.Native)
		}
	}
}

// What a type says about itself beyond its class: how many places a number
// has, how long a string is, what an enum's labels are, what an array holds.
func TestWhatATypeSaysAboutItself(t *testing.T) {
	if dt := dataType("Decimal(30, 10)"); dt.Precision != 30 || dt.Scale != 10 {
		t.Errorf("Decimal(30, 10) has precision %d and scale %d", dt.Precision, dt.Scale)
	}
	// The sized names carry the precision in the name and the scale alone.
	for decl, want := range map[string]int32{
		"Decimal32(4)": 9, "Decimal64(4)": 18, "Decimal128(4)": 38, "Decimal256(4)": 76,
	} {
		if dt := dataType(decl); dt.Precision != want || dt.Scale != 4 {
			t.Errorf("%s has precision %d and scale %d, want %d and 4", decl, dt.Precision, dt.Scale, want)
		}
	}
	if dt := dataType("FixedString(16)"); dt.Length != 16 {
		t.Errorf("FixedString(16) is %d long", dt.Length)
	}
	if dt := dataType("String"); dt.Length != -1 {
		t.Errorf("a String is %d long, want no length at all", dt.Length)
	}
	if dt := dataType("DateTime64(3, 'UTC')"); dt.Scale != 3 || !dt.TimeZone {
		t.Errorf("DateTime64(3, 'UTC') has scale %d and zone %v", dt.Scale, dt.TimeZone)
	}
	for decl, zoned := range map[string]bool{
		"DateTime": false, "DateTime('UTC')": true, "DateTime64(3)": false, "Date": false,
	} {
		if got := dataType(decl).TimeZone; got != zoned {
			t.Errorf("%s carries a zone: %v, want %v", decl, got, zoned)
		}
	}
	if dt := dataType("Array(Nullable(UInt8))"); dt.Element == nil ||
		dt.Element.Class != model.TypeInteger || !dt.Element.Nullable {
		t.Errorf("Array(Nullable(UInt8)) holds %+v", dt.Element)
	}
	if dt := dataType("Array(String)"); dt.Element == nil || dt.Element.Native != "String" {
		t.Errorf("Array(String) holds %+v", dt.Element)
	}
}

// An enum's labels are what somebody may choose between.
func TestAnEnumsLabels(t *testing.T) {
	cases := map[string][]string{
		"Enum8('one' = 1, 'two' = 2)": {"one", "two"},
		"Enum8('a' = 1)":              {"a"},
		"Enum16('it\\'s' = 1)":        {"it's"},
		"Enum8('a, b' = 1, 'c' = 2)":  {"a, b", "c"},
	}
	for decl, want := range cases {
		got := dataType(decl).EnumValues
		if len(got) != len(want) {
			t.Errorf("%s has labels %q, want %q", decl, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s has labels %q, want %q", decl, got, want)
				break
			}
		}
	}
}

// A tuple's members are named where the type names them, and numbered where
// it does not — as ClickHouse numbers them, from one.
func TestATuplesMembers(t *testing.T) {
	dt := dataType("Tuple(a String, b Decimal(10, 2))")
	if len(dt.Fields) != 2 {
		t.Fatalf("the members are %+v", dt.Fields)
	}
	if dt.Fields[0].Name != "a" || dt.Fields[0].Type.Class != model.TypeString {
		t.Errorf("the first member is %+v", dt.Fields[0])
	}
	if dt.Fields[1].Name != "b" || dt.Fields[1].Type.Scale != 2 {
		t.Errorf("the second member is %+v", dt.Fields[1])
	}
	dt = dataType("Tuple(String, UInt8)")
	if len(dt.Fields) != 2 || dt.Fields[0].Name != "1" || dt.Fields[1].Name != "2" {
		t.Errorf("unnamed members are %+v", dt.Fields)
	}
	// A member with a space inside its own type is unnamed, not a member
	// called "Decimal(30,".
	dt = dataType("Tuple(Decimal(30, 10), String)")
	if len(dt.Fields) != 2 || dt.Fields[0].Name != "1" || dt.Fields[0].Type.Scale != 10 {
		t.Errorf("a member that is a type with arguments is %+v", dt.Fields)
	}
	// A member's name may be quoted, and what it is called is what is
	// inside the quotes.
	dt = dataType("Tuple(`a b` String)")
	if len(dt.Fields) != 1 || dt.Fields[0].Name != "a b" || dt.Fields[0].Type.Class != model.TypeString {
		t.Errorf("a quoted member name is %+v", dt.Fields)
	}
	// A Map's key and value are two members, whatever they are made of.
	dt = dataType("Map(String, Array(UInt8))")
	if len(dt.Fields) != 2 || dt.Fields[1].Type.Class != model.TypeArray {
		t.Errorf("a map's members are %+v", dt.Fields)
	}
}

// An argument list is divided at the commas that belong to it, and not at
// those inside a nested type or a label.
func TestAnArgumentListIsDividedAtItsOwnCommas(t *testing.T) {
	cases := []struct {
		args string
		want []string
	}{
		{"30, 10", []string{"30", "10"}},
		{"String, Array(UInt8)", []string{"String", "Array(UInt8)"}},
		{"a Map(String, UInt8), b String", []string{"a Map(String, UInt8)", "b String"}},
		{"'a, b' = 1, 'c' = 2", []string{"'a, b' = 1", "'c' = 2"}},
		{`'it\'s, mine' = 1`, []string{`'it\'s, mine' = 1`}},
		{"", nil},
	}
	for _, c := range cases {
		got := splitArgs(c.args)
		if len(got) != len(c.want) {
			t.Errorf("splitArgs(%q) = %q, want %q", c.args, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitArgs(%q) = %q, want %q", c.args, got, c.want)
				break
			}
		}
	}
}

// A type with no arguments has none, and one whose brackets do not close is
// a name.
func TestSplittingATypeFromItsArguments(t *testing.T) {
	cases := []struct{ decl, name, args string }{
		{"String", "String", ""},
		{"Decimal(30, 10)", "Decimal", "30, 10"},
		{" Nullable(String) ", "Nullable", "String"},
		{"Array(String", "Array(String", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		name, args := splitType(c.decl)
		if name != c.name || args != c.args {
			t.Errorf("splitType(%q) = %q, %q, want %q, %q", c.decl, name, args, c.name, c.args)
		}
	}
}
