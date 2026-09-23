package sqlserver

import (
	"strings"
	"testing"
)

// Where a T-SQL statement ends (FR-5.4, FR-5.10).

func texts(script string) []string {
	var out []string
	for _, s := range splitScript(script) {
		out = append(out, s.Text)
	}
	return out
}

func equal(t *testing.T, script string, want ...string) {
	t.Helper()
	got := texts(script)
	if len(got) != len(want) {
		t.Fatalf("%q split into %d statements %q, want %d %q", script, len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("statement %d of %q is %q, want %q", i, script, got[i], want[i])
		}
	}
}

// A semicolon ends a statement, as it does everywhere.
func TestASemicolonEndsAStatement(t *testing.T) {
	equal(t, "SELECT 1; SELECT 2", "SELECT 1", "SELECT 2")
	equal(t, "SELECT 1", "SELECT 1")
	equal(t, "", nil...)
	equal(t, "  \n\t ", nil...)
	equal(t, "-- nothing but a note", nil...)
}

// GO is the client's own word for "send what I have written". The server has
// never heard of it, so it is never sent.
func TestGOSeparatesBatches(t *testing.T) {
	equal(t, "SELECT 1\nGO\nSELECT 2", "SELECT 1", "SELECT 2")
	equal(t, "SELECT 1\ngo\nSELECT 2", "SELECT 1", "SELECT 2")
	equal(t, "SELECT 1\n  GO  \nSELECT 2", "SELECT 1", "SELECT 2")
	equal(t, "SELECT 1\nGO", "SELECT 1")
	equal(t, "GO\nSELECT 1", "SELECT 1")
	equal(t, "SELECT 1\nGO\nGO\nSELECT 2", "SELECT 1", "SELECT 2")
}

// The number sqlcmd repeats a batch with is not part of the batch, and is
// not obeyed: a window that ran a write five times over would be hard to
// forgive.
func TestTheRepeatCountAfterGOIsNotObeyed(t *testing.T) {
	equal(t, "DELETE FROM t\nGO 5\nSELECT 1", "DELETE FROM t", "SELECT 1")
	equal(t, "SELECT 1\nGO 2 -- twice\nSELECT 2", "SELECT 1", "SELECT 2")
}

// A word that only looks like the separator is not one.
func TestOnlyGOAloneOnALineSeparates(t *testing.T) {
	equal(t, "SELECT 1\nGO TO\nSELECT 2", "SELECT 1\nGO TO\nSELECT 2")
	equal(t, "SELECT 'go'", "SELECT 'go'")
	equal(t, "SELECT 1 GO", "SELECT 1 GO")
}

// A GO inside a string or a comment is text somebody wrote.
func TestGOInsideAStringOrCommentIsText(t *testing.T) {
	equal(t, "SELECT '\nGO\n'", "SELECT '\nGO\n'")
	equal(t, "/*\nGO\n*/ SELECT 1", "SELECT 1")
}

// A BEGIN … END body's semicolons are its own.
func TestABodyKeepsItsSemicolons(t *testing.T) {
	equal(t, "IF @x = 1 BEGIN SELECT 1; SELECT 2; END; SELECT 3",
		"IF @x = 1 BEGIN SELECT 1; SELECT 2; END", "SELECT 3")
	equal(t, "BEGIN TRY SELECT 1; END TRY BEGIN CATCH SELECT 2; END CATCH; SELECT 3",
		"BEGIN TRY SELECT 1; END TRY BEGIN CATCH SELECT 2; END CATCH", "SELECT 3")
	equal(t, "WHILE @i < 3 BEGIN SET @i = @i + 1; END; SELECT @i",
		"WHILE @i < 3 BEGIN SET @i = @i + 1; END", "SELECT @i")
}

// BEGIN TRANSACTION opens nothing: there is no END to match it, and a
// splitter that waited for one would swallow the rest of the script.
func TestBeginningATransactionOpensNoBody(t *testing.T) {
	equal(t, "BEGIN TRANSACTION; SELECT 1; COMMIT", "BEGIN TRANSACTION", "SELECT 1", "COMMIT")
	equal(t, "BEGIN TRAN; SELECT 1; COMMIT", "BEGIN TRAN", "SELECT 1", "COMMIT")
	equal(t, "BEGIN DISTRIBUTED TRANSACTION; SELECT 1", "BEGIN DISTRIBUTED TRANSACTION", "SELECT 1")
}

// A CASE ends at END too, and closes only itself.
func TestACaseDoesNotCloseABody(t *testing.T) {
	equal(t, "IF @x = 1 BEGIN SELECT CASE WHEN 1 = 1 THEN 'a' END; SELECT 2; END; SELECT 3",
		"IF @x = 1 BEGIN SELECT CASE WHEN 1 = 1 THEN 'a' END; SELECT 2; END", "SELECT 3")
}

// A batch that defines a routine is sent whole: the server requires it to
// hold nothing else, so everything after the name is the definition.
func TestARoutineOwnsItsBatch(t *testing.T) {
	equal(t, "CREATE PROCEDURE p AS SELECT 1; SELECT 2",
		"CREATE PROCEDURE p AS SELECT 1; SELECT 2")
	equal(t, "CREATE OR ALTER PROCEDURE p AS SELECT 1; SELECT 2",
		"CREATE OR ALTER PROCEDURE p AS SELECT 1; SELECT 2")
	equal(t, "ALTER PROCEDURE p AS SELECT 1; SELECT 2", "ALTER PROCEDURE p AS SELECT 1; SELECT 2")
	equal(t, "ALTER FUNCTION f() RETURNS int AS BEGIN RETURN 1; END",
		"ALTER FUNCTION f() RETURNS int AS BEGIN RETURN 1; END")
	equal(t, "CREATE VIEW v AS SELECT 1; SELECT 2", "CREATE VIEW v AS SELECT 1; SELECT 2")
	equal(t, "CREATE SCHEMA s; SELECT 1", "CREATE SCHEMA s; SELECT 1")
	equal(t, "CREATE TRIGGER tr ON t AFTER INSERT AS SELECT 1; SELECT 2",
		"CREATE TRIGGER tr ON t AFTER INSERT AS SELECT 1; SELECT 2")
	// A body of bodies: what the outer BEGIN opened is still open when the
	// inner one has closed, so nothing after it ends the batch either.
	equal(t, "CREATE PROCEDURE p AS BEGIN IF 1 = 1 BEGIN SELECT 1; END; SELECT 2; END",
		"CREATE PROCEDURE p AS BEGIN IF 1 = 1 BEGIN SELECT 1; END; SELECT 2; END")
}

// A routine batch ends where its GO does, which is how a script puts one
// after another.
func TestARoutineBatchEndsAtItsGO(t *testing.T) {
	equal(t, "CREATE PROCEDURE p AS SELECT 1;\nGO\nSELECT 2",
		"CREATE PROCEDURE p AS SELECT 1", "SELECT 2")
}

// Everything else a CREATE makes is a statement like any other.
func TestOtherDefinitionsAreOrdinaryStatements(t *testing.T) {
	equal(t, "CREATE TABLE t (a int); SELECT 1", "CREATE TABLE t (a int)", "SELECT 1")
	equal(t, "CREATE INDEX i ON t (a); SELECT 1", "CREATE INDEX i ON t (a)", "SELECT 1")
	equal(t, "DROP PROCEDURE p; SELECT 1", "DROP PROCEDURE p", "SELECT 1")
	equal(t, "CREATE", "CREATE")
}

// An IF whose arms are single statements ends the first arm with a
// semicolon, and that semicolon ends the arm rather than the IF.
func TestAnElseBelongsToTheIfBeforeIt(t *testing.T) {
	equal(t, "IF @x = 1 SELECT 1; ELSE SELECT 2; SELECT 3",
		"IF @x = 1 SELECT 1; ELSE SELECT 2", "SELECT 3")
	equal(t, "IF @x = 1 SELECT 1; ELSE IF @x = 2 SELECT 2; ELSE SELECT 3",
		"IF @x = 1 SELECT 1; ELSE IF @x = 2 SELECT 2; ELSE SELECT 3")
	// Nothing to glue it to: it is left as it is, and the server says so.
	equal(t, "ELSE SELECT 1", "ELSE SELECT 1")
	// Put back together by where the statements are, which is counted in
	// characters and not in bytes.
	equal(t, "IF @x = 1 SELECT 'é'; ELSE SELECT 2; SELECT 3",
		"IF @x = 1 SELECT 'é'; ELSE SELECT 2", "SELECT 3")
}

// An offset is where the statement starts in the script, counted in
// characters, so an editor can point at it (FR-5.10).
func TestAStatementSaysWhereItStarts(t *testing.T) {
	script := "SELECT 'é';\nGO\n  SELECT 2;\nSELECT 3"
	stmts := splitScript(script)
	if len(stmts) != 3 {
		t.Fatalf("%q split into %q", script, texts(script))
	}
	runes := []rune(script)
	for _, s := range stmts {
		if got := string(runes[s.Offset : s.Offset+len([]rune(s.Text))]); got != s.Text {
			t.Errorf("statement at %d is %q, and the script holds %q there", s.Offset, s.Text, got)
		}
	}
}

// A statement carries no trailing semicolon and no surrounding blank.
func TestAStatementIsJustTheStatement(t *testing.T) {
	for _, s := range splitScript("\n\n  SELECT 1 ;\n\n-- a note\nSELECT 2;\n\n") {
		if strings.TrimSpace(s.Text) != s.Text || strings.HasSuffix(s.Text, ";") {
			t.Errorf("statement %q carries something that is not part of it", s.Text)
		}
	}
}
