package clickhouse

import (
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Reading a ClickHouse type (FR-2.3, FR-3.8).
//
// A type here is a small language rather than a word: Nullable(Decimal(30,
// 10)), Array(LowCardinality(String)), Map(String, UInt64). What a column
// holds is the innermost of those, and what somebody sees written beside it
// is the whole thing.

// dataType reads a declared type into the model's own.
func dataType(decl string) model.DataType {
	dt := parseType(decl)
	dt.Native = strings.TrimSpace(decl)
	return dt
}

// parseType reads one type, recursing through the wrappers.
func parseType(decl string) model.DataType {
	name, args := splitType(decl)
	dt := model.DataType{Native: strings.TrimSpace(decl), Length: -1}
	switch strings.ToLower(name) {
	case "nullable":
		// The wrapper says the column may hold nothing; what it holds when
		// it holds something is inside.
		inner := parseType(args)
		inner.Nullable = true
		return inner
	case "lowcardinality":
		// How the column is stored, not what it holds.
		return parseType(args)
	case "simpleaggregatefunction":
		// The function, then the type it accumulates.
		if parts := splitArgs(args); len(parts) == 2 {
			return parseType(parts[1])
		}
		dt.Class = model.TypeUnknown
	case "array":
		el := parseType(args)
		dt.Class, dt.Element = model.TypeArray, &el
	case "map", "tuple", "nested":
		dt.Class, dt.Fields = model.TypeStruct, fieldsOf(args)
	case "decimal", "decimal32", "decimal64", "decimal128", "decimal256":
		dt.Class = model.TypeDecimal
		parts := splitArgs(args)
		if len(parts) == 2 {
			dt.Precision, dt.Scale = int32(number(parts[0])), int32(number(parts[1]))
		} else if len(parts) == 1 {
			// Decimal64(S) and its kin name only the scale; the precision is
			// the width in the name.
			dt.Precision, dt.Scale = decimalWidth(name), int32(number(parts[0]))
		}
	case "int8", "int16", "int32", "int64", "uint8", "uint16", "uint32", "uint64":
		dt.Class = model.TypeInteger
	case "int128", "int256", "uint128", "uint256":
		// Past what an int64 holds, so they travel as exact text.
		dt.Class = model.TypeDecimal
	case "float32", "float64", "bfloat16":
		dt.Class = model.TypeFloat
	case "bool", "boolean":
		dt.Class = model.TypeBool
	case "string":
		dt.Class = model.TypeString
	case "fixedstring":
		dt.Class = model.TypeString
		dt.Length = int64(number(args))
	case "uuid":
		dt.Class = model.TypeUUID
	case "date", "date32":
		dt.Class = model.TypeDate
	case "datetime":
		dt.Class = model.TypeTimestamp
		dt.TimeZone = args != ""
	case "datetime64":
		dt.Class = model.TypeTimestamp
		parts := splitArgs(args)
		if len(parts) > 0 {
			dt.Scale = int32(number(parts[0]))
		}
		dt.TimeZone = len(parts) > 1
	case "time", "time64":
		dt.Class = model.TypeTime
	case "enum", "enum8", "enum16":
		dt.Class, dt.EnumValues = model.TypeEnum, enumValues(args)
	case "ipv4", "ipv6":
		dt.Class = model.TypeNetwork
	case "json", "object":
		dt.Class = model.TypeJSON
	case "point", "ring", "polygon", "multipolygon", "linestring", "multilinestring":
		dt.Class = model.TypeGeometry
	default:
		dt.Class = model.TypeUnknown
	}
	return dt
}

// decimalWidth is the precision the sized Decimal names carry.
func decimalWidth(name string) int32 {
	switch strings.ToLower(name) {
	case "decimal32":
		return 9
	case "decimal64":
		return 18
	case "decimal128":
		return 38
	case "decimal256":
		return 76
	}
	return 0
}

// splitType divides "Name(args)" into its name and its arguments. A type
// with no arguments has none.
func splitType(decl string) (name, args string) {
	decl = strings.TrimSpace(decl)
	i := strings.IndexByte(decl, '(')
	if i < 0 || !strings.HasSuffix(decl, ")") {
		return decl, ""
	}
	return strings.TrimSpace(decl[:i]), decl[i+1 : len(decl)-1]
}

// splitArgs divides an argument list at the commas that belong to it, and
// not at those inside a nested type or a string.
func splitArgs(args string) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(args); i++ {
		switch c := args[i]; {
		case quote == '\'' && c == '\\':
			i++
		case quote != 0 && c == quote:
			quote = 0
		case quote != 0:
		case c == '\'' || c == '`':
			// A label and a name may hold anything, a comma among it.
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(args[start:i]))
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(args[start:]); rest != "" || len(out) > 0 {
		out = append(out, rest)
	}
	return out
}

// fieldsOf reads the members of a Tuple, a Map or a Nested. A named member
// is "name Type"; an unnamed one is a type alone, and is numbered as
// ClickHouse numbers it.
func fieldsOf(args string) []model.FieldDef {
	parts := splitArgs(args)
	out := make([]model.FieldDef, 0, len(parts))
	for i, p := range parts {
		name, typ := memberName(p)
		if name == "" {
			name = strconv.Itoa(i + 1)
		}
		out = append(out, model.FieldDef{Name: name, Type: dataType(typ)})
	}
	return out
}

// memberName splits "name Type" where there is a name, and answers an empty
// name where the member is a type on its own.
func memberName(s string) (name, typ string) {
	s = strings.TrimSpace(s)
	if len(s) > 0 && s[0] == '`' {
		// A quoted name may hold a space, which is what the quotes are for.
		if j := strings.IndexByte(s[1:], '`'); j >= 0 {
			return s[1 : 1+j], strings.TrimSpace(s[j+2:])
		}
		return "", s
	}
	i := strings.IndexByte(s, ' ')
	if i < 0 {
		return "", s
	}
	head := s[:i]
	if strings.ContainsAny(head, "()',") {
		return "", s // the space is inside a type, as in "Decimal(30, 10)"
	}
	return head, strings.TrimSpace(s[i+1:])
}

// enumValues reads the labels of an Enum: 'a' = 1, 'b' = 2.
func enumValues(args string) []string {
	var out []string
	for _, p := range splitArgs(args) {
		label, _, _ := strings.Cut(p, "=")
		label = strings.TrimSpace(label)
		if len(label) >= 2 && label[0] == '\'' && label[len(label)-1] == '\'' {
			label = strings.ReplaceAll(label[1:len(label)-1], `\'`, `'`)
		}
		out = append(out, label)
	}
	return out
}

// number is the leading integer of a type argument, or zero.
func number(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}
