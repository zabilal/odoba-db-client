package cockroach

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Classification is the first of three defences (NFR-S4). The rule is that
// anything not confidently read-only is reported as mutating: a false
// positive costs a confirmation, a false negative is a write to a
// connection somebody marked read-only.
func TestWhatAStatementDoes(t *testing.T) {
	for _, c := range []struct {
		sql  string
		want source.Access
	}{
		{"SELECT 1", source.AccessRead},
		{"select * from t where a = 'delete from t'", source.AccessRead},
		{"-- DROP TABLE t\nSELECT 1", source.AccessRead},
		// A comment inside a statement is not part of it either, which
		// the splitter cannot answer because the statement holds it.
		{"SELECT /* DROP TABLE t */ 1", source.AccessRead},
		{"SELECT 1 -- DELETE FROM t", source.AccessRead},
		// A block comment that opens on one line and closes on another:
		// what is inside it is a comment only if the lexer's state
		// carries from line to line.
		{"SELECT 1 /* a\nDELETE FROM t\nb */", source.AccessRead},
		{"SELECT '\n DELETE FROM t\n'", source.AccessRead},
		// A statement that is nothing but a comment is not something the
		// server runs, and not something to warn anybody about.
		{"/* just a comment */", source.AccessRead},
		{"SELECT 1;\n/* just a comment */", source.AccessRead},
		{"-- just a comment", source.AccessRead},
		{"VALUES (1)", source.AccessRead},
		{"TABLE t", source.AccessRead},
		{"WITH x AS (SELECT 1) SELECT * FROM x", source.AccessRead},
		{"SHOW TABLES", source.AccessRead},
		{"SHOW CREATE TABLE t", source.AccessRead},
		{"BEGIN", source.AccessRead},
		{"COMMIT", source.AccessRead},
		{"ROLLBACK", source.AccessRead},
		{"SAVEPOINT s", source.AccessRead},
		{"RELEASE SAVEPOINT s", source.AccessRead},
		{"SET search_path = public", source.AccessRead},
		{"EXPLAIN SELECT 1", source.AccessRead},
		{`SELECT "delete" FROM t`, source.AccessRead},

		{"INSERT INTO t VALUES (1)", source.AccessWrite},
		{"UPDATE t SET a = 1 WHERE b = 2", source.AccessWrite},
		{"DELETE FROM t WHERE a = 1", source.AccessWrite},
		// UPSERT is CockroachDB's own, and writes.
		{"UPSERT INTO t VALUES (1)", source.AccessWrite},
		{"UPSERT INTO t (a) SELECT a FROM u", source.AccessWrite},
		{"COPY t FROM STDIN", source.AccessWrite},
		{"CALL p()", source.AccessWrite},
		// A data-modifying CTE is a write wearing a read's clothes, and
		// the INTO of an INSERT inside one is not a SELECT INTO.
		{"WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d", source.AccessWrite},
		{"WITH d AS (INSERT INTO t VALUES (1) RETURNING *) SELECT * FROM d", source.AccessWrite},
		{"WITH d AS (UPSERT INTO t VALUES (1) RETURNING *) SELECT * FROM d", source.AccessWrite},
		{"EXPLAIN ANALYZE UPSERT INTO t VALUES (1)", source.AccessWrite},
		{"SELECT * FROM t FOR UPDATE", source.AccessWrite},
		{"SELECT * FROM t FOR SHARE", source.AccessWrite},
		{"SELECT nextval('s')", source.AccessWrite},
		{"SELECT setval('s', 1)", source.AccessWrite},
		// Two statements in one string really do both run.
		{"SELECT 1; DROP TABLE t", source.AccessDDL},
		// A word this classifier has not been taught is not confidently a read.
		{"FLIBBERTIGIBBET t", source.AccessWrite},
		// Nothing but a quoted name is not something the server can run.
		{`"t"`, source.AccessWrite},
		{"", source.AccessRead},

		{"CREATE TABLE t (a INT8)", source.AccessDDL},
		{"ALTER TABLE t ADD COLUMN b INT8", source.AccessDDL},
		{"DROP TABLE t", source.AccessDDL},
		{"TRUNCATE t", source.AccessDDL},
		{"COMMENT ON TABLE t IS 'x'", source.AccessDDL},
		{"GRANT SELECT ON t TO u", source.AccessDDL},
		{"REVOKE SELECT ON t FROM u", source.AccessDDL},
		{"IMPORT INTO t CSV DATA ('x')", source.AccessDDL},
		{"REFRESH MATERIALIZED VIEW v", source.AccessDDL},
		{"RESTORE FROM 'x'", source.AccessDDL},
		{"CREATE STATISTICS s FROM t", source.AccessDDL},
		{"SELECT * INTO u FROM t", source.AccessDDL},

		// set_config turns read-only mode off without ever issuing a SET.
		{"SELECT set_config('default_transaction_read_only', 'off', false)", source.AccessAdmin},
		{`SELECT "set_config"('x', 'y', false)`, source.AccessAdmin},
		{"SELECT crdb_internal.force_error('XX000', 'x')", source.AccessAdmin},
		{"SET default_transaction_read_only = off", source.AccessAdmin},
		{"SET TRANSACTION READ WRITE", source.AccessAdmin},
		{"SET ROLE admin", source.AccessAdmin},
		{"SET CLUSTER SETTING sql.defaults.distsql = 'off'", source.AccessAdmin},
		{"RESET ALL", source.AccessAdmin},
		{"BEGIN TRANSACTION READ WRITE", source.AccessAdmin},
		// USE changes which database every unqualified name after it means.
		{"USE other", source.AccessAdmin},
		{"CANCEL QUERY 'x'", source.AccessAdmin},
		{"PAUSE JOB 1", source.AccessAdmin},
		{"RESUME JOB 1", source.AccessAdmin},
		{"BACKUP INTO 'x'", source.AccessAdmin},
		{"EXPORT INTO CSV 'x' FROM SELECT * FROM t", source.AccessAdmin},
		{"ANALYZE t", source.AccessAdmin},

		// EXPLAIN ANALYZE runs the statement for real, so it is exactly as
		// dangerous as what it explains.
		{"EXPLAIN ANALYZE SELECT 1", source.AccessRead},
		{"EXPLAIN ANALYZE DELETE FROM t", source.AccessWrite},
		{"EXPLAIN ANALYZE CREATE TABLE t (a INT8)", source.AccessDDL},
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

// A script is cut at the semicolons between statements, and not at the
// ones inside a routine's body.
func TestAScriptIsCutBetweenStatements(t *testing.T) {
	got := splitScript("SELECT 1; SELECT 2")
	if len(got) != 2 {
		t.Fatalf("cut into %d: %+v", len(got), got)
	}
	body := `CREATE FUNCTION f() RETURNS INT8 LANGUAGE SQL BEGIN ATOMIC SELECT 1; SELECT 2; END;
SELECT 3`
	got = splitScript(body)
	if len(got) != 2 {
		t.Fatalf("a routine's body was cut open: %d statements %+v", len(got), got)
	}
	// A semicolon inside a string or a quoted name ends nothing.
	if got := splitScript(`SELECT ';'`); len(got) != 1 {
		t.Errorf("a semicolon in a string cut the script into %d", len(got))
	}
	if got := splitScript(`SELECT "a;b" FROM t`); len(got) != 1 {
		t.Errorf("a semicolon in a name cut the script into %d", len(got))
	}
}

// A name is read case-insensitively when it was written bare, and exactly
// when it was quoted: "SET_CONFIG" names a different function.
func TestAQuotedNameIsItsOwnName(t *testing.T) {
	if got := classify(`SELECT "SET_CONFIG"('a','b',false)`); got == source.AccessAdmin {
		t.Error(`"SET_CONFIG" is not set_config and was taken for it`)
	}
	if got := classify("SELECT SET_CONFIG('a','b',false)"); got != source.AccessAdmin {
		t.Errorf("SET_CONFIG unquoted is set_config and read as %v", got)
	}
}
