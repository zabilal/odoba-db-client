package oracle

import "testing"

// Where a statement ends in Oracle's SQL (FR-5.4, FR-5.10).

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
			t.Errorf("statement %d of %q is\n%q\nwant\n%q", i, script, got[i], want[i])
		}
	}
}

// A semicolon ends a statement, as it does everywhere.
func TestASemicolonEndsAStatement(t *testing.T) {
	equal(t, "SELECT 1 FROM dual; SELECT 2 FROM dual", "SELECT 1 FROM dual", "SELECT 2 FROM dual")
	equal(t, "SELECT 1 FROM dual", "SELECT 1 FROM dual")
	equal(t, "", nil...)
	equal(t, "-- only a note", nil...)
	equal(t, "SELECT ';' FROM dual", "SELECT ';' FROM dual")
	equal(t, `SELECT "a;b" FROM dual`, `SELECT "a;b" FROM dual`)
}

// A PL/SQL block's semicolons are the block's own, and a slash on a line of
// its own is the client's word for "send what I have written".
func TestABlockKeepsItsSemicolons(t *testing.T) {
	equal(t, "BEGIN NULL; NULL; END;\n/\nSELECT 1 FROM dual",
		"BEGIN NULL; NULL; END;", "SELECT 1 FROM dual")
	equal(t, "DECLARE n NUMBER; BEGIN n := 1; END;\n/\nSELECT 1 FROM dual",
		"DECLARE n NUMBER; BEGIN n := 1; END;", "SELECT 1 FROM dual")
	// A block nested in a block closes only itself.
	equal(t, "BEGIN BEGIN NULL; END; NULL; END;\n/", "BEGIN BEGIN NULL; END; NULL; END;")
}

// A routine's body is a block, and the AS or IS before it is what says so.
func TestARoutinesBodyIsABlock(t *testing.T) {
	equal(t, "CREATE PROCEDURE p AS BEGIN NULL; END;\n/\nSELECT 1 FROM dual",
		"CREATE PROCEDURE p AS BEGIN NULL; END;", "SELECT 1 FROM dual")
	equal(t, "CREATE FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END;\n/",
		"CREATE FUNCTION f RETURN NUMBER IS BEGIN RETURN 1; END;")
	equal(t, "CREATE TRIGGER t BEFORE INSERT ON x FOR EACH ROW BEGIN NULL; END;\n/",
		"CREATE TRIGGER t BEFORE INSERT ON x FOR EACH ROW BEGIN NULL; END;")
}

// A view's AS is followed by a query, not a body, so its statement ends at
// the semicolon like any other.
func TestAViewsAsIsNotABody(t *testing.T) {
	equal(t, "CREATE VIEW v AS SELECT 1 FROM dual; SELECT 2 FROM dual",
		"CREATE VIEW v AS SELECT 1 FROM dual", "SELECT 2 FROM dual")
	equal(t, "CREATE TABLE t AS SELECT 1 FROM dual; SELECT 2 FROM dual",
		"CREATE TABLE t AS SELECT 1 FROM dual", "SELECT 2 FROM dual")
}

// A slash inside a string or a comment is text somebody wrote, and a slash
// that shares its line with anything else is not the separator.
func TestOnlyASlashAloneOnALineSeparates(t *testing.T) {
	equal(t, "SELECT '\n/\n' FROM dual", "SELECT '\n/\n' FROM dual")
	equal(t, "SELECT 1/2 FROM dual; SELECT 3 FROM dual",
		"SELECT 1/2 FROM dual", "SELECT 3 FROM dual")
	equal(t, "/* \n/\n */ SELECT 1 FROM dual", "SELECT 1 FROM dual")
}

// The slash is the client's and is never sent.
func TestTheSlashIsNotPartOfTheStatement(t *testing.T) {
	for _, script := range []string{"BEGIN NULL; END;\n/", "BEGIN NULL; END;\n/\n", "BEGIN NULL; END;\n  /  "} {
		equal(t, script, "BEGIN NULL; END;")
	}
}

// An offset is where the statement starts in the script, counted in
// characters, so an editor can point at it (FR-5.10).
func TestAStatementSaysWhereItStarts(t *testing.T) {
	script := "SELECT 'é' FROM dual;\nBEGIN NULL; END;\n/\nSELECT 2 FROM dual"
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
