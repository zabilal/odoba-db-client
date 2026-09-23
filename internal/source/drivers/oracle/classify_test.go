package oracle

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a statement does, before it is sent (NFR-S4, FR-4.9).

func TestWhatAStatementDoes(t *testing.T) {
	cases := []struct {
		sql  string
		want source.Access
	}{
		{"SELECT * FROM people", source.AccessRead},
		{"", source.AccessRead},
		{"-- only a note", source.AccessRead},
		// A statement with no words in it reaches nothing.
		{"1", source.AccessRead},
		{"WITH n AS (SELECT 1 FROM dual) SELECT * FROM n", source.AccessRead},
		{"EXPLAIN PLAN FOR SELECT 1 FROM dual", source.AccessRead},
		{"DESCRIBE people", source.AccessRead},
		{"SET SERVEROUTPUT ON", source.AccessRead},
		{"ALTER SESSION SET TIME_ZONE = '+00:00'", source.AccessRead},
		{"COMMIT", source.AccessRead},
		{"ROLLBACK", source.AccessRead},
		{"SAVEPOINT here", source.AccessRead},
		{"INSERT INTO t VALUES (1)", source.AccessWrite},
		{"UPDATE t SET a = 1 WHERE b = 2", source.AccessWrite},
		{"DELETE FROM t WHERE b = 2", source.AccessWrite},
		{"MERGE INTO t USING u ON (t.a = u.a) WHEN MATCHED THEN UPDATE SET t.b = u.b", source.AccessWrite},
		// TRUNCATE is a definition here: it cannot be rolled back.
		{"TRUNCATE TABLE t", source.AccessDDL},
		{"CREATE TABLE t (a NUMBER)", source.AccessDDL},
		{"ALTER TABLE t ADD b NUMBER", source.AccessDDL},
		{"DROP TABLE t", source.AccessDDL},
		{"RENAME a TO b", source.AccessDDL},
		{"COMMENT ON TABLE t IS 'x'", source.AccessDDL},
		{"PURGE RECYCLEBIN", source.AccessDDL},
		{"GRANT SELECT ON t TO bob", source.AccessAdmin},
		{"REVOKE SELECT ON t FROM bob", source.AccessAdmin},
		{"AUDIT SELECT ON t", source.AccessAdmin},
		{"LOCK TABLE t IN EXCLUSIVE MODE", source.AccessAdmin},
		{"ANALYZE TABLE t COMPUTE STATISTICS", source.AccessAdmin},
		// A block: what it runs cannot be seen from here.
		{"BEGIN wipe_everything; END;", source.AccessAdmin},
		{"DECLARE n NUMBER; BEGIN n := 1; END;", source.AccessAdmin},
		{"CALL p()", source.AccessAdmin},
		// A word this has not been taught is a write.
		{"MAGIC t", source.AccessWrite},
	}
	for _, c := range cases {
		if got := classify(c.sql); got != c.want {
			t.Errorf("classify(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

// ALTER SESSION is this connection's own setting; every other ALTER changes
// something everybody sees.
func TestOnlyASessionsOwnSettingIsARead(t *testing.T) {
	if got := classify("ALTER SESSION SET NLS_DATE_FORMAT = 'DD-MON-RR'"); got != source.AccessRead {
		t.Errorf("a session setting is %v", got)
	}
	for _, sql := range []string{"ALTER SYSTEM FLUSH SHARED_POOL", "ALTER USER bob IDENTIFIED BY x",
		"ALTER TABLE t DROP COLUMN c", "ALTER INDEX i REBUILD"} {
		if got := classify(sql); got != source.AccessDDL {
			t.Errorf("classify(%q) = %v, want a definition", sql, got)
		}
	}
}

func TestAScriptIsWhatItsWorstStatementIs(t *testing.T) {
	cases := []struct {
		sql  string
		want source.Access
	}{
		{"SELECT 1 FROM dual; SELECT 2 FROM dual", source.AccessRead},
		{"SELECT 1 FROM dual; DELETE FROM t WHERE a = 1", source.AccessWrite},
		{"SELECT 1 FROM dual; DROP TABLE t", source.AccessDDL},
		{"DROP TABLE t; SELECT 1 FROM dual", source.AccessDDL},
		{"SELECT 1 FROM dual; GRANT SELECT ON t TO bob", source.AccessAdmin},
	}
	for _, c := range cases {
		if got := classify(c.sql); got != c.want {
			t.Errorf("classify(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

func TestAChangeOverAWholeTableIsNoticed(t *testing.T) {
	for _, sql := range []string{"DELETE FROM t", "UPDATE t SET a = 1", "SELECT 1 FROM dual; DELETE FROM t"} {
		if u := unboundedIn(sql); u == nil {
			t.Errorf("%q was not noticed as unbounded", sql)
		}
	}
	for _, sql := range []string{"DELETE FROM t WHERE a = 1", "UPDATE t SET a = 1 WHERE b = 2", "SELECT 1 FROM dual"} {
		if u := unboundedIn(sql); u != nil {
			t.Errorf("%q was called unbounded: %v", sql, u)
		}
	}
}

func TestTheWordsOfAStatement(t *testing.T) {
	got := words(`SELECT "a b", 'delete' /* drop */ FROM t -- note`)
	want := []string{"select", "from", "t"}
	if len(got) != len(want) {
		t.Fatalf("words are %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("word %d is %q, want %q", i, got[i], want[i])
		}
	}
}

// Which statements answer with rows has to be known before one is sent: a
// count of changed rows is had from the other kind.
func TestWhichStatementsAnswerWithRows(t *testing.T) {
	for _, sql := range []string{"SELECT 1 FROM dual", "select 1 from dual",
		"WITH n AS (SELECT 1 FROM dual) SELECT * FROM n", "  SELECT 1 FROM dual"} {
		if !returnsRows(sql) {
			t.Errorf("%q was sent as a statement with no result", sql)
		}
	}
	for _, sql := range []string{"INSERT INTO t VALUES (1)", "UPDATE t SET a = 1",
		"DELETE FROM t", "CREATE TABLE t (a NUMBER)", "BEGIN NULL; END;", "", "-- a note",
		// A statement that reads to write is still one that writes.
		"INSERT INTO t SELECT 1 FROM dual", "CREATE TABLE t AS SELECT 1 FROM dual"} {
		if returnsRows(sql) {
			t.Errorf("%q was sent as a statement that answers with rows", sql)
		}
	}
}
