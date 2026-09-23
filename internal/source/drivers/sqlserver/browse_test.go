package sqlserver

import (
	"reflect"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Reading a SQL Server type, and the values that come back in it.

func TestATypeIsClassified(t *testing.T) {
	cases := map[string]model.TypeClass{
		"bit": model.TypeBool, "tinyint": model.TypeInteger, "smallint": model.TypeInteger,
		"int": model.TypeInteger, "bigint": model.TypeInteger,
		"decimal": model.TypeDecimal, "numeric": model.TypeDecimal,
		"money": model.TypeDecimal, "smallmoney": model.TypeDecimal,
		"float": model.TypeFloat, "real": model.TypeFloat,
		"char": model.TypeString, "varchar": model.TypeString, "text": model.TypeString,
		"nchar": model.TypeString, "nvarchar": model.TypeString, "ntext": model.TypeString,
		"sysname": model.TypeString,
		"binary":  model.TypeBytes, "varbinary": model.TypeBytes, "image": model.TypeBytes,
		"timestamp": model.TypeBytes, "rowversion": model.TypeBytes,
		"date": model.TypeDate, "time": model.TypeTime,
		"datetime": model.TypeTimestamp, "datetime2": model.TypeTimestamp,
		"smalldatetime": model.TypeTimestamp, "datetimeoffset": model.TypeTimestamp,
		"uniqueidentifier": model.TypeUUID, "xml": model.TypeXML,
		"geography": model.TypeGeometry, "geometry": model.TypeGeometry,
		"hierarchyid": model.TypeUnknown, "sql_variant": model.TypeUnknown,
	}
	for name, want := range cases {
		if got := dataType(name).Class; got != want {
			t.Errorf("dataType(%q) is %v, want %v", name, got, want)
		}
	}
	// However the server spells it, and whatever it was declared with.
	if got := dataType("NVARCHAR").Class; got != model.TypeString {
		t.Errorf("an upper-case name is %v", got)
	}
	if got := dataType("decimal(30,10)"); got.Class != model.TypeDecimal || got.Native != "decimal" {
		t.Errorf("a declared type is %+v", got)
	}
}

// Only a datetimeoffset carries a zone; the rest are wall clocks.
func TestOnlyAnOffsetCarriesAZone(t *testing.T) {
	if !dataType("datetimeoffset").TimeZone {
		t.Error("a datetimeoffset carries no zone")
	}
	for _, n := range []string{"datetime", "datetime2", "smalldatetime", "date", "time"} {
		if dataType(n).TimeZone {
			t.Errorf("%s was said to carry a zone", n)
		}
	}
}

// A type is written the way somebody would declare it, out of the parts the
// catalogue keeps apart.
func TestATypeIsWrittenAsItWasDeclared(t *testing.T) {
	cases := []struct {
		name                     string
		maxLen, precision, scale int64
		want                     string
	}{
		{"varchar", 50, 0, 0, "varchar(50)"},
		{"varchar", -1, 0, 0, "varchar(max)"},
		{"nvarchar", 100, 0, 0, "nvarchar(50)"},
		{"nvarchar", -1, 0, 0, "nvarchar(max)"},
		{"nchar", 20, 0, 0, "nchar(10)"},
		{"char", 10, 0, 0, "char(10)"},
		{"binary", 16, 0, 0, "binary(16)"},
		{"varbinary", -1, 0, 0, "varbinary(max)"},
		{"decimal", 9, 30, 10, "decimal(30,10)"},
		{"numeric", 9, 18, 0, "numeric(18,0)"},
		{"datetime2", 8, 27, 7, "datetime2(7)"},
		{"time", 5, 16, 3, "time(3)"},
		{"datetimeoffset", 10, 34, 7, "datetimeoffset(7)"},
		{"int", 4, 10, 0, "int"},
		{"INT", 4, 10, 0, "int"},
		{"uniqueidentifier", 16, 0, 0, "uniqueidentifier"},
		{"text", 16, 0, 0, "text"},
	}
	for _, c := range cases {
		if got := nativeType(c.name, c.maxLen, c.precision, c.scale); got != c.want {
			t.Errorf("nativeType(%q, %d, %d, %d) = %q, want %q",
				c.name, c.maxLen, c.precision, c.scale, got, c.want)
		}
	}
}

// A length is in characters for text and bytes for binary, and there is none
// where a type has none.
func TestADeclaredLength(t *testing.T) {
	cases := []struct {
		name   string
		maxLen int64
		want   int64
	}{
		{"varchar", 50, 50},
		{"nvarchar", 100, 50},
		{"nchar", 20, 10},
		{"binary", 16, 16},
		{"varbinary", -1, -1},
		{"nvarchar", -1, -1},
		{"int", 4, -1},
		{"text", 16, -1},
	}
	for _, c := range cases {
		if got := declaredLength(c.name, c.maxLen); got != c.want {
			t.Errorf("declaredLength(%q, %d) = %d, want %d", c.name, c.maxLen, got, c.want)
		}
	}
}

// A value keeps the shape the model gives it, and the driver's buffer is
// never handed on.
func TestAValueTakesTheModelsShape(t *testing.T) {
	uuid := []byte{0xff, 0x19, 0x96, 0x6f, 0x86, 0x8b, 0x11, 0xd0, 0xb4, 0x2d, 0x00, 0xc0, 0x4f, 0xc9, 0x64, 0xff}
	cases := []struct {
		v    any
		dt   model.DataType
		want any
	}{
		{[]byte("1.50"), dataType("decimal"), model.Decimal("1.50")},
		{"1.50", dataType("money"), model.Decimal("1.50")},
		{uuid, dataType("uniqueidentifier"), "6F9619FF-8B86-D011-B42D-00C04FC964FF"},
		{[]byte(`{"a":1}`), dataType("json"), model.JSON(`{"a":1}`)},
		{[]byte(`not json`), dataType("json"), "not json"},
		{[]byte("hello"), dataType("nvarchar"), "hello"},
		{[]byte("<a/>"), dataType("xml"), "<a/>"},
		{int32(7), dataType("int"), int64(7)},
		{float32(1.5), dataType("real"), float64(1.5)},
		{int64(7), dataType("int"), int64(7)},
		{true, dataType("bit"), true},
		{nil, dataType("int"), nil},
		{"plain", dataType("nvarchar"), "plain"},
	}
	for _, c := range cases {
		if got := normalize(c.v, c.dt); !reflect.DeepEqual(got, c.want) {
			t.Errorf("normalize(%#v, %s) = %#v, want %#v", c.v, c.dt.Native, got, c.want)
		}
	}
	// Bytes stay bytes, in a copy of their own: the driver reuses its buffer.
	buf := []byte{1, 2, 3}
	got, ok := normalize(buf, dataType("varbinary")).([]byte)
	if !ok || len(got) != 3 || &got[0] == &buf[0] {
		t.Errorf("binary data came back as %#v, sharing the driver's buffer: %v", got, ok && &got[0] == &buf[0])
	}
	// Sixteen bytes that are not a uniqueidentifier are left as they are.
	if _, ok := normalize([]byte{1, 2}, dataType("uniqueidentifier")).([]byte); !ok {
		t.Error("something that is not an identifier was read as one")
	}
}

// ORDER BY may not name a large object, and a browse of a table without a
// key orders by everything it can.
func TestWhatMayBeSortedOn(t *testing.T) {
	for _, n := range []string{"text", "ntext", "image", "xml", "geography", "geometry", "hierarchyid", "sql_variant"} {
		dt := dataType(n)
		dt.Native = n
		if sortable(dt) {
			t.Errorf("%s was said to be sortable", n)
		}
	}
	for _, n := range []string{"int", "nvarchar(50)", "varbinary(16)", "date", "uniqueidentifier"} {
		dt := dataType(n)
		dt.Native, dt.Length = n, 50
		if !sortable(dt) {
			t.Errorf("%s was said not to be sortable", n)
		}
	}
	// A column with no length at all is one of the large ones under another
	// name: nvarchar(max) cannot be sorted on either.
	big := model.DataType{Class: model.TypeString, Native: "nvarchar(max)", Length: -1}
	if sortable(big) {
		t.Error("nvarchar(max) was said to be sortable")
	}
}

// Paging ends its order with a key, and never repeats a column somebody
// already sorted by.
func TestPagingEndsItsOrderWithAKey(t *testing.T) {
	// The key and the sort may name the same column in different cases, and
	// ordering by it twice would be an order that says nothing.
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
