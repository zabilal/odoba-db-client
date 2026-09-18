package cassandra

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// CQL's types, as the model classifies them (FR-3.8).
//
// Native is kept as the cluster wrote it — a person who wrote `frozen<address>`
// expects to read `frozen<address>` — and Class is what the grid and the
// editors branch on. A type nothing here knows is a user's own, which is a
// structure of fields: that is what a user-defined type is in CQL, and the
// only thing it can be.

// cqlDataType reads a CQL type as the model's own.
func cqlDataType(cql string) model.DataType {
	native := strings.TrimSpace(cql)
	t := model.DataType{Native: native, Class: classOf(native)}
	// A collection says what it holds, which the cell viewer reads down into.
	if name, args, ok := generic(unfrozen(native)); ok && len(args) == 1 {
		switch name {
		case "list", "set", "vector":
			element := cqlDataType(args[0])
			t.Element = &element
		}
	}
	return t
}

// classOf is the class a CQL type belongs to.
func classOf(cql string) model.TypeClass {
	bare := unfrozen(cql)
	if name, _, ok := generic(bare); ok {
		switch name {
		case "list", "set", "vector":
			return model.TypeArray
		case "map", "tuple":
			// A map and a tuple are both read down into, and neither is a
			// list of one kind of thing.
			return model.TypeStruct
		}
		// A generic type nobody here knows is still something with parts.
		return model.TypeStruct
	}
	switch strings.ToLower(bare) {
	case "ascii", "text", "varchar":
		return model.TypeString
	case "tinyint", "smallint", "int", "bigint", "varint", "counter":
		return model.TypeInteger
	case "float", "double":
		return model.TypeFloat
	case "decimal":
		return model.TypeDecimal
	case "boolean":
		return model.TypeBool
	case "blob":
		return model.TypeBytes
	case "date":
		return model.TypeDate
	case "time":
		return model.TypeTime
	case "timestamp":
		return model.TypeTimestamp
	case "duration":
		return model.TypeInterval
	case "uuid", "timeuuid":
		return model.TypeUUID
	case "inet":
		return model.TypeNetwork
	}
	// Whatever is left is a keyspace's own type, which is its fields.
	return model.TypeStruct
}

// unfrozen is a type without the frozen<> around it. Frozen says how
// Cassandra stores a value — whole, rather than part by part — and not what
// the value is.
func unfrozen(cql string) string {
	t := strings.TrimSpace(cql)
	for {
		name, args, ok := generic(t)
		if !ok || strings.ToLower(name) != "frozen" || len(args) != 1 {
			return t
		}
		t = strings.TrimSpace(args[0])
	}
}

// generic splits a type written with arguments — list<text>, map<text, int> —
// into its name and those arguments. Arguments of its own arguments stay
// where they are: map<text, frozen<list<int>>> has two.
func generic(cql string) (string, []string, bool) {
	t := strings.TrimSpace(cql)
	open := strings.Index(t, "<")
	if open < 0 || !strings.HasSuffix(t, ">") {
		return "", nil, false
	}
	name := strings.ToLower(strings.TrimSpace(t[:open]))
	inner := t[open+1 : len(t)-1]
	var args []string
	depth, start := 0, 0
	for i, r := range inner {
		switch r {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				args = append(args, strings.TrimSpace(inner[start:i]))
				start = i + 1
			}
		}
	}
	args = append(args, strings.TrimSpace(inner[start:]))
	return name, args, name != ""
}
