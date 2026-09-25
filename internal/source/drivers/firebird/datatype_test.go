package firebird

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// What a value is. Firebird stores several of its types the same way and
// tells them apart by a sub-type or a scale, so these are the cases where
// reading only the storage type would be wrong.

func TestWhatTheCatalogueSaysAColumnHolds(t *testing.T) {
	for name, c := range map[string]struct {
		typ, sub, length, precision, scale int
		class                              model.TypeClass
		native                             string
	}{
		"a smallint": {typeShort, 0, 2, 0, 0, model.TypeInteger, "smallint"},
		"an integer": {typeLong, 0, 4, 0, 0, model.TypeInteger, "integer"},
		"a bigint":   {typeInt64, 0, 8, 0, 0, model.TypeInteger, "bigint"},
		"an int128":  {typeInt128, 0, 16, 0, 0, model.TypeInteger, "int128"},
		// The same storage as a bigint, and not a bigint: the scale is what
		// makes it exact, and a number read as a float loses its last place.
		"a numeric":               {typeInt64, 1, 8, 12, -2, model.TypeDecimal, "numeric(12,2)"},
		"a decimal":               {typeInt64, 2, 8, 18, -4, model.TypeDecimal, "decimal(18,4)"},
		"a small numeric":         {typeShort, 1, 2, 4, -1, model.TypeDecimal, "numeric(4,1)"},
		"a float":                 {typeFloat, 0, 4, 0, 0, model.TypeFloat, "float"},
		"a double":                {typeDouble, 0, 8, 0, 0, model.TypeFloat, "double precision"},
		"a decfloat":              {typeDecFloat16, 0, 8, 0, 0, model.TypeDecimal, "decfloat(16)"},
		"a wider decfloat":        {typeDecFloat34, 0, 16, 0, 0, model.TypeDecimal, "decfloat(34)"},
		"a date":                  {typeDate, 0, 4, 0, 0, model.TypeDate, "date"},
		"a time":                  {typeTime, 0, 4, 0, 0, model.TypeTime, "time"},
		"a time with a zone":      {typeTimeTZ, 0, 8, 0, 0, model.TypeTime, "time with time zone"},
		"a timestamp":             {typeTimestamp, 0, 8, 0, 0, model.TypeTimestamp, "timestamp"},
		"a timestamp with a zone": {typeTimestampTZ, 0, 12, 0, 0, model.TypeTimestamp, "timestamp with time zone"},
		"a boolean":               {typeBoolean, 0, 1, 0, 0, model.TypeBool, "boolean"},
		"a char":                  {typeChar, 0, 10, 0, 0, model.TypeString, "char(10)"},
		"a varchar":               {typeVarchar, 0, 50, 0, 0, model.TypeString, "varchar(50)"},
		// The same storage, told apart by the sub-type: one is text and one
		// is bytes, and showing bytes as text is a wall of replacement
		// characters.
		"a text blob":                       {typeBlob, 1, 8, 0, 0, model.TypeString, "blob sub_type text"},
		"a binary blob":                     {typeBlob, 0, 8, 0, 0, model.TypeBytes, "blob"},
		"a blob of somebody's own sub-type": {typeBlob, 7, 8, 0, 0, model.TypeBytes, "blob"},
		"the catalogue's own pairs":         {typeQuad, 0, 8, 0, 0, model.TypeBytes, "quad"},
		"something from a later Firebird":   {250, 0, 0, 0, 0, model.TypeUnknown, "type 250"},
	} {
		t.Run(name, func(t *testing.T) {
			got := dataType(c.typ, c.sub, c.length, c.precision, c.scale)
			if got.Class != c.class {
				t.Errorf("it reads as %v, want %v", got.Class, c.class)
			}
			if got.Native != c.native {
				t.Errorf("it calls itself %q, want %q", got.Native, c.native)
			}
			if !got.Nullable {
				t.Error("it is not nullable, and the catalogue said nothing about that yet")
			}
		})
	}
}

// A time with a zone says so, so that a value shown without one is not
// mistaken for a value that has none.
func TestATimeWithAZoneSaysSo(t *testing.T) {
	for _, typ := range []int{typeTimeTZ, typeTimestampTZ} {
		if !dataType(typ, 0, 8, 0, 0).TimeZone {
			t.Errorf("type %d does not report a zone", typ)
		}
	}
	for _, typ := range []int{typeTime, typeTimestamp, typeDate} {
		if dataType(typ, 0, 8, 0, 0).TimeZone {
			t.Errorf("type %d reports a zone it has not got", typ)
		}
	}
}

// A decimal keeps its precision and scale, which is what tells a grid how
// many places to show.
func TestADecimalKeepsItsPlaces(t *testing.T) {
	got := dataType(typeInt64, 1, 8, 12, -2)
	if got.Precision != 12 || got.Scale != 2 {
		t.Errorf("it is %d places of %d digits", got.Scale, got.Precision)
	}
}

// What the wire says, which is all there is for a column nothing declared.
func TestWhatTheWireSaysAResultColumnHolds(t *testing.T) {
	for name, c := range map[string]struct {
		wire  string
		class model.TypeClass
	}{
		"a smallint": {"SHORT", model.TypeInteger},
		"an integer": {"LONG", model.TypeInteger},
		// Wider than the truth on purpose: an INT64 on the wire may be a
		// bigint or an exact decimal, the library hands exact numbers over as
		// text, and a column called integer holding strings would be wrong
		// about both.
		"a 64-bit number":  {"INT64", model.TypeDecimal},
		"a 128-bit number": {"INT128", model.TypeDecimal},
		"a decfloat":       {"DECFLOAT34", model.TypeDecimal},
		"a float":          {"FLOAT", model.TypeFloat},
		"a double":         {"DOUBLE", model.TypeFloat},
		"a date":           {"DATE", model.TypeDate},
		"a time":           {"TIME", model.TypeTime},
		"a timestamp":      {"TIMESTAMP", model.TypeTimestamp},
		"a boolean":        {"BOOLEAN", model.TypeBool},
		"a varchar":        {"VARYING", model.TypeString},
		"a char":           {"TEXT", model.TypeString},
		// Bytes, text being the sub-type the wire does not carry.
		"a blob":         {"BLOB", model.TypeBytes},
		"lower case":     {"varying", model.TypeString},
		"nothing at all": {"", model.TypeUnknown},
		"something new":  {"WIDGET", model.TypeUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			if got := wireType(c.wire).Class; got != c.class {
				t.Errorf("%q reads as %v, want %v", c.wire, got, c.class)
			}
		})
	}
	if got := wireType("WIDGET").Native; got != "any" {
		t.Errorf("a type nothing knows calls itself %q", got)
	}
}

// The object's answer wins over the wire's where there is one, and the
// wire's stands where there is not.
func TestTheObjectsAnswerWinsAndTheWiresStands(t *testing.T) {
	cols := []model.ColumnDef{
		{Name: "AMOUNT", Type: wireType("INT64")},
		{Name: "TOTAL", Type: wireType("INT64")}, // an expression: nothing declares it
	}
	applyDeclared(cols, []model.Column{
		{Name: "AMOUNT", Type: dataType(typeInt64, 1, 8, 12, -2)},
	})
	if cols[0].Type.Native != "numeric(12,2)" {
		t.Errorf("the declared column reads as %q", cols[0].Type.Native)
	}
	if cols[1].Type.Native != "int64" {
		t.Errorf("the expression lost what the wire said: %q", cols[1].Type.Native)
	}
}
