package redis

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func TestACommandIsALineOfWordsAndQuotedStrings(t *testing.T) {
	cases := map[string][]string{
		"GET user:1":                 {"GET", "user:1"},
		"  SET  a   b  ":             {"SET", "a", "b"},
		"GET\tuser:1":                {"GET", "user:1"},
		`SET greeting "hello world"`: {"SET", "greeting", "hello world"},
		`SET a "say \"hi\""`:         {"SET", "a", `say "hi"`},
		`SET a "line\nbreak"`:        {"SET", "a", "line\nbreak"},
		`SET a "\x41\x64\x61"`:       {"SET", "a", "Ada"},
		`SET a 'it\'s'`:              {"SET", "a", "it's"},
		`SET a 'no \n escape'`:       {"SET", "a", `no \n escape`},
		`HSET h field ""`:            {"HSET", "h", "field", ""},
		"":                           nil,
		"JSON.SET doc $ '{\"a\":1}'": {"JSON.SET", "doc", "$", `{"a":1}`},
	}
	for line, want := range cases {
		got, err := parseArgs(line)
		if err != nil {
			t.Errorf("%s: %v", line, err)
			continue
		}
		if len(got) != len(want) {
			t.Errorf("%s: %q, want %q", line, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: %q, want %q", line, got, want)
				break
			}
		}
	}
	for _, line := range []string{`SET a "unclosed`, `SET a 'unclosed`, `SET a "closed"x`, `SET a 'closed'x`} {
		if _, err := parseArgs(line); err == nil {
			t.Errorf("%s: read", line)
		}
	}
}

func TestAScriptIsACommandALine(t *testing.T) {
	script := "# what Ada holds\nGET user:1\n\n  HGETALL user:1:profile  \n# and nothing else\n"
	stmts := splitCommands(script)
	if len(stmts) != 2 {
		t.Fatalf("%d commands: %+v", len(stmts), stmts)
	}
	if stmts[0].Text != "GET user:1" || stmts[1].Text != "HGETALL user:1:profile" {
		t.Errorf("commands %+v", stmts)
	}
	// The offset is where the command is, so an error can be shown under it.
	for _, st := range stmts {
		if got := script[st.Offset : st.Offset+len(st.Text)]; got != st.Text {
			t.Errorf("%q is at %d, where the script has %q", st.Text, st.Offset, got)
		}
	}
	if len(splitCommands("   \n# nothing\n")) != 0 {
		t.Error("a script of comments is a script of nothing")
	}
}

func TestWhatACommandDoes(t *testing.T) {
	cases := map[string]source.Access{
		"GET user:1":               source.AccessRead,
		"hgetall user:1":           source.AccessRead,
		"SCAN 0 MATCH user:*":      source.AccessRead,
		"INFO keyspace":            source.AccessRead,
		"TTL user:1":               source.AccessRead,
		"JSON.GET doc $":           source.AccessRead,
		"CONFIG GET databases":     source.AccessRead,
		"CLIENT LIST":              source.AccessRead,
		"OBJECT ENCODING user:1":   source.AccessRead,
		"XINFO STREAM events":      source.AccessRead,
		"SET user:1 Ada":           source.AccessWrite,
		"DEL user:1":               source.AccessWrite,
		"EXPIRE user:1 60":         source.AccessWrite,
		"XADD events * a b":        source.AccessWrite,
		"XGROUP CREATE events g $": source.AccessWrite,
		"JSON.SET doc $ 1":         source.AccessWrite,
		"SOMETHING nobody knows":   source.AccessWrite,
		"FLUSHALL":                 source.AccessDDL,
		"flushdb":                  source.AccessDDL,
		"CONFIG SET appendonly no": source.AccessDDL,
		"CLIENT KILL ID 4":         source.AccessDDL,
		"SHUTDOWN NOSAVE":          source.AccessDDL,
		"DEBUG SLEEP 10":           source.AccessDDL,
		"SCRIPT FLUSH":             source.AccessDDL,
		"ACL SETUSER ada on":       source.AccessDDL,
		"MODULE LIST":              source.AccessDDL,
		"REPLICAOF no one":         source.AccessDDL,
	}
	for line, want := range cases {
		args, err := parseArgs(line)
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if got := commandAccess(args); got != want {
			t.Errorf("%s does %v, want %v", line, got, want)
		}
	}
	// A command nobody can even read is taken to change everything.
	s := &redisSource{}
	if got := s.Classify(`SET a "unclosed`); got != source.AccessDDL {
		t.Errorf("a command that cannot be read is %v", got)
	}
	if got := s.Classify("  "); got != source.AccessDDL {
		t.Errorf("nothing at all is %v", got)
	}
	if got := s.Classify("get user:1"); got != source.AccessRead {
		t.Errorf("a read is %v", got)
	}
}

func TestWhatThisConsoleWillNotRun(t *testing.T) {
	for _, line := range []string{"SUBSCRIBE news", "psubscribe news.*", "MONITOR"} {
		args, err := parseArgs(line)
		if err != nil {
			t.Fatal(err)
		}
		why, refused := refusedHere(args)
		if !refused || why == "" {
			t.Errorf("%s: %q %v", line, why, refused)
		}
	}
	if _, refused := refusedHere([]string{"GET", "user:1"}); refused {
		t.Error("a read was refused")
	}
}

func TestAReplyIsDrawnAsWhatItIs(t *testing.T) {
	cases := []struct {
		what  string
		reply any
		cols  []string
		rows  int
		first any
	}{
		{"a word", "Ada", []string{"reply"}, 1, "Ada"},
		{"a number", int64(3), []string{"reply"}, 1, int64(3)},
		{"a list of words", []any{"a", "b"}, []string{"reply"}, 2, "a"},
		{"a list of lists", []any{[]any{"a", "b"}}, []string{"reply"}, 1, model.JSON(`["a","b"]`)},
		// The pairs read the same way twice, however the map was walked.
		{"the pairs of a map", map[any]any{"maxmemory": "0", "appendonly": "no", "save": ""},
			[]string{"name", "value"}, 3, "appendonly"},
		{"nothing in a list", []any{}, []string{"reply"}, 0, nil},
	}
	for _, c := range cases {
		cols, rows := replyRows(c.reply)
		names := make([]string, 0, len(cols))
		for _, col := range cols {
			names = append(names, col.Name)
		}
		if strings.Join(names, " ") != strings.Join(c.cols, " ") {
			t.Errorf("%s: columns %v, want %v", c.what, names, c.cols)
		}
		if len(rows) != c.rows {
			t.Errorf("%s: %d rows, want %d", c.what, len(rows), c.rows)
			continue
		}
		if c.rows > 0 && fmt.Sprintf("%T %v", rows[0][0], rows[0][0]) != fmt.Sprintf("%T %v", c.first, c.first) {
			t.Errorf("%s: first %T %v, want %T %v", c.what, rows[0][0], rows[0][0], c.first, c.first)
		}
	}
	// A column is the type its values are, and text where they are not all
	// one; what is not text is bytes.
	if cols, _ := replyRows([]any{int64(1), int64(2)}); cols[0].Type.Class != model.TypeInteger {
		t.Errorf("a list of numbers is %v", cols[0].Type.Class)
	}
	if cols, _ := replyRows([]any{int64(1), "two"}); cols[0].Type.Class != model.TypeString {
		t.Errorf("a list of both is %v", cols[0].Type.Class)
	}
	if _, rows := replyRows(string([]byte{0xff, 0xfe})); len(rows) != 1 {
		t.Fatal("bytes are a row")
	} else if _, ok := rows[0][0].([]byte); !ok {
		t.Errorf("what is not text came back as %T", rows[0][0])
	}
}

func TestWhatAnAnswerSays(t *testing.T) {
	ok := replied(now(), "OK")
	if len(ok.Messages) != 1 || ok.Messages[0].Text != "OK" {
		t.Errorf("OK says %+v", ok.Messages)
	}
	n := replied(now(), int64(4))
	if n.Affected != 4 {
		t.Errorf("a number of four affected %d", n.Affected)
	}
	list := replied(now(), []any{"a", "b", "c"})
	if list.Affected != 3 {
		t.Errorf("three back affected %d", list.Affected)
	}
	if nothing(now()).Messages[0].Text != "(nil)" {
		t.Error("nothing at all says nothing")
	}
}

func TestWhatABrowseSends(t *testing.T) {
	s := &redisSource{}
	db := model.NewRef(model.KindDatabase, "db1")
	st, err := s.BuildBrowse(db, source.BrowseOptions{Limit: 50})
	if err != nil || st.SQL != "SCAN 0 COUNT 100" {
		t.Errorf("a keyspace: %q, %v", st.SQL, err)
	}
	st, err = s.BuildBrowse(db, source.BrowseOptions{Limit: 50, Where: "user:*",
		Filters: []source.Filter{{Column: "type", Op: source.OpEqual, Values: []any{"hash"}}}})
	if err != nil || st.SQL != "SCAN 0 MATCH user:* COUNT 100 TYPE hash" {
		t.Errorf("a keyspace narrowed: %q, %v", st.SQL, err)
	}
	// A pattern with a space in it is quoted, so the command reads back as
	// the one that was sent.
	st, err = s.BuildBrowse(db, source.BrowseOptions{Limit: 50, Where: "a name*"})
	if err != nil || st.SQL != `SCAN 0 MATCH "a name*" COUNT 100` {
		t.Errorf("a pattern with a space: %q, %v", st.SQL, err)
	}
	// What a key holds is only known once something has read it.
	key := model.NewRef(model.KindKey, "db1", "user:1")
	if _, err := s.BuildBrowse(key, source.BrowseOptions{}); err == nil ||
		!strings.Contains(err.Error(), "user:1") {
		t.Errorf("a key nothing has read: %v", err)
	}
	for kind, want := range map[string]string{
		"string":    "GET user:1",
		"list":      "LRANGE user:1 0 199",
		"hash":      "HSCAN user:1 0 COUNT 200",
		"set":       "SSCAN user:1 0 COUNT 200",
		"zset":      "ZSCAN user:1 0 COUNT 200",
		"stream":    "XRANGE user:1 - + COUNT 200",
		"ReJSON-RL": "JSON.GET user:1 $",
	} {
		s.kindSeen(key, kind)
		st, err := s.BuildBrowse(key, source.BrowseOptions{})
		if err != nil || st.SQL != want {
			t.Errorf("a %s: %q, %v; want %q", kind, st.SQL, err, want)
		}
	}
	s.kindSeen(key, "hash")
	st, err = s.BuildBrowse(key, source.BrowseOptions{Where: "c*", Limit: 10})
	if err != nil || st.SQL != "HSCAN user:1 0 MATCH c* COUNT 100" {
		t.Errorf("a hash narrowed: %q, %v", st.SQL, err)
	}
}

func TestHowAnObjectIsWrittenAndNothingIsBound(t *testing.T) {
	s := &redisSource{}
	if got := s.QuoteIdentifier("user:1"); got != "user:1" {
		t.Errorf("a plain name is written %s", got)
	}
	if got := s.QuoteIdentifier("a name"); got != `"a name"` {
		t.Errorf("a name with a space is written %s", got)
	}
	if got := s.QualifyRef(model.NewRef(model.KindKey, "db1", "user:1")); got != "user:1" {
		t.Errorf("a key is addressed as %s", got)
	}
	if got := s.QualifyRef(model.NewRef(model.KindDatabase, "db1")); got != "db1" {
		t.Errorf("a database is addressed as %s", got)
	}
	if got := s.QualifyRef(model.ObjectRef{}); got != "" {
		t.Errorf("nothing is addressed as %s", got)
	}
	if got := s.Placeholder(1); got != "" {
		t.Errorf("a command binds %q", got)
	}
}

// now is a moment a command started at.
func now() time.Time { return time.Now() }
