package trino

import (
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// What Trino's types are to a grid (FR-3.8).
//
// A type arrives written two ways: as the catalogue writes it,
// "timestamp(3) with time zone", and as a result's column names it,
// "TIMESTAMP WITH TIME ZONE" with the precision said separately. Both are read
// here, and neither is trusted to be the other.

// dataType reads a type as Trino writes one.
func dataType(native string) model.DataType {
	native = strings.TrimSpace(native)
	dt := model.DataType{Native: native, Length: -1}
	lower := strings.ToLower(native)
	base, args, rest := parts(lower)

	switch base {
	case "boolean":
		dt.Class = model.TypeBool
	case "tinyint", "smallint", "integer", "int", "bigint":
		dt.Class = model.TypeInteger
	case "real", "double":
		dt.Class = model.TypeFloat
	case "decimal":
		dt.Class = model.TypeDecimal
		if p := list(args); len(p) > 0 {
			dt.Precision = number(p[0])
			if len(p) > 1 {
				dt.Scale = number(p[1])
			}
		}
	case "varchar", "char":
		dt.Class = model.TypeString
		if n := number(args); n > 0 {
			dt.Length = int64(n)
		}
	case "varbinary":
		dt.Class = model.TypeBytes
	case "date":
		dt.Class = model.TypeDate
	case "time":
		dt.Class = model.TypeTime
		dt.Precision = number(args)
		dt.TimeZone = strings.Contains(rest, "with time zone")
	case "timestamp":
		dt.Class = model.TypeTimestamp
		dt.Precision = number(args)
		dt.TimeZone = strings.Contains(rest, "with time zone")
	case "interval":
		dt.Class = model.TypeInterval
	case "uuid":
		dt.Class = model.TypeUUID
	case "json":
		dt.Class = model.TypeJSON
	case "ipaddress":
		dt.Class = model.TypeNetwork
	case "array":
		dt.Class = model.TypeArray
		if args != "" {
			el := dataType(args)
			dt.Element = &el
		}
	case "map", "row":
		dt.Class = model.TypeStruct
	case "geometry", "sphericalgeography":
		dt.Class = model.TypeGeometry
	default:
		// A sketch, a digest, a colour, and whatever a later Trino adds:
		// shown as the server writes it, and compared with nothing.
		dt.Class = model.TypeUnknown
	}
	return dt
}

// parts splits a type into its name, what is in its brackets and what follows
// them: "timestamp(3) with time zone" is timestamp, 3 and "with time zone".
// The brackets are matched, because a type's arguments are types with
// brackets of their own.
func parts(lower string) (base, args, rest string) {
	open := strings.IndexByte(lower, '(')
	if open < 0 {
		base, rest, _ = strings.Cut(lower, " ")
		return base, "", rest
	}
	depth := 0
	for i := open; i < len(lower); i++ {
		switch lower[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return strings.TrimSpace(lower[:open]), lower[open+1 : i], strings.TrimSpace(lower[i+1:])
			}
		}
	}
	return strings.TrimSpace(lower[:open]), lower[open+1:], ""
}

// list splits a type's arguments at the commas that are not inside another
// type's brackets.
func list(args string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(args[start:i]))
				start = i + 1
			}
		}
	}
	if tail := strings.TrimSpace(args[start:]); tail != "" {
		out = append(out, tail)
	}
	return out
}

func number(s string) int32 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
	if err != nil {
		return 0
	}
	return int32(n)
}

// orderable reports whether Trino will sort by a column of this type. It
// orders nearly everything; what it refuses is what has no order to give — a
// map, a document, a shape, a sketch — and a row or an array only when what
// is inside them has one, which is not worth finding out to break a tie.
func orderable(dt model.DataType) bool {
	switch dt.Class {
	case model.TypeBool, model.TypeInteger, model.TypeFloat, model.TypeDecimal, model.TypeString,
		model.TypeBytes, model.TypeDate, model.TypeTime, model.TypeTimestamp, model.TypeInterval,
		model.TypeUUID, model.TypeNetwork:
		return true
	}
	return false
}
