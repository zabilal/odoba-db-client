package importer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWhatIsNotAConnectionSet(t *testing.T) {
	for _, c := range []struct{ what, body, says string }{
		{"not JSON", "nonsense", "is not a connection set"},
		{"somebody else's JSON", `{"kind":"other.thing","connections":[]}`, `it says it is "other.thing"`},
		{"no kind at all", `{"connections":[]}`, "is not a connection set"},
		{"from a newer version", `{"kind":"ikigai-db.connections","version":99,"connections":[]}`,
			"written by a newer version"},
	} {
		t.Run(c.what, func(t *testing.T) {
			_, err := readSetBytes([]byte(c.body), "a-file.json")
			if err == nil {
				t.Fatal("it was read as a connection set")
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("it said %v, wanted something about %q", err, c.says)
			}
		})
	}
}

// An older file is read, because that is what a version number is for.
func TestASetFromAnOlderVersionIsStillRead(t *testing.T) {
	body := `{"kind":"ikigai-db.connections","version":1,"connections":[
		{"name":"Orders","driver":"postgres","host":"db.example.com","port":5432}]}`
	found, err := readSetBytes([]byte(body), "a-file.json")
	if err != nil {
		t.Fatalf("it would not read it: %v", err)
	}
	if len(found) != 1 || !found[0].OK() || found[0].Connection.Host != "db.example.com" {
		t.Errorf("it read %+v", found)
	}
}

func TestAnEntryWithNoDriverIsReportedNotGuessedAt(t *testing.T) {
	body := `{"kind":"ikigai-db.connections","version":1,"connections":[
		{"name":"Mystery","host":"db.example.com"},
		{"name":"Orders","driver":"postgres","host":"db.example.com"}]}`
	found, err := readSetBytes([]byte(body), "a-file.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("it read %d entries", len(found))
	}
	if found[0].OK() {
		t.Error("a connection with no driver was imported as something")
	}
	if !strings.Contains(found[0].Note, "Mystery") {
		t.Errorf("it said %q, which does not say which one", found[0].Note)
	}
	if !found[1].OK() {
		t.Errorf("the good one was lost with it: %s", found[1].Note)
	}
}

func TestAnEmptySetIsAnAnswerNotAnError(t *testing.T) {
	found, err := readSetBytes([]byte(`{"kind":"ikigai-db.connections","version":1,"connections":[]}`), "a.json")
	if err != nil {
		t.Fatalf("an empty set was an error: %v", err)
	}
	if len(found) != 1 || found[0].OK() {
		t.Fatalf("it read %+v", found)
	}
	if !strings.Contains(found[0].Note, "no connections") {
		t.Errorf("it said %q", found[0].Note)
	}
}

func TestWhatASetSaysIsStillNeeded(t *testing.T) {
	body := `{"kind":"ikigai-db.connections","version":1,"connections":[
		{"name":"Tunnelled","driver":"postgres","host":"db.example.com",
		 "needs_secrets":["password","ssh_passphrase"]}]}`
	found, err := readSetBytes([]byte(body), "a-file.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(found[0].Note, "password and ssh_passphrase") {
		t.Errorf("it said %q, and should have named both", found[0].Note)
	}
}

func TestASetAlwaysHasAKindAndAVersionWrittenIntoIt(t *testing.T) {
	// Even one built without them, because whoever reads the file relies on
	// them being there.
	raw, err := MarshalSet(Set{})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back["kind"] != SetKind {
		t.Errorf("it says it is %v", back["kind"])
	}
	if back["version"] != float64(SetVersion) {
		t.Errorf("its version is %v", back["version"])
	}
	// An empty set is an empty list, not null: a file people read.
	if _, ok := back["connections"].([]any); !ok {
		t.Errorf("its connections are %#v", back["connections"])
	}
}

func TestNamesReadAsASentence(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want string
	}{
		{nil, "nothing"},
		{[]string{"password"}, "password"},
		{[]string{"password", "token"}, "password and token"},
		{[]string{"a", "b", "c"}, "a, b, and c"},
	} {
		if got := list(c.in); got != c.want {
			t.Errorf("list(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
