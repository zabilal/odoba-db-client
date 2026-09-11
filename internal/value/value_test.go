package value

import (
	"reflect"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func typed(class model.TypeClass) model.ColumnDef {
	return model.ColumnDef{Name: "c", Type: model.DataType{Class: class}}
}

func nullable(col model.ColumnDef) model.ColumnDef {
	col.Type.Nullable = true
	return col
}

func TestAValueOpenedInAnEditorReadsBackAsItWas(t *testing.T) {
	plus2 := time.FixedZone("", 7200)
	zoned := typed(model.TypeTimestamp)
	zoned.Type.TimeZone = true
	enum := typed(model.TypeEnum)
	enum.Type.EnumValues = []string{"red", "green"}
	for _, c := range []struct {
		col model.ColumnDef
		v   any
	}{
		{typed(model.TypeInteger), int64(-42)},
		{typed(model.TypeFloat), 1.5},
		{typed(model.TypeDecimal), model.Decimal("12.50")},
		{typed(model.TypeBool), true},
		{typed(model.TypeString), " as typed "},
		{typed(model.TypeString), ""},
		{typed(model.TypeDate), time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC)},
		{typed(model.TypeTime), time.Date(0, 1, 1, 7, 8, 9, 5000, time.UTC)},
		{typed(model.TypeTimestamp), time.Date(2024, 5, 6, 7, 8, 9, 123456000, time.UTC)},
		{zoned, time.Date(2024, 5, 6, 7, 8, 9, 1000, time.UTC)},
		{typed(model.TypeJSON), model.JSON(`{"a":[1,2]}`)},
		{typed(model.TypeUUID), "0f8fad5b-d9cb-469f-a165-70867728950e"},
		{enum, "green"},
		{nullable(typed(model.TypeInteger)), nil},
	} {
		text := EditText(c.v, c.col, plus2)
		got, err := Parse(text, c.col, plus2)
		if err != nil {
			t.Errorf("%v as %q: %v", c.v, text, err)
			continue
		}
		if w, ok := c.v.(time.Time); ok {
			if g, ok := got.(time.Time); !ok || !g.Equal(w) {
				t.Errorf("%v read back as %v, from %q", w, got, text)
			}
			continue
		}
		if !reflect.DeepEqual(got, c.v) {
			t.Errorf("%#v read back as %#v, from %q", c.v, got, text)
		}
	}
}

func TestTextThatIsNotTheColumnsTypeIsRefused(t *testing.T) {
	enum := typed(model.TypeEnum)
	enum.Type.EnumValues = []string{"red", "green"}
	for _, c := range []struct {
		col  model.ColumnDef
		text string
		want string
	}{
		{typed(model.TypeInteger), "lots", "not a whole number"},
		{typed(model.TypeInteger), "99999999999999999999", "too large for a whole number"},
		{typed(model.TypeInteger), " ", "needs a value"},
		{typed(model.TypeFloat), "1,5", "not a number"},
		{typed(model.TypeFloat), "1e999", "too large a number"},
		{typed(model.TypeDecimal), "1.2.3", "not a number"},
		{typed(model.TypeBool), "maybe", "not true or false"},
		{typed(model.TypeDate), "2024-13-01", "not a date, as 2006-01-02"},
		{typed(model.TypeTime), "25:00", "not a time, as 15:04:05"},
		{typed(model.TypeTimestamp), "yesterday", "not a date and time, as 2006-01-02 15:04:05"},
		{typed(model.TypeUUID), "0f8fad5b", "not a UUID"},
		{typed(model.TypeJSON), "{", "not valid JSON"},
		{enum, "gold", "not one of red, green"},
	} {
		if v, err := Parse(c.text, c.col, time.UTC); err == nil || err.Error() != c.want {
			t.Errorf("%q as %v: %v, %v; want %q", c.text, c.col.Type.Class, v, err, c.want)
		}
	}
}

func TestTypedTextIsReadAsTheColumnsType(t *testing.T) {
	plus2 := time.FixedZone("", 7200)
	zoned := typed(model.TypeTimestamp)
	zoned.Type.TimeZone = true
	instant := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	for _, c := range []struct {
		col  model.ColumnDef
		text string
		want any
	}{
		{typed(model.TypeInteger), " 7 ", int64(7)},
		{typed(model.TypeBool), "No", false},
		{typed(model.TypeBool), "t", true},
		{nullable(typed(model.TypeDate)), "", nil},
		{typed(model.TypeString), "  ", "  "},
		{typed(model.TypeUnknown), " x ", " x "},
		{typed(model.TypeXML), "", ""},
		{typed(model.TypeTime), "07:08", time.Date(0, 1, 1, 7, 8, 0, 0, time.UTC)},
		{zoned, "2024-05-06 09:08:09", instant},  // local time, as shown
		{zoned, "2024-05-06T07:08:09Z", instant}, // an offset given
		{zoned, "2024-05-06 10:08:09+03:00", instant},
		{typed(model.TypeTimestamp), "2024-05-06 07:08:09", instant}, // no zone: as written
		{typed(model.TypeTimestamp), "2024-05-06", time.Date(2024, 5, 6, 0, 0, 0, 0, time.UTC)},
	} {
		got, err := Parse(c.text, c.col, plus2)
		if err != nil {
			t.Errorf("%q: %v", c.text, err)
			continue
		}
		if w, ok := c.want.(time.Time); ok {
			if g, ok := got.(time.Time); !ok || !g.Equal(w) {
				t.Errorf("%q read as %v, want %v", c.text, got, w)
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q read as %#v, want %#v", c.text, got, c.want)
		}
	}
}

func TestOnlyValuesThatCanBeTypedAreEditedInPlace(t *testing.T) {
	for class, want := range map[model.TypeClass]bool{
		model.TypeBytes: false, model.TypeArray: false, model.TypeStruct: false, model.TypeGeometry: false,
		model.TypeString: true, model.TypeJSON: true, model.TypeTimestamp: true, model.TypeBool: true,
	} {
		if Editable(typed(class)) != want {
			t.Errorf("%v edited in place: %v", class, !want)
		}
	}
}
