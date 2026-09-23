package oracle

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Named parameters become Oracle's own (FR-5.7).

// A name used twice is one parameter given once: Oracle's placeholders are
// named, which is what a name used twice already means.
func TestANameUsedTwiceIsOneParameter(t *testing.T) {
	sql, args, err := bindNamed(source.Statement{
		SQL:   "SELECT * FROM t WHERE a = :who OR b = :who OR c = :other",
		Named: map[string]any{"who": int64(1), "other": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT * FROM t WHERE a = :1 OR b = :1 OR c = :2"
	if sql != want {
		t.Errorf("it reads\n%s\nwant\n%s", sql, want)
	}
	if len(args) != 2 || args[0] != int64(1) || args[1] != "x" {
		t.Errorf("it binds %#v", args)
	}
}

func TestAStatementWithNoNamesIsLeftAlone(t *testing.T) {
	sql, args, err := bindNamed(source.Statement{SQL: "SELECT :1 FROM dual", Args: []any{int64(1)}})
	if err != nil || sql != "SELECT :1 FROM dual" || len(args) != 1 {
		t.Errorf("%q %#v %v", sql, args, err)
	}
}

func TestNamedAndPositionalParametersCannotBeMixed(t *testing.T) {
	if _, _, err := bindNamed(source.Statement{SQL: "SELECT :a, :1 FROM dual",
		Named: map[string]any{"a": 1}, Args: []any{2}}); err == nil {
		t.Error("a statement using both was accepted")
	}
}

func TestANameWithNoValueIsRefused(t *testing.T) {
	if _, _, err := bindNamed(source.Statement{SQL: "SELECT :missing FROM dual",
		Named: map[string]any{"other": 1}}); err == nil {
		t.Error("a name nobody gave a value was accepted")
	}
}
