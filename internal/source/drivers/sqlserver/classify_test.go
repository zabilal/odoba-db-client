package sqlserver

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
		{";;;", source.AccessRead},
		{"WITH n AS (SELECT 1 AS i) SELECT * FROM n", source.AccessRead},
		{"PRINT 'hello'", source.AccessRead},
		{"DECLARE @i int", source.AccessRead},
		{"SET NOCOUNT ON", source.AccessRead},
		{"USE master", source.AccessRead},
		{"BEGIN TRANSACTION", source.AccessRead},
		{"COMMIT", source.AccessRead},
		{"ROLLBACK", source.AccessRead},
		{"SAVE TRANSACTION here", source.AccessRead},
		{"INSERT INTO t (a) VALUES (1)", source.AccessWrite},
		{"UPDATE t SET a = 1 WHERE b = 2", source.AccessWrite},
		{"DELETE FROM t WHERE b = 2", source.AccessWrite},
		{"MERGE t USING u ON t.a = u.a WHEN MATCHED THEN UPDATE SET t.b = u.b", source.AccessWrite},
		{"TRUNCATE TABLE t", source.AccessWrite},
		{"CREATE TABLE t (a int)", source.AccessDDL},
		{"ALTER TABLE t ADD b int", source.AccessDDL},
		{"DROP TABLE t", source.AccessDDL},
		{"GRANT SELECT ON t TO bob", source.AccessAdmin},
		{"REVOKE SELECT ON t FROM bob", source.AccessAdmin},
		{"DENY SELECT ON t TO bob", source.AccessAdmin},
		{"BACKUP DATABASE shop TO DISK = 'x'", source.AccessAdmin},
		{"RESTORE DATABASE shop FROM DISK = 'x'", source.AccessAdmin},
		{"DBCC CHECKDB", source.AccessAdmin},
		{"KILL 53", source.AccessAdmin},
		{"SHUTDOWN", source.AccessAdmin},
		{"RECONFIGURE", source.AccessAdmin},
		{"CHECKPOINT", source.AccessAdmin},
		// A word this has not been taught is a write: SQL Server has no
		// read-only session to fall back on, so a guess is never a read.
		{"MAGIC t", source.AccessWrite},
	}
	for _, c := range cases {
		if got := classify(c.sql); got != c.want {
			t.Errorf("classify(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

// A CTE can front a write, and SELECT … INTO makes a table.
func TestAReadingWordCanFrontAWrite(t *testing.T) {
	cases := []struct {
		sql  string
		want source.Access
	}{
		{"WITH n AS (SELECT 1 AS i) INSERT INTO t SELECT i FROM n", source.AccessWrite},
		{"WITH n AS (SELECT 1 AS i) UPDATE t SET a = 1 FROM n", source.AccessWrite},
		{"WITH n AS (SELECT 1 AS i) DELETE FROM t WHERE a IN (SELECT i FROM n)", source.AccessWrite},
		{"WITH n AS (SELECT 1 AS i) MERGE t USING n ON 1 = 1 WHEN MATCHED THEN DELETE", source.AccessWrite},
		{"SELECT * INTO copy FROM people", source.AccessDDL},
	}
	for _, c := range cases {
		if got := classify(c.sql); got != c.want {
			t.Errorf("classify(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

// What EXEC runs cannot be seen, so it is taken for the most it could be —
// except where the name is one of the catalogue's own.
func TestExecIsTakenForWhatItCouldBe(t *testing.T) {
	cases := []struct {
		sql  string
		want source.Access
	}{
		{"EXEC sp_who", source.AccessRead},
		{"EXECUTE sp_helptext 'v'", source.AccessRead},
		{"EXEC sp_spaceused", source.AccessRead},
		{"EXEC dbo.wipe_everything", source.AccessAdmin},
		{"EXEC sp_executesql N'DELETE FROM t'", source.AccessAdmin},
		{"EXEC", source.AccessAdmin},
		{"EXEC [sp_who]", source.AccessAdmin},
	}
	for _, c := range cases {
		if got := classify(c.sql); got != c.want {
			t.Errorf("classify(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

// A script is what the worst of its statements is.
func TestAScriptIsWhatItsWorstStatementIs(t *testing.T) {
	cases := []struct {
		sql  string
		want source.Access
	}{
		{"SELECT 1; SELECT 2", source.AccessRead},
		{"SELECT 1; DELETE FROM t WHERE a = 1", source.AccessWrite},
		{"SELECT 1; DROP TABLE t", source.AccessDDL},
		{"DROP TABLE t; SELECT 1", source.AccessDDL},
		{"SELECT 1\nGO\nGRANT SELECT ON t TO bob", source.AccessAdmin},
		// The body of a routine is the routine's, and defining one is DDL
		// whatever the body says.
		{"CREATE PROCEDURE p AS DELETE FROM t", source.AccessDDL},
	}
	for _, c := range cases {
		if got := classify(c.sql); got != c.want {
			t.Errorf("classify(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

// A DELETE or an UPDATE over a whole table is worth asking about (FR-4.9).
func TestAChangeOverAWholeTableIsNoticed(t *testing.T) {
	for _, sql := range []string{"DELETE FROM t", "UPDATE t SET a = 1", "SELECT 1; DELETE FROM t"} {
		if u := unboundedIn(sql); u == nil {
			t.Errorf("%q was not noticed as unbounded", sql)
		}
	}
	for _, sql := range []string{"DELETE FROM t WHERE a = 1", "UPDATE t SET a = 1 WHERE b = 2", "SELECT 1"} {
		if u := unboundedIn(sql); u != nil {
			t.Errorf("%q was called unbounded: %v", sql, u)
		}
	}
}

// The words of a statement are its keywords and plain names, and nothing
// that is inside a string, a comment or brackets. A bracketed name is left
// out because it is a name somebody chose, not a word of the language: EXEC
// [sp_who] is then taken for the most it could be, which is the direction
// this errs in anyway.
func TestTheWordsOfAStatement(t *testing.T) {
	got := words("SELECT [a b], 'delete' /* drop */ FROM t -- note")
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
