package sqlserver

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Named parameters become SQL Server's own (FR-5.7).

// A name used twice is one parameter given once: SQL Server binds by name,
// so the same @p stands in both places.
func TestANameUsedTwiceIsOneParameter(t *testing.T) {
	sql, args, err := bindNamed(source.Statement{
		SQL:   "SELECT * FROM t WHERE a = :who OR b = :who OR c = :other",
		Named: map[string]any{"who": int64(1), "other": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT * FROM t WHERE a = @p1 OR b = @p1 OR c = @p2"
	if sql != want {
		t.Errorf("it reads\n%s\nwant\n%s", sql, want)
	}
	if len(args) != 2 || args[0] != int64(1) || args[1] != "x" {
		t.Errorf("it binds %#v", args)
	}
}

// A statement with no names is sent as it was written.
func TestAStatementWithNoNamesIsLeftAlone(t *testing.T) {
	sql, args, err := bindNamed(source.Statement{SQL: "SELECT @p1", Args: []any{int64(1)}})
	if err != nil || sql != "SELECT @p1" || len(args) != 1 {
		t.Errorf("%q %#v %v", sql, args, err)
	}
}

// The two ways of naming a value cannot be mixed: which one a driver binds
// first is not something anybody should have to know.
func TestNamedAndPositionalParametersCannotBeMixed(t *testing.T) {
	if _, _, err := bindNamed(source.Statement{SQL: "SELECT :a, @p1",
		Named: map[string]any{"a": 1}, Args: []any{2}}); err == nil {
		t.Error("a statement using both was accepted")
	}
}

// A name with no value is an error: nothing is bound in its place.
func TestANameWithNoValueIsRefused(t *testing.T) {
	if _, _, err := bindNamed(source.Statement{SQL: "SELECT :missing",
		Named: map[string]any{"other": 1}}); err == nil {
		t.Error("a name nobody gave a value was accepted")
	}
}
