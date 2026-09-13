package redis

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func keyFilter(column string, op source.FilterOp, values ...any) source.Filter {
	return source.Filter{Column: column, Op: op, Values: values}
}

func TestADatabaseIsNamedByItsNumber(t *testing.T) {
	for _, name := range []string{"db0", "db15"} {
		if _, err := databaseOf(model.NewRef(model.KindDatabase, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n, err := databaseOf(model.NewRef(model.KindDatabase, "db7")); err != nil || n != 7 {
		t.Errorf("db7 = %d, %v", n, err)
	}
	for _, ref := range []model.ObjectRef{
		model.NewRef(model.KindTable, "db0"),
		model.NewRef(model.KindDatabase, "db0", "user:1"),
		model.NewRef(model.KindDatabase, "sales"),
	} {
		if _, err := databaseOf(ref); err == nil {
			t.Errorf("%v was read as a database of keys", ref)
		}
	}
}

func TestAKeyShowsItsNameItsKindAndWhatIsLeftOfIt(t *testing.T) {
	cols, err := projection(nil)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		names = append(names, c.Name)
	}
	if strings.Join(names, " ") != "key type ttl" {
		t.Errorf("columns %v", names)
	}
	if cols[2].Type.Class != model.TypeInterval || !cols[2].Type.Nullable {
		t.Errorf("a time to live is %+v; a key that never expires has none", cols[2].Type)
	}
	// Asked for fewer, a browse reads fewer: what a key holds and how long it
	// has left are a command each, and a page that shows neither sends none.
	only, err := projection([]string{"key"})
	if err != nil || len(only) != 1 || only[0].Name != "key" {
		t.Errorf("projection = %v, %v", only, err)
	}
	if _, err := projection([]string{"key", "value"}); err == nil {
		t.Error("a column a key has not got was accepted")
	}
}

func TestTheServerIsAskedForThePatternAndTheKind(t *testing.T) {
	cases := []struct {
		what  string
		opt   source.BrowseOptions
		match string
		kind  string
	}{
		{"everything", source.BrowseOptions{}, "", ""},
		{"a name outright", source.BrowseOptions{Filters: []source.Filter{keyFilter("key", source.OpEqual, "user:1")}}, "user:1", ""},
		{"a name within", source.BrowseOptions{Filters: []source.Filter{keyFilter("key", source.OpContains, "user")}}, "*user*", ""},
		{"a pattern", source.BrowseOptions{Filters: []source.Filter{keyFilter("key", source.OpLike, "user:%")}}, "user:*", ""},
		{"one character", source.BrowseOptions{Filters: []source.Filter{keyFilter("key", source.OpLike, "user:_")}}, "user:?", ""},
		{"a kind", source.BrowseOptions{Filters: []source.Filter{keyFilter("type", source.OpEqual, "hash")}}, "", "hash"},
		// A condition typed in the grid is a pattern, as the server matches
		// names by, and it is taken as it is written.
		{"a condition", source.BrowseOptions{Where: " user:* "}, "user:*", ""},
		{"a condition and a kind", source.BrowseOptions{Where: "user:*",
			Filters: []source.Filter{keyFilter("type", source.OpEqual, "set")}}, "user:*", "set"},
		{"both", source.BrowseOptions{Filters: []source.Filter{
			keyFilter("key", source.OpLike, "user:%"), keyFilter("type", source.OpEqual, "zset")}}, "user:*", "zset"},
	}
	for _, c := range cases {
		got, err := scanOf(c.opt)
		if err != nil {
			t.Errorf("%s: %v", c.what, err)
			continue
		}
		if got.match != c.match || got.kind != c.kind {
			t.Errorf("%s: match %q kind %q, want %q and %q", c.what, got.match, got.kind, c.match, c.kind)
		}
	}
}

func TestAGlobMatchesTheNameAndNothingElse(t *testing.T) {
	// A name holding a pattern's own characters is looked for as it is, not
	// as the pattern it would otherwise be.
	t.Run("a literal name", func(t *testing.T) {
		if got := glob(`cache:*[1]?\x`); got != `cache:\*\[1\]\?\\x` {
			t.Errorf("glob = %s", got)
		}
	})
	t.Run("the grid's own pattern language", func(t *testing.T) {
		for in, want := range map[string]string{
			"user:%": "user:*",
			"%:1":    "*:1",
			"a_c":    "a?c",
			// An escaped % or _ is the character itself, and a character the
			// server would read as a pattern is written out even there.
			`100\%`:     `100%`,
			`a\_b`:      `a_b`,
			`a\*b`:      `a\*b`,
			"star*here": `star\*here`,
			`ends\`:     `ends\\`,
		} {
			if got := globOfLike(in); got != want {
				t.Errorf("globOfLike(%q) = %q, want %q", in, got, want)
			}
		}
	})
}

func TestWhatTheServerCannotDoIsRefused(t *testing.T) {
	cases := map[string]source.BrowseOptions{
		"an order the keyspace has not": {Sorts: []source.Sort{{Column: "key"}}},
		// A condition here is a pattern, and a walk takes one pattern.
		"a condition beside a pattern": {Where: "user:*", Filters: []source.Filter{
			keyFilter("key", source.OpLike, "a%")}},
		"a column a key has not got":             {Filters: []source.Filter{keyFilter("value", source.OpEqual, "x")}},
		"keys picked by how long they have left": {Filters: []source.Filter{keyFilter("ttl", source.OpGreater, 60)}},
		"a kind no key is":                       {Filters: []source.Filter{keyFilter("type", source.OpEqual, "table")}},
		"a kind asked for as a range":            {Filters: []source.Filter{keyFilter("type", source.OpGreater, "hash")}},
		"names matched by a regular expression":  {Filters: []source.Filter{keyFilter("key", source.OpRegex, "^user")}},
		"names that do not match":                {Filters: []source.Filter{{Column: "key", Op: source.OpEqual, Values: []any{"a"}, Negate: true}}},
		"two patterns at once": {Filters: []source.Filter{
			keyFilter("key", source.OpLike, "a%"), keyFilter("key", source.OpLike, "b%")}},
		"a pattern of several values": {Filters: []source.Filter{keyFilter("key", source.OpLike, "a%", "b%")}},
	}
	for what, opt := range cases {
		if _, err := scanOf(opt); err == nil {
			t.Errorf("%s: accepted", what)
		}
	}
}

func TestWhatTTLSaysAboutAKey(t *testing.T) {
	answer := func(d time.Duration, err error) *goredis.DurationCmd {
		cmd := goredis.NewDurationCmd(context.Background(), time.Second, "ttl", "k")
		if err != nil {
			cmd.SetErr(err)
			return cmd
		}
		cmd.SetVal(d)
		return cmd
	}
	if v, ok := ttlOf(answer(90*time.Second, nil)); !ok || v != 90*time.Second {
		t.Errorf("a key with a minute and a half left: %v %v", v, ok)
	}
	// -1 is a key that will not expire, which is no time to live rather than
	// a negative one.
	for _, d := range []time.Duration{-1, -time.Second} {
		if v, ok := ttlOf(answer(d, nil)); !ok || v != nil {
			t.Errorf("a key that never expires: %v %v", v, ok)
		}
	}
	// -2 is a key that has gone between the walk and the question.
	for _, d := range []time.Duration{-2, -2 * time.Second} {
		if _, ok := ttlOf(answer(d, nil)); ok {
			t.Error("a key that is not there was read as one that is")
		}
	}
	if _, ok := ttlOf(answer(0, errors.New("connection lost"))); ok {
		t.Error("a command that failed was read as an answer")
	}
}

func TestHowMuchOfTheKeyspaceIsWalkedAtOnce(t *testing.T) {
	// A page's worth, but never so few that a page is a round trip for every
	// handful, and never so many that the server is held for long.
	if got := batchOf(3); got != minBatch {
		t.Errorf("a small page walks %d", got)
	}
	if got := batchOf(500); got != 500 {
		t.Errorf("a page of 500 walks %d", got)
	}
	if got := batchOf(100000); got != maxBatch {
		t.Errorf("a page of a hundred thousand walks %d", got)
	}
}

func TestAKeyThatWentBetweenTheWalkAndTheQuestionIsNoRow(t *testing.T) {
	kind := func(s string, err error) *goredis.StatusCmd {
		cmd := goredis.NewStatusCmd(context.Background(), "type", "k")
		if err != nil {
			cmd.SetErr(err)
		} else {
			cmd.SetVal(s)
		}
		return cmd
	}
	ttl := func(d time.Duration) *goredis.DurationCmd {
		cmd := goredis.NewDurationCmd(context.Background(), time.Second, "ttl", "k")
		cmd.SetVal(d)
		return cmd
	}
	row, ok := keyRow(keyColumns, "user:1", kind("hash", nil), ttl(time.Minute))
	if !ok || row[0] != "user:1" || row[1] != "hash" || row[2] != time.Minute {
		t.Errorf("a key that is there: %v %v", row, ok)
	}
	// TYPE answers "none" for a key that is not there, and TTL -2.
	if _, ok := keyRow(keyColumns, "user:1", kind("none", nil), ttl(time.Minute)); ok {
		t.Error("a key the server says it has not got was drawn")
	}
	if _, ok := keyRow(keyColumns, "user:1", kind("hash", nil), ttl(-2)); ok {
		t.Error("a key that expired while it was being asked about was drawn")
	}
	if _, ok := keyRow(keyColumns, "user:1", kind("", errors.New("gone")), ttl(time.Minute)); ok {
		t.Error("a command that failed was read as an answer")
	}
	// Asked for the name alone, nothing else is asked of the server, and
	// nothing else is read back.
	only, ok := keyRow(keyColumns[:1], "user:1", nil, nil)
	if !ok || len(only) != 1 || only[0] != "user:1" {
		t.Errorf("the name alone: %v %v", only, ok)
	}
}
