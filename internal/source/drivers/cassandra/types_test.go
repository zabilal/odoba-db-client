package cassandra

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestACQLTypeIsReadAsTheModelsOwn(t *testing.T) {
	for cql, want := range map[string]model.TypeClass{
		"text": model.TypeString, "varchar": model.TypeString, "ascii": model.TypeString,
		"int": model.TypeInteger, "bigint": model.TypeInteger, "smallint": model.TypeInteger,
		"tinyint": model.TypeInteger, "varint": model.TypeInteger, "counter": model.TypeInteger,
		"float": model.TypeFloat, "double": model.TypeFloat, "decimal": model.TypeDecimal,
		"boolean": model.TypeBool, "blob": model.TypeBytes, "inet": model.TypeNetwork,
		"date": model.TypeDate, "time": model.TypeTime, "timestamp": model.TypeTimestamp,
		"duration": model.TypeInterval, "uuid": model.TypeUUID, "timeuuid": model.TypeUUID,
		"list<text>": model.TypeArray, "set<int>": model.TypeArray, "vector<float, 3>": model.TypeArray,
		"map<text, int>": model.TypeStruct, "tuple<text, int>": model.TypeStruct,
		// A type nobody here knows is a keyspace's own, which is its fields.
		"address": model.TypeStruct, "frozen<address>": model.TypeStruct,
	} {
		if got := cqlDataType(cql); got.Class != want {
			t.Errorf("%s reads as %v, want %v", cql, got.Class, want)
		}
	}
	// The cluster's own words are kept: a person who wrote frozen<address>
	// reads frozen<address>.
	for _, cql := range []string{"frozen<address>", "map<text, frozen<list<int>>>", "text"} {
		if got := cqlDataType(cql).Native; got != cql {
			t.Errorf("%s is shown as %q", cql, got)
		}
	}
	// A collection says what it holds, and what holds no one thing does not.
	if el := cqlDataType("list<text>").Element; el == nil || el.Class != model.TypeString {
		t.Errorf("a list of text holds %+v", el)
	}
	if el := cqlDataType("set<frozen<address>>").Element; el == nil || el.Class != model.TypeStruct {
		t.Errorf("a set of a type holds %+v", el)
	}
	for _, cql := range []string{"map<text, int>", "text", "tuple<int, int>"} {
		if el := cqlDataType(cql).Element; el != nil {
			t.Errorf("%s holds one kind of thing: %+v", cql, el)
		}
	}
}

func TestFrozenSaysHowAValueIsHeldRatherThanWhatItIs(t *testing.T) {
	for cql, want := range map[string]string{
		"frozen<address>":             "address",
		"frozen<frozen<address>>":     "address",
		"frozen<list<int>>":           "list<int>",
		"map<text, frozen<address>>":  "map<text, frozen<address>>",
		"text":                        "text",
		"tuple<frozen<address>, int>": "tuple<frozen<address>, int>",
	} {
		if got := unfrozen(cql); got != want {
			t.Errorf("unfrozen(%s) = %s, want %s", cql, got, want)
		}
	}
}

func TestATypeWrittenWithArgumentsIsSplitIntoThem(t *testing.T) {
	for cql, want := range map[string]string{
		"list<text>":                   "list: text",
		"map<text, int>":               "map: text|int",
		"map<text, frozen<list<int>>>": "map: text|frozen<list<int>>",
		// A comma inside an argument is that argument's, not the type's.
		"map<text, frozen<map<int, text>>>": "map: text|frozen<map<int, text>>",
		"tuple<map<int, text>, list<text>>": "tuple: map<int, text>|list<text>",
		"tuple<int, text, uuid>":            "tuple: int|text|uuid",
		"vector<float, 3>":                  "vector: float|3",
	} {
		name, args, ok := generic(cql)
		if got := name + ": " + strings.Join(args, "|"); !ok || got != want {
			t.Errorf("generic(%s) = %q, %v; want %q", cql, got, ok, want)
		}
	}
	// A type with no arguments has none to split.
	for _, cql := range []string{"text", "address", "list<text", "list>"} {
		if _, _, ok := generic(cql); ok {
			t.Errorf("%s was read as a type with arguments", cql)
		}
	}
}
