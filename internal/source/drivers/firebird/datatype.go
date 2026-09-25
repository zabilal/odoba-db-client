package firebird

import (
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Firebird's types, as the catalogue records them and as the wire reports
// them. The two are different alphabets for the same thing, and neither is
// enough on its own.
//
// The catalogue is exact: RDB$FIELD_TYPE is the storage type, and the sub-type
// and scale beside it say which of the types sharing that storage this is —
// an INT64 with a scale is a NUMERIC and without one a BIGINT, a BLOB with
// sub-type 1 is text and with 0 is bytes. So a column of a table is read from
// there, and this is the mapping.
//
// The wire is all there is for anything the catalogue does not hold: the
// columns of a query nobody declared. It names the storage type and nothing
// more, so what it can say is less, and it says that rather than guessing.

// The RDB$FIELD_TYPE values, named. Firebird's own blr_* constants.
const (
	typeShort       = 7   // SMALLINT
	typeLong        = 8   // INTEGER
	typeQuad        = 9   // an internal 64-bit pair, not a column type
	typeFloat       = 10  // FLOAT
	typeDate        = 12  // DATE
	typeTime        = 13  // TIME
	typeChar        = 14  // CHAR
	typeInt64       = 16  // BIGINT, or NUMERIC/DECIMAL with a scale
	typeBoolean     = 23  // BOOLEAN
	typeDecFloat16  = 24  // DECFLOAT(16)
	typeDecFloat34  = 25  // DECFLOAT(34)
	typeInt128      = 26  // INT128, or NUMERIC with more than 18 digits
	typeDouble      = 27  // DOUBLE PRECISION
	typeTimeTZ      = 28  // TIME WITH TIME ZONE
	typeTimestampTZ = 29  // TIMESTAMP WITH TIME ZONE
	typeTimestamp   = 35  // TIMESTAMP
	typeVarchar     = 37  // VARCHAR
	typeBlob        = 261 // BLOB, text or binary by its sub-type
)

// dataType is what a column holds, from the catalogue's own description of it.
func dataType(typ, sub, length, precision, scale int) model.DataType {
	dt := model.DataType{Length: -1, Nullable: true}
	switch typ {
	case typeShort, typeLong, typeInt64, typeInt128:
		if scale != 0 {
			// A scale is what makes it exact: NUMERIC and DECIMAL are stored
			// as an integer and a number of places to shift it.
			dt.Class = model.TypeDecimal
			dt.Precision, dt.Scale = int32(precision), int32(-scale)
			dt.Native = "numeric(" + strconv.Itoa(precision) + "," + strconv.Itoa(-scale) + ")"
			if sub == 2 {
				dt.Native = "decimal(" + strconv.Itoa(precision) + "," + strconv.Itoa(-scale) + ")"
			}
			return dt
		}
		dt.Class = model.TypeInteger
		dt.Native = map[int]string{typeShort: "smallint", typeLong: "integer",
			typeInt64: "bigint", typeInt128: "int128"}[typ]
	case typeFloat:
		dt.Class, dt.Native = model.TypeFloat, "float"
	case typeDouble:
		dt.Class, dt.Native = model.TypeFloat, "double precision"
	case typeDecFloat16:
		dt.Class, dt.Native = model.TypeDecimal, "decfloat(16)"
	case typeDecFloat34:
		dt.Class, dt.Native = model.TypeDecimal, "decfloat(34)"
	case typeDate:
		dt.Class, dt.Native = model.TypeDate, "date"
	case typeTime:
		dt.Class, dt.Native = model.TypeTime, "time"
	case typeTimeTZ:
		dt.Class, dt.Native, dt.TimeZone = model.TypeTime, "time with time zone", true
	case typeTimestamp:
		dt.Class, dt.Native = model.TypeTimestamp, "timestamp"
	case typeTimestampTZ:
		dt.Class, dt.Native, dt.TimeZone = model.TypeTimestamp, "timestamp with time zone", true
	case typeBoolean:
		dt.Class, dt.Native = model.TypeBool, "boolean"
	case typeChar:
		dt.Class, dt.Native, dt.Length = model.TypeString, "char("+strconv.Itoa(length)+")", int64(length)
	case typeVarchar:
		dt.Class, dt.Native, dt.Length = model.TypeString, "varchar("+strconv.Itoa(length)+")", int64(length)
	case typeBlob:
		// Sub-type 1 is a text blob and everything else is bytes. Firebird
		// allows user-defined sub-types, which are bytes as far as anything
		// here can tell, and 0 is the declared binary one.
		if sub == 1 {
			dt.Class, dt.Native = model.TypeString, "blob sub_type text"
			return dt
		}
		dt.Class, dt.Native = model.TypeBytes, "blob"
	case typeQuad:
		// Not a column type anybody declares; it turns up in the catalogue's
		// own columns. Named rather than left unknown, so a person reading
		// the RDB$ tables sees what it is.
		dt.Class, dt.Native = model.TypeBytes, "quad"
	default:
		dt.Class, dt.Native = model.TypeUnknown, "type "+strconv.Itoa(typ)
	}
	return dt
}

// wireType is what the library says a result column is, which is the storage
// type and nothing else.
//
// Where a storage type is shared it is read as the wider of the two, because
// this is only reached for a column nothing declared: an INT64 is decimal
// rather than an integer, since the library hands exact numbers over as text
// and a column called integer whose values are strings would be wrong about
// both. A BLOB is bytes, text being the sub-type the wire does not carry.
func wireType(name string) model.DataType {
	dt := model.DataType{Native: strings.ToLower(name), Length: -1, Nullable: true}
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "SHORT", "LONG":
		dt.Class = model.TypeInteger
	case "INT64", "INT128", "DECFLOAT16", "DECFLOAT34":
		dt.Class = model.TypeDecimal
	case "FLOAT", "DOUBLE":
		dt.Class = model.TypeFloat
	case "DATE":
		dt.Class = model.TypeDate
	case "TIME", "TIME WITH TIMEZONE":
		dt.Class = model.TypeTime
	case "TIMESTAMP", "TIMESTAMP WITH TIMEZONE":
		dt.Class = model.TypeTimestamp
	case "BOOLEAN":
		dt.Class = model.TypeBool
	case "TEXT", "VARYING":
		dt.Class = model.TypeString
	case "BLOB":
		dt.Class = model.TypeBytes
	default:
		dt.Class, dt.Native = model.TypeUnknown, "any"
	}
	return dt
}
