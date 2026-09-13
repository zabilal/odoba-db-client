package redis

import (
	"errors"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// holds is what the parts of a value hold, as a plan reads them to render a
// change that carries one over.
func holds(parts map[string]string) held {
	return func(part string) (string, bool) {
		v, ok := parts[part]
		return v, ok
	}
}

// rendered is the command one change would send, and the line a person reads
// beside it.
func rendered(t *testing.T, kind, name string, c source.RowChange) (string, string) {
	t.Helper()
	cols, id, err := valueColumns(kind)
	if err != nil {
		t.Fatal(err)
	}
	w, desc, err := writeOf(kind, name, cols, id, c, holds(map[string]string{"city": "London", "Grace": "7"}))
	if err != nil {
		t.Fatalf("%s: %v", kind, err)
	}
	return w.command, desc
}

func refused(t *testing.T, kind, name string, c source.RowChange) string {
	t.Helper()
	cols, id, err := valueColumns(kind)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := writeOf(kind, name, cols, id, c, holds(nil)); err != nil {
		return err.Error()
	}
	t.Fatalf("%s: the change was accepted", kind)
	return ""
}

func TestAChangeIsTheCommandItWouldSend(t *testing.T) {
	cases := []struct {
		kind    string
		change  source.RowChange
		command string
		said    string
	}{
		{"string", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"value": "Ada Lovelace"}}, `SET user:1 "Ada Lovelace"`, "Set user:1"},
		{"hash", source.RowChange{Kind: source.ChangeInsert,
			Values: map[string]any{"field": "city", "value": "London"}}, "HSET user:1 city London", "Add the field city"},
		{"hash", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"city"},
			Values: map[string]any{"value": "Paris"}}, "HSET user:1 city Paris", "Change the field city"},
		// A field moved and not otherwise changed carries what it holds, and
		// the plan shows the value it will really write.
		{"hash", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"city"},
			Values: map[string]any{"field": "town"}}, "HDEL user:1 city ; HSET user:1 town London", "Change the field city"},
		{"hash", source.RowChange{Kind: source.ChangeDelete, Key: []any{"city"}}, "HDEL user:1 city", "Delete the field city"},
		{"list", source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"value": "d"}},
			"RPUSH user:1 d", "Add an element at the end"},
		{"list", source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(2)},
			Values: map[string]any{"value": "c"}}, "LSET user:1 2 c", "Set the element at 2"},
		{"set", source.RowChange{Kind: source.ChangeInsert, Values: map[string]any{"member": "go"}},
			"SADD user:1 go", "Add go"},
		{"set", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"go"},
			Values: map[string]any{"member": "golang"}}, "SREM user:1 go ; SADD user:1 golang", "Change go"},
		{"set", source.RowChange{Kind: source.ChangeDelete, Key: []any{"go"}}, "SREM user:1 go", "Remove go"},
		{"zset", source.RowChange{Kind: source.ChangeInsert,
			Values: map[string]any{"member": "Grace", "score": 7.0}}, "ZADD user:1 NX 7 Grace", "Add Grace"},
		{"zset", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"Grace"},
			Values: map[string]any{"score": "8.5"}}, "ZADD user:1 8.5 Grace", "Change Grace"},
		{"zset", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"Grace"},
			Values: map[string]any{"member": "Hopper"}}, "ZREM user:1 Grace ; ZADD user:1 7 Hopper", "Change Grace"},
		{"zset", source.RowChange{Kind: source.ChangeDelete, Key: []any{"Grace"}}, "ZREM user:1 Grace", "Remove Grace"},
	}
	for _, c := range cases {
		command, said := rendered(t, c.kind, "user:1", c.change)
		if command != c.command {
			t.Errorf("%s: %s, want %s", c.kind, command, c.command)
		}
		if said != c.said {
			t.Errorf("%s: said %q, want %q", c.kind, said, c.said)
		}
	}
}

func TestAChangeAKindCannotTakeIsRefused(t *testing.T) {
	cases := []struct {
		what   string
		kind   string
		change source.RowChange
		says   string
	}{
		{"a string is one value", "string", source.RowChange{Kind: source.ChangeInsert,
			Values: map[string]any{"value": "x"}}, "one value"},
		{"and it is not deleted a row at a time", "string",
			source.RowChange{Kind: source.ChangeDelete, Key: []any{"user:1"}}, "one value"},
		{"a key is renamed on its own", "string", source.RowChange{Kind: source.ChangeUpdate,
			Key: []any{"user:1"}, Values: map[string]any{"key": "user:2"}}, "renamed"},
		{"an element is added at the end", "list", source.RowChange{Kind: source.ChangeInsert,
			Values: map[string]any{"index": int64(2), "value": "c"}}, "end of a list"},
		{"an element is where it is", "list", source.RowChange{Kind: source.ChangeUpdate,
			Key: []any{int64(1)}, Values: map[string]any{"index": int64(2)}}, "moving one"},
		{"an element is removed by value", "list", source.RowChange{Kind: source.ChangeDelete,
			Key: []any{int64(1)}}, "by value"},
		{"a position is a number", "list", source.RowChange{Kind: source.ChangeUpdate,
			Key: []any{"second"}, Values: map[string]any{"value": "c"}}, "not a position"},
		{"a row is addressed by one value", "hash", source.RowChange{Kind: source.ChangeUpdate,
			Key: []any{"city", "town"}, Values: map[string]any{"value": "x"}}, "one field"},
		{"and by a value that is there", "hash", source.RowChange{Kind: source.ChangeUpdate,
			Key: []any{nil}, Values: map[string]any{"value": "x"}}, "empty"},
		{"an update that changes nothing", "hash", source.RowChange{Kind: source.ChangeUpdate,
			Key: []any{"city"}}, "changes nothing"},
		{"a field with no name", "hash", source.RowChange{Kind: source.ChangeInsert,
			Values: map[string]any{"value": "London"}}, "no name"},
		{"a part of a value is not taken away", "hash", source.RowChange{Kind: source.ChangeUpdate,
			Key: []any{"city"}, Values: map[string]any{"value": model.Removed{}}}, "take away"},
		{"a score is a number", "zset", source.RowChange{Kind: source.ChangeInsert,
			Values: map[string]any{"member": "Grace", "score": "high"}}, "not a score"},
	}
	for _, c := range cases {
		if says := refused(t, c.kind, "user:1", c.change); !strings.Contains(says, c.says) {
			t.Errorf("%s: %q, want something about %q", c.what, says, c.says)
		}
	}
}

func TestNothingHereReadsWhatItCannotWrite(t *testing.T) {
	// The kinds T2.42 brings are named as what they are, and a kind nothing
	// knows is not taken for one of them.
	for _, kind := range []string{"stream", "ReJSON-RL"} {
		_, _, err := valueColumns(kind)
		if !errors.Is(err, errNotYet) || !strings.Contains(err.Error(), kind) {
			t.Errorf("%s: %v; it is coming, and the message says which", kind, err)
		}
	}
	// A kind nothing here reads is not one of those: it is not coming, it is
	// unknown, and saying otherwise would promise a task that is not there.
	_, _, err := valueColumns("timeseries")
	if err == nil || errors.Is(err, errNotYet) {
		t.Errorf("a kind nothing here reads: %v", err)
	}
}

func TestWhatAKindOfValueLooksLike(t *testing.T) {
	cases := map[string]struct {
		cols []string
		id   string
	}{
		"string": {[]string{"key", "value"}, "key"},
		"hash":   {[]string{"field", "value"}, "field"},
		"list":   {[]string{"index", "value"}, "index"},
		"set":    {[]string{"member"}, "member"},
		"zset":   {[]string{"member", "score"}, "member"},
	}
	for kind, want := range cases {
		cols, id, err := valueColumns(kind)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		names := make([]string, 0, len(cols))
		for _, c := range cols {
			names = append(names, c.Name)
		}
		if strings.Join(names, " ") != strings.Join(want.cols, " ") {
			t.Errorf("%s shows %v, want %v", kind, names, want.cols)
		}
		if len(id) != 1 || id[0] != want.id {
			t.Errorf("%s is addressed by %v, want %s", kind, id, want.id)
		}
	}
	// A key's own name and an element's position are not editable: one is
	// the address of the whole value, the other of the element.
	str, _, _ := valueColumns("string")
	list, _, _ := valueColumns("list")
	if !str[0].ReadOnly || !list[0].ReadOnly {
		t.Error("the address of a value is not something to type over")
	}
	if list[0].Type.Class != model.TypeInteger {
		t.Errorf("a position is %v", list[0].Type.Class)
	}
	zset, _, _ := valueColumns("zset")
	if zset[1].Type.Class != model.TypeFloat {
		t.Errorf("a score is %v", zset[1].Type.Class)
	}
}

func TestAValueIsQuotedAsItWouldBeTyped(t *testing.T) {
	for in, want := range map[string]string{
		"city":        "city",
		"":            `""`,
		"New York":    `"New York"`,
		`say "hi"`:    `"say \"hi\""`,
		"a\nb":        `"a\nb"`,
		`back\slash`:  `"back\\slash"`,
		"caffè":       `"caffè"`,
		"user:1:name": "user:1:name",
	} {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestAValueIsWrittenAsTheTextOfIt(t *testing.T) {
	for _, c := range []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"Ada", "Ada"},
		{[]byte{0x41, 0x64, 0x61}, "Ada"},
		{int64(7), "7"},
		{7.5, "7.5"},
		{model.Decimal("1.250"), "1.250"},
		{model.Default{}, ""},
		{true, "true"},
	} {
		if got := str(c.in); got != c.want {
			t.Errorf("str(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	// A score is a number however the grid held it, and a whole one keeps no
	// decimal point.
	for _, c := range []struct {
		in   any
		want float64
	}{{nil, 0}, {7.5, 7.5}, {int64(3), 3}, {" 8 ", 8}, {model.Default{}, 0}} {
		got, err := scoreOf(c.in)
		if err != nil || got != c.want {
			t.Errorf("scoreOf(%v) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	if number(7) != "7" || number(7.5) != "7.5" {
		t.Errorf("scores are written %s and %s", number(7), number(7.5))
	}
}

func TestAKeyIsNamedByItsDatabaseAndItsName(t *testing.T) {
	db, name, err := keyOf(model.NewRef(model.KindKey, "db3", "user:1"))
	if err != nil || db != 3 || name != "user:1" {
		t.Errorf("keyOf = %d, %q, %v", db, name, err)
	}
	for _, ref := range []model.ObjectRef{
		model.NewRef(model.KindDatabase, "db3"),
		model.NewRef(model.KindKey, "db3"),
		model.NewRef(model.KindKey, "db3", "user:1", "city"),
		model.NewRef(model.KindKey, "sales", "user:1"),
		model.NewRef(model.KindKey, "db3", ""),
	} {
		if _, _, err := keyOf(ref); err == nil {
			t.Errorf("%v was read as a key", ref)
		}
	}
}

func TestOnlyWhatTheServerWalksTakesAPattern(t *testing.T) {
	byMember := source.BrowseOptions{Filters: []source.Filter{
		{Column: "member", Op: source.OpLike, Values: []any{"a%"}}}}
	cols, _, _ := valueColumns("set")
	got, err := memberMatch("set", cols, byMember)
	if err != nil || got != "a*" {
		t.Errorf("a set's members: %q, %v", got, err)
	}
	// A list is read by position and a string is one value: neither is
	// narrowed by the server, so neither is narrowed at all.
	for _, kind := range []string{"list", "string"} {
		cols, _, _ := valueColumns(kind)
		byFirst := source.BrowseOptions{Filters: []source.Filter{
			{Column: cols[0].Name, Op: source.OpLike, Values: []any{"a%"}}}}
		if _, err := memberMatch(kind, cols, byFirst); err == nil {
			t.Errorf("a %s was narrowed", kind)
		}
	}
	// One pattern, on the part a row is known by, and nothing else about it.
	cols, _, _ = valueColumns("zset")
	refused := map[string]source.BrowseOptions{
		"two patterns": {Filters: []source.Filter{
			{Column: "member", Op: source.OpLike, Values: []any{"a%"}},
			{Column: "member", Op: source.OpLike, Values: []any{"b%"}}}},
		"a score": {Filters: []source.Filter{{Column: "score", Op: source.OpGreater, Values: []any{1}}}},
		"members that do not match": {Filters: []source.Filter{
			{Column: "member", Op: source.OpLike, Values: []any{"a%"}, Negate: true}}},
		"an order the server does not hold": {Sorts: []source.Sort{{Column: "member"}}},
		"a condition":                       {Where: "member = 'Ada'"},
	}
	for what, opt := range refused {
		if _, err := memberMatch("zset", cols, opt); err == nil {
			t.Errorf("%s: accepted", what)
		}
	}
	if got, err := memberMatch("zset", cols, source.BrowseOptions{}); err != nil || got != "" {
		t.Errorf("nothing asked for: %q, %v", got, err)
	}
}
