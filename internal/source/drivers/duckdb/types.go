//go:build duckdb

package duckdb

import (
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// A type here is written the way somebody would declare it, and read the
// same way: DECIMAL(30,10), VARCHAR[], STRUCT(a INTEGER, b VARCHAR). The
// catalogue and the wire agree on the spelling, so one reader does both
// (ADR-0146).

var simpleTypes = map[string]model.TypeClass{
	"BOOLEAN":   model.TypeBool,
	"TINYINT":   model.TypeInteger,
	"SMALLINT":  model.TypeInteger,
	"INTEGER":   model.TypeInteger,
	"BIGINT":    model.TypeInteger,
	"UTINYINT":  model.TypeInteger,
	"USMALLINT": model.TypeInteger,
	"UINTEGER":  model.TypeInteger,
	"UBIGINT":   model.TypeInteger,
	// A HUGEINT holds more than an int64 does, so it is kept as an exact
	// number rather than as an integer this application could not carry.
	"HUGEINT":                  model.TypeDecimal,
	"UHUGEINT":                 model.TypeDecimal,
	"FLOAT":                    model.TypeFloat,
	"DOUBLE":                   model.TypeFloat,
	"VARCHAR":                  model.TypeString,
	"BLOB":                     model.TypeBytes,
	"DATE":                     model.TypeDate,
	"TIME":                     model.TypeTime,
	"TIME WITH TIME ZONE":      model.TypeTime,
	"TIMESTAMP":                model.TypeTimestamp,
	"TIMESTAMP_S":              model.TypeTimestamp,
	"TIMESTAMP_MS":             model.TypeTimestamp,
	"TIMESTAMP_NS":             model.TypeTimestamp,
	"TIMESTAMP WITH TIME ZONE": model.TypeTimestamp,
	"INTERVAL":                 model.TypeInterval,
	"UUID":                     model.TypeUUID,
	"JSON":                     model.TypeJSON,
	"BIT":                      model.TypeBit,
}

// zoned are the types that carry a zone with them.
var zoned = map[string]bool{
	"TIMESTAMP WITH TIME ZONE": true,
	"TIME WITH TIME ZONE":      true,
	"TIMESTAMPTZ":              true,
	"TIMETZ":                   true,
}

// dataType reads a declared type into the canonical one, keeping what was
// declared as the name beside it.
func dataType(declared string) model.DataType {
	declared = strings.TrimSpace(declared)
	dt := model.DataType{Native: declared, Nullable: true, Length: -1}
	if declared == "" {
		return dt
	}

	// A list is written as what it holds with [] after it, however deep.
	if inner, ok := strings.CutSuffix(declared, "[]"); ok {
		e := dataType(inner)
		dt.Class, dt.Element = model.TypeArray, &e
		return dt
	}

	head, args, hasArgs := strings.Cut(declared, "(")
	head = strings.ToUpper(strings.TrimSpace(head))
	args = strings.TrimSuffix(args, ")")

	// The two spellings of a zoned time: the catalogue writes one and the
	// wire the other.
	switch head {
	case "TIMESTAMPTZ":
		head = "TIMESTAMP WITH TIME ZONE"
	case "TIMETZ":
		head = "TIME WITH TIME ZONE"
	}
	dt.TimeZone = zoned[head] || zoned[strings.ToUpper(declared)]

	switch head {
	case "STRUCT", "MAP", "UNION":
		dt.Class = model.TypeStruct
		return dt
	case "ENUM":
		dt.Class = model.TypeEnum
		return dt
	case "DECIMAL", "NUMERIC":
		dt.Class = model.TypeDecimal
		if hasArgs {
			dt.Precision, dt.Scale = widthOf(args)
		}
		return dt
	case "VARCHAR", "CHAR", "BPCHAR", "STRING", "TEXT":
		dt.Class = model.TypeString
		if hasArgs {
			if n, err := strconv.ParseInt(strings.TrimSpace(args), 10, 64); err == nil {
				dt.Length = n
			}
		}
		return dt
	}
	if class, ok := simpleTypes[head]; ok {
		dt.Class = class
		return dt
	}
	// A type this driver has not been taught keeps the name it was
	// declared with, which says more than "unknown" does.
	return dt
}

// widthOf reads the precision and scale out of a declared width.
func widthOf(args string) (precision, scale int32) {
	p, s, _ := strings.Cut(args, ",")
	if n, err := strconv.ParseInt(strings.TrimSpace(p), 10, 32); err == nil {
		precision = int32(n)
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32); err == nil {
		scale = int32(n)
	}
	return precision, scale
}
