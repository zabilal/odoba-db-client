package redis

import (
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func keyspaceRendered(t *testing.T, c source.RowChange) (string, string) {
	t.Helper()
	w, desc, err := keyspaceWrite(c)
	if err != nil {
		t.Fatalf("%+v: %v", c, err)
	}
	return w.command, desc
}

func TestAKeyIsChangedByHowLongItHasLeftAndWhatItIsCalled(t *testing.T) {
	cases := []struct {
		what    string
		change  source.RowChange
		command string
		said    string
	}{
		{"a length of time", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"ttl": "30m"}}, "PEXPIRE user:1 1800000", "user:1 expires in 30m0s"},
		{"as the grid holds one", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"ttl": 90 * time.Second}}, "PEXPIRE user:1 90000", "user:1 expires in 1m30s"},
		{"a number of seconds", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"ttl": "3600"}}, "PEXPIRE user:1 3600000", "user:1 expires in 1h0m0s"},
		{"and a fraction of one", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"ttl": "1.5"}}, "PEXPIRE user:1 1500", "user:1 expires in 1.5s"},
		{"nothing at all", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"ttl": nil}}, "PERSIST user:1", "user:1 never expires"},
		{"a value taken away", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"ttl": model.Removed{}}}, "PERSIST user:1", "user:1 never expires"},
		{"an empty cell", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"ttl": ""}}, "PERSIST user:1", "user:1 never expires"},
		{"another name", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"key": "user:2"}}, "RENAMENX user:1 user:2", "Rename user:1 to user:2"},
		{"gone altogether", source.RowChange{Kind: source.ChangeDelete, Key: []any{"user:1"}},
			"DEL user:1", "Delete the key user:1"},
	}
	for _, c := range cases {
		command, said := keyspaceRendered(t, c.change)
		if command != c.command {
			t.Errorf("%s: %s, want %s", c.what, command, c.command)
		}
		if said != c.said {
			t.Errorf("%s: said %q, want %q", c.what, said, c.said)
		}
	}
}

func TestWhatAKeyItselfCannotBeToldToDo(t *testing.T) {
	cases := []struct {
		what   string
		change source.RowChange
		says   string
	}{
		{"a key is written into being", source.RowChange{Kind: source.ChangeInsert,
			Values: map[string]any{"key": "user:9"}}, "written to it"},
		{"a key is of the kind of what it holds", source.RowChange{Kind: source.ChangeUpdate,
			Key: []any{"user:1"}, Values: map[string]any{"type": "hash"}}, "kind"},
		{"one change at a time", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"key": "user:2", "ttl": "30m"}}, "one change at a time"},
		{"a name that is no name", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"key": "  "}}, "no name"},
		{"a name it has already", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"key": "user:1"}}, "changes nothing"},
		{"an update that changes nothing", source.RowChange{Kind: source.ChangeUpdate,
			Key: []any{"user:1"}}, "changes nothing"},
		{"a time that is no time", source.RowChange{Kind: source.ChangeUpdate, Key: []any{"user:1"},
			Values: map[string]any{"ttl": "soon"}}, "not a length of time"},
		{"a key addressed by more than its name", source.RowChange{Kind: source.ChangeDelete,
			Key: []any{"user:1", "hash"}}, "one key"},
	}
	for _, c := range cases {
		_, _, err := keyspaceWrite(c.change)
		if err == nil {
			t.Errorf("%s: accepted", c.what)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %q, want something about %q", c.what, err, c.says)
		}
	}
}

func TestATimeToLiveIsReadHoweverItIsWritten(t *testing.T) {
	for _, c := range []struct {
		in   any
		want time.Duration
	}{
		{nil, 0},
		{model.Removed{}, 0},
		{"", 0},
		{"  ", 0},
		{int64(60), time.Minute},
		{90.0, 90 * time.Second},
		{time.Hour, time.Hour},
		{"1h30m", 90 * time.Minute},
		{" 45s ", 45 * time.Second},
		{"-1", -time.Second},
	} {
		got, err := ttlValue(c.in)
		if err != nil || got != c.want {
			t.Errorf("ttlValue(%v) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"soon", "30 minutes", "1h30", ""} {
		if in == "" {
			continue
		}
		if _, err := ttlValue(in); err == nil {
			t.Errorf("ttlValue(%q) was read", in)
		}
	}
}
