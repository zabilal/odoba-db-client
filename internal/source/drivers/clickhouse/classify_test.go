package clickhouse

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
		{"1", source.AccessRead},
		{"WITH n AS (SELECT 1 AS i) SELECT * FROM n", source.AccessRead},
		{"SHOW TABLES", source.AccessRead},
		{"DESCRIBE TABLE people", source.AccessRead},
		{"DESC people", source.AccessRead},
		{"EXISTS TABLE people", source.AccessRead},
		{"EXPLAIN SELECT 1", source.AccessRead},
		{"CHECK TABLE people", source.AccessRead},
		{"SET max_threads = 4", source.AccessRead},
		{"USE shop", source.AccessRead},
		{"BEGIN TRANSACTION", source.AccessRead},
		{"COMMIT", source.AccessRead},
		{"ROLLBACK", source.AccessRead},
		{"INSERT INTO t VALUES (1)", source.AccessWrite},
		{"DELETE FROM t WHERE a = 1", source.AccessWrite},
		{"UPDATE t SET a = 1 WHERE b = 2", source.AccessWrite},
		{"TRUNCATE TABLE t", source.AccessWrite},
		{"CREATE TABLE t (a UInt8) ENGINE = Memory", source.AccessDDL},
		{"DROP TABLE t", source.AccessDDL},
		{"ATTACH TABLE t", source.AccessDDL},
		{"DETACH TABLE t", source.AccessDDL},
		{"RENAME TABLE a TO b", source.AccessDDL},
		{"EXCHANGE TABLES a AND b", source.AccessDDL},
		{"UNDROP TABLE t", source.AccessDDL},
		{"OPTIMIZE TABLE t FINAL", source.AccessAdmin},
		{"SYSTEM RELOAD DICTIONARIES", source.AccessAdmin},
		{"KILL QUERY WHERE query_id = 'x'", source.AccessAdmin},
		{"GRANT SELECT ON t TO bob", source.AccessAdmin},
		{"REVOKE SELECT ON t FROM bob", source.AccessAdmin},
		{"BACKUP TABLE t TO Disk('d', 'x')", source.AccessAdmin},
		{"RESTORE TABLE t FROM Disk('d', 'x')", source.AccessAdmin},
		// A word this has not been taught is a write.
		{"MAGIC t", source.AccessWrite},
	}
	for _, c := range cases {
		if got := classify(c.sql); got != c.want {
			t.Errorf("classify(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

// ALTER TABLE … UPDATE and … DELETE are how ClickHouse changed rows before
// it had the statements for it: they are writes wearing a definition's
// clothes.
func TestAnAlterThatChangesRowsIsAWrite(t *testing.T) {
	cases := []struct {
		sql  string
		want source.Access
	}{
		{"ALTER TABLE t UPDATE a = 1 WHERE b = 2", source.AccessWrite},
		{"ALTER TABLE t DELETE WHERE b = 2", source.AccessWrite},
		{"ALTER TABLE t ADD COLUMN c UInt8", source.AccessDDL},
		{"ALTER TABLE t MODIFY COLUMN c String", source.AccessDDL},
		{"ALTER TABLE t DROP COLUMN c", source.AccessDDL},
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
		{"SELECT 1; INSERT INTO t VALUES (1)", source.AccessWrite},
		{"SELECT 1; DROP TABLE t", source.AccessDDL},
		{"DROP TABLE t; SELECT 1", source.AccessDDL},
		{"SELECT 1; GRANT SELECT ON t TO bob", source.AccessAdmin},
	}
	for _, c := range cases {
		if got := classify(c.sql); got != c.want {
			t.Errorf("classify(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

// A change over a whole table is worth asking about (FR-4.9).
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
// inside a string, a comment or a quoted name.
func TestTheWordsOfAStatement(t *testing.T) {
	got := words("SELECT `a b`, 'delete' /* drop */ FROM t -- note")
	want := []string{"select", "from", "t"}
	if len(got) != len(want) {
		t.Fatalf("words are %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("word %d is %q, want %q", i, got[i], want[i])
		}
	}
	// A name in double quotes is a name here too, not a string somebody can
	// hide a word in.
	if got := words(`SELECT "delete" FROM t`); len(got) != 3 || got[1] != "from" {
		t.Errorf("words are %q", got)
	}
}
