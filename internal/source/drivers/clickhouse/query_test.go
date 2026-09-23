package clickhouse

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Named parameters become ClickHouse's own (FR-5.7).

// A name used twice is bound twice: the placeholders here are positional,
// so each use is its own.
func TestANameUsedTwiceIsBoundTwice(t *testing.T) {
	sql, args, err := bindNamed(source.Statement{
		SQL:   "SELECT * FROM t WHERE a = :who OR b = :who OR c = :other",
		Named: map[string]any{"who": int64(1), "other": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "SELECT * FROM t WHERE a = ? OR b = ? OR c = ?"
	if sql != want {
		t.Errorf("it reads\n%s\nwant\n%s", sql, want)
	}
	if len(args) != 3 || args[0] != int64(1) || args[1] != int64(1) || args[2] != "x" {
		t.Errorf("it binds %#v", args)
	}
}

// A statement with no names is sent as it was written.
func TestAStatementWithNoNamesIsLeftAlone(t *testing.T) {
	sql, args, err := bindNamed(source.Statement{SQL: "SELECT ?", Args: []any{int64(1)}})
	if err != nil || sql != "SELECT ?" || len(args) != 1 {
		t.Errorf("%q %#v %v", sql, args, err)
	}
}

// The two ways of naming a value cannot be mixed: which one a driver binds
// first is not something anybody should have to know.
func TestNamedAndPositionalParametersCannotBeMixed(t *testing.T) {
	if _, _, err := bindNamed(source.Statement{SQL: "SELECT :a, ?",
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

// Which statements answer with rows has to be decided before one is sent:
// asking this driver for rows a statement does not have ends the
// connection, not just the statement.
func TestWhichStatementsAnswerWithRows(t *testing.T) {
	for _, sql := range []string{
		"SELECT 1", "select 1", "  SELECT 1", "WITH n AS (SELECT 1) SELECT * FROM n",
		"SHOW TABLES", "DESCRIBE TABLE t", "DESC t", "EXPLAIN SELECT 1",
		"EXISTS TABLE t", "CHECK TABLE t", "(SELECT 1)", "-- a note\nSELECT 1",
	} {
		if !returnsRows(sql) {
			t.Errorf("%q was sent as a statement with no result", sql)
		}
	}
	for _, sql := range []string{
		"INSERT INTO t VALUES (1)", "CREATE TABLE t (a UInt8) ENGINE = Memory",
		"DROP TABLE t", "SET max_threads = 4", "USE shop", "OPTIMIZE TABLE t",
		"ALTER TABLE t UPDATE a = 1 WHERE b = 2", "SYSTEM RELOAD DICTIONARIES",
		"", "-- only a note",
	} {
		if returnsRows(sql) {
			t.Errorf("%q was sent as a statement that answers with rows", sql)
		}
	}
}
