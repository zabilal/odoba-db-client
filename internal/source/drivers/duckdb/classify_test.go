//go:build duckdb

package duckdb

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Classification is the first of two defences (NFR-S4). Anything not
// confidently read-only is reported as mutating.
func TestWhatAStatementDoes(t *testing.T) {
	for _, c := range []struct {
		sql  string
		want source.Access
	}{
		{"SELECT 1", source.AccessRead},
		// FROM begins a statement here: this is how DuckDB spells a
		// SELECT over everything in a table.
		{"FROM people", source.AccessRead},
		{"FROM people SELECT name", source.AccessRead},
		{"select * from t where a = 'delete from t'", source.AccessRead},
		{"-- DROP TABLE t\nSELECT 1", source.AccessRead},
		{"SELECT /* DROP TABLE t */ 1", source.AccessRead},
		{"SELECT 1 /* a\nDELETE FROM t\nb */", source.AccessRead},
		{"SELECT '\n DELETE FROM t\n'", source.AccessRead},
		{"VALUES (1)", source.AccessRead},
		{"TABLE t", source.AccessRead},
		{"WITH x AS (SELECT 1) SELECT * FROM x", source.AccessRead},
		{"PIVOT t ON a USING sum(b)", source.AccessRead},
		{"UNPIVOT t ON a, b", source.AccessRead},
		{"SHOW TABLES", source.AccessRead},
		{"DESCRIBE people", source.AccessRead},
		{"SUMMARIZE people", source.AccessRead},
		{"BEGIN", source.AccessRead},
		{"COMMIT", source.AccessRead},
		{"ROLLBACK", source.AccessRead},
		{"EXPLAIN SELECT 1", source.AccessRead},
		{`SELECT "delete" FROM t`, source.AccessRead},
		{"", source.AccessRead},

		{"INSERT INTO t VALUES (1)", source.AccessWrite},
		{"UPDATE t SET a = 1 WHERE b = 2", source.AccessWrite},
		{"DELETE FROM t WHERE a = 1", source.AccessWrite},
		{"CALL pragma_version()", source.AccessWrite},
		// COPY reads a file into a table or writes one out of it, and
		// either way it touches the filesystem.
		{"COPY t FROM 'x.csv'", source.AccessWrite},
		{"COPY (SELECT * FROM t) TO 'x.csv'", source.AccessWrite},
		{"WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d", source.AccessWrite},
		{"WITH d AS (INSERT INTO t VALUES (1) RETURNING *) SELECT * FROM d", source.AccessWrite},
		{"SELECT nextval('s')", source.AccessWrite},
		{"SELECT setval('s', 1)", source.AccessWrite},
		{"SELECT 1; DROP TABLE t", source.AccessDDL},
		{"FLIBBERTIGIBBET t", source.AccessWrite},
		{`"t"`, source.AccessWrite},
		{"SELECT * INTO u FROM t", source.AccessDDL},

		{"CREATE TABLE t (a INTEGER)", source.AccessDDL},
		{"CREATE MACRO m() AS 1", source.AccessDDL},
		{"ALTER TABLE t ADD COLUMN b INTEGER", source.AccessDDL},
		{"DROP TABLE t", source.AccessDDL},
		{"TRUNCATE t", source.AccessDDL},
		{"COMMENT ON TABLE t IS 'x'", source.AccessDDL},
		{"IMPORT DATABASE 'dir'", source.AccessDDL},

		// An extension is native code with the run of the process.
		{"INSTALL httpfs", source.AccessAdmin},
		{"LOAD httpfs", source.AccessAdmin},
		{"SELECT load_extension('httpfs')", source.AccessAdmin},
		{"SELECT install_extension('httpfs')", source.AccessAdmin},
		// What the connection holds, and where an unqualified name looks.
		{"ATTACH 'other.duckdb' AS other", source.AccessAdmin},
		{"DETACH other", source.AccessAdmin},
		{"USE other", source.AccessAdmin},
		{"SET access_mode = 'READ_WRITE'", source.AccessAdmin},
		{"RESET access_mode", source.AccessAdmin},
		{"PRAGMA database_list", source.AccessAdmin},
		{"CHECKPOINT", source.AccessAdmin},
		{"FORCE CHECKPOINT", source.AccessAdmin},
		{"EXPORT DATABASE 'dir'", source.AccessAdmin},
		{"ANALYZE", source.AccessAdmin},

		{"EXPLAIN ANALYZE SELECT 1", source.AccessRead},
		{"EXPLAIN ANALYZE DELETE FROM t", source.AccessWrite},
		{"EXPLAIN ANALYZE CREATE TABLE t (a INTEGER)", source.AccessDDL},
		{"EXPLAIN ANALYZE", source.AccessWrite},
	} {
		if got := classify(c.sql); got != c.want {
			t.Errorf("classify(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

// A change that names no rows reaches all of them, and the window asks
// before it runs (FR-4.9).
func TestAChangeWithNoWhereIsNoticed(t *testing.T) {
	for _, sql := range []string{
		"DELETE FROM t",
		"UPDATE t SET a = 1",
		"SELECT 1; DELETE FROM t",
	} {
		if unboundedIn(sql) == nil {
			t.Errorf("%q reaches every row and nothing said so", sql)
		}
	}
	for _, sql := range []string{
		"DELETE FROM t WHERE a = 1",
		"UPDATE t SET a = 1 WHERE b = 2",
		"SELECT 1",
	} {
		if u := unboundedIn(sql); u != nil {
			t.Errorf("%q was called unbounded: %v", sql, u)
		}
	}
}

// A script is cut between statements, and not inside a routine's body.
func TestAScriptIsCutBetweenStatements(t *testing.T) {
	if got := splitScript("SELECT 1; SELECT 2"); len(got) != 2 {
		t.Fatalf("cut into %d: %+v", len(got), got)
	}
	if got := splitScript(`SELECT ';'`); len(got) != 1 {
		t.Errorf("a semicolon in a string cut the script into %d", len(got))
	}
	if got := splitScript(`SELECT "a;b" FROM t`); len(got) != 1 {
		t.Errorf("a semicolon in a name cut the script into %d", len(got))
	}
	// A routine written with a body holds ';' between its own statements,
	// and cutting there would send half a definition to the engine.
	body := `CREATE FUNCTION f() RETURNS INTEGER LANGUAGE SQL BEGIN ATOMIC SELECT 1; SELECT 2; END;
SELECT 3`
	if got := splitScript(body); len(got) != 2 {
		t.Errorf("a routine's body was cut open: %d statements", len(got))
	}
}

// A quoted name is its own name: "LOAD_EXTENSION" is not load_extension.
func TestAQuotedNameIsItsOwnName(t *testing.T) {
	if got := classify(`SELECT "LOAD_EXTENSION"('x')`); got == source.AccessAdmin {
		t.Error(`"LOAD_EXTENSION" is not load_extension and was taken for it`)
	}
	if got := classify("SELECT LOAD_EXTENSION('x')"); got != source.AccessAdmin {
		t.Errorf("LOAD_EXTENSION unquoted is load_extension and read as %v", got)
	}
	// And a quoted name that spells it exactly is it: the quotes are what
	// is dropped, not the name.
	if got := classify(`SELECT "load_extension"('x')`); got != source.AccessAdmin {
		t.Errorf(`"load_extension" is load_extension and read as %v`, got)
	}
}
