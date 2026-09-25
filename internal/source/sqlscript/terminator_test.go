package sqlscript

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

// Firebird's scripts, which say where a statement ends in either of two ways:
// by the shape of a PSQL body, or by changing the terminator with SET TERM.

func fbTexts(script string) []string {
	var out []string
	for _, s := range SplitTerminated(sqllex.Firebird, script, FirebirdBlocks) {
		out = append(out, s.Text)
	}
	return out
}

func TestASemicolonEndsAStatementUntilSomethingSaysOtherwise(t *testing.T) {
	got := fbTexts("SELECT 1 FROM RDB$DATABASE;\nSELECT 2 FROM RDB$DATABASE;\n")
	if len(got) != 2 || got[0] != "SELECT 1 FROM RDB$DATABASE" || got[1] != "SELECT 2 FROM RDB$DATABASE" {
		t.Errorf("it found %q", got)
	}
}

// A body's semicolons are its own, in a script that never changes the
// terminator — which Firebird 3 and later accept from a client that can tell
// where the statement ends.
func TestABodysSemicolonsAreItsOwn(t *testing.T) {
	for name, script := range map[string]string{
		"a procedure": `CREATE PROCEDURE P RETURNS (N INTEGER) AS
			BEGIN N = 1; SUSPEND; N = 2; SUSPEND; END;
			SELECT 1 FROM RDB$DATABASE;`,
		"a trigger": `CREATE TRIGGER T FOR PEOPLE AFTER INSERT AS
			BEGIN INSERT INTO LOG VALUES (1); END;
			SELECT 1 FROM RDB$DATABASE;`,
		"a function": `CREATE FUNCTION F RETURNS INTEGER AS
			BEGIN RETURN 1; END;
			SELECT 1 FROM RDB$DATABASE;`,
		"a package": `CREATE PACKAGE BODY PK AS
			BEGIN SELECT 1 FROM RDB$DATABASE; END;
			SELECT 1 FROM RDB$DATABASE;`,
		"an anonymous block": `EXECUTE BLOCK AS
			BEGIN DELETE FROM T; DELETE FROM U; END;
			SELECT 1 FROM RDB$DATABASE;`,
	} {
		t.Run(name, func(t *testing.T) {
			got := fbTexts(script)
			if len(got) != 2 {
				for i, s := range got {
					t.Logf("  %d: %q", i, s)
				}
				t.Fatalf("it found %d statements, want 2", len(got))
			}
			if !strings.HasPrefix(got[1], "SELECT 1") {
				t.Errorf("the second statement is %q", got[1])
			}
		})
	}
}

// A bare BEGIN opens nothing: Firebird starts a transaction with SET
// TRANSACTION, so a BEGIN with no body-bearing word before it is not a body,
// and a script that treated it as one would swallow everything after it.
func TestABareBeginOpensNothing(t *testing.T) {
	got := fbTexts("BEGIN;\nSELECT 1 FROM RDB$DATABASE;\nCOMMIT;")
	if len(got) != 3 {
		t.Errorf("it found %q", got)
	}
}

// A CASE inside a body does not close the body at its own END.
func TestACaseInsideABodyDoesNotCloseIt(t *testing.T) {
	got := fbTexts(`CREATE PROCEDURE P RETURNS (N INTEGER) AS
		BEGIN N = CASE WHEN 1 = 1 THEN 1 ELSE 2 END; SUSPEND; END;
		SELECT 1 FROM RDB$DATABASE;`)
	if len(got) != 2 {
		for i, s := range got {
			t.Logf("  %d: %q", i, s)
		}
		t.Errorf("it found %d statements, want 2", len(got))
	}
}

func TestSETTERMChangesWhereAStatementEnds(t *testing.T) {
	script := "SET TERM ^ ;\n" +
		"CREATE PROCEDURE P AS BEGIN EXIT; END^\n" +
		"SET TERM ; ^\n" +
		"SELECT 1 FROM RDB$DATABASE;\n"
	got := fbTexts(script)
	if len(got) != 2 {
		for i, s := range got {
			t.Logf("  %d: %q", i, s)
		}
		t.Fatalf("it found %d statements, want 2", len(got))
	}
	if !strings.HasPrefix(got[0], "CREATE PROCEDURE") || strings.Contains(got[0], "SET TERM") {
		t.Errorf("the first statement is %q", got[0])
	}
	if got[1] != "SELECT 1 FROM RDB$DATABASE" {
		t.Errorf("the second statement is %q", got[1])
	}
}

// The directive is isql's and is never sent: a server that was given one
// would refuse it, and the refusal would be about a line the person did not
// think of as a statement.
func TestSETTERMIsNeverSent(t *testing.T) {
	for _, s := range fbTexts("SET TERM ^ ;\nSELECT 1 FROM RDB$DATABASE^\nSET TERM ; ^\n") {
		if strings.Contains(strings.ToUpper(s), "SET TERM") {
			t.Errorf("a directive was sent as a statement: %q", s)
		}
	}
}

func TestTheWaysSETTERMIsWritten(t *testing.T) {
	for name, c := range map[string]struct {
		line, current, want string
		ok                  bool
	}{
		"spaced":             {"SET TERM ^ ;", ";", "^", true},
		"unspaced":           {"SET TERM ^;", ";", "^", true},
		"lower case":         {"set term ^ ;", ";", "^", true},
		"indented":           {"   SET TERM ^ ;   ", ";", "^", true},
		"several characters": {"SET TERM !! ;", ";", "!!", true},
		"back again":         {"SET TERM ; ^", "^", ";", true},
		"with no terminator": {"SET TERM ^", ";", "^", true},
		// No terminator in it at all: taking it as one would set the
		// terminator to nothing, which ends every statement everywhere.
		"naming nothing but itself": {"SET TERM ;", ";", "", false},
		"naming nothing":            {"SET TERM  ", ";", "", false},
		// A directive and a no-op, which is still a directive: the line is
		// dropped rather than sent, because it is not a statement.
		"naming the one in force":      {"SET TERM ; ;", ";", ";", true},
		"a statement, not a directive": {"SELECT 1 FROM RDB$DATABASE;", ";", "", false},
		// The directive is the word TERM, not a word beginning with it.
		"a longer word": {"SET TERMINATOR ^ ;", ";", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := newTerm(c.line, c.current)
			if ok != c.ok {
				t.Fatalf("%q: read as a directive %v, want %v (got %q)", c.line, ok, c.ok, got)
			}
			if ok && got != c.want {
				t.Errorf("%q asks for %q, want %q", c.line, got, c.want)
			}
		})
	}
}

// A SET TERM inside a string or a comment is text.
func TestSETTERMInsideSomethingElseIsText(t *testing.T) {
	script := "/*\nSET TERM ^ ;\n*/\nSELECT 1 FROM RDB$DATABASE;\nSELECT 2 FROM RDB$DATABASE;\n"
	if got := fbTexts(script); len(got) != 2 {
		for i, s := range got {
			t.Logf("  %d: %q", i, s)
		}
		t.Errorf("it found %d statements, want 2", len(got))
	}
}

// Offsets are counted through the whole script, not from the region a
// terminator change began, so an editor underlines the right line.
func TestOffsetsAreCountedThroughTheWholeScript(t *testing.T) {
	script := "SET TERM ^ ;\nSELECT 1 FROM RDB$DATABASE^\nSET TERM ; ^\nSELECT 2 FROM RDB$DATABASE;\n"
	got := SplitTerminated(sqllex.Firebird, script, FirebirdBlocks)
	if len(got) != 2 {
		t.Fatalf("it found %d statements", len(got))
	}
	for _, s := range got {
		at := []rune(script)[s.Offset:]
		if !strings.HasPrefix(string(at), s.Text) {
			t.Errorf("%q is said to be at %d, where the script reads %.20q", s.Text, s.Offset, string(at))
		}
	}
}
