package sqlscript

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/sqllex"
)

func texts(d *sqllex.Dialect, script string, b Blocks) []string {
	var out []string
	for _, s := range Split(d, script, b) {
		out = append(out, s.Text)
	}
	return out
}

func TestSQLiteTriggerBodiesAreOneStatement(t *testing.T) {
	script := `CREATE TABLE t(a);
CREATE TRIGGER tr AFTER INSERT ON t BEGIN
  UPDATE t SET a = CASE WHEN a > 1 THEN 1 ELSE 0 END;
  DELETE FROM t WHERE a IS NULL;
END;
INSERT INTO t VALUES (1)`
	got := texts(sqllex.SQLite, script, TriggerBlocks)
	if len(got) != 3 {
		t.Fatalf("%d statements: %q", len(got), got)
	}
	if want := "CREATE TRIGGER tr AFTER INSERT ON t BEGIN\n  UPDATE t SET a = CASE WHEN a > 1 THEN 1 ELSE 0 END;\n  DELETE FROM t WHERE a IS NULL;\nEND"; got[1] != want {
		t.Errorf("trigger split as %q", got[1])
	}
}

func TestTempTriggersAndBareBegin(t *testing.T) {
	got := texts(sqllex.SQLite, "BEGIN; CREATE TEMP TRIGGER x BEFORE DELETE ON t BEGIN SELECT 1; END; COMMIT", TriggerBlocks)
	want := []string{"BEGIN", "CREATE TEMP TRIGGER x BEFORE DELETE ON t BEGIN SELECT 1; END", "COMMIT"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%q", got)
	}
}

func TestWithoutBlocksEverySemicolonSplits(t *testing.T) {
	got := texts(sqllex.SQLite, "CREATE TRIGGER x AFTER INSERT ON t BEGIN SELECT 1; END", nil)
	if len(got) != 2 {
		t.Errorf("%q", got)
	}
}

func TestOffsetsAreCharactersAndCommentsAreDropped(t *testing.T) {
	stmts := Split(sqllex.SQLite, "SELECT 'é';\n-- only a comment;\nSELECT 2", nil)
	if len(stmts) != 2 || stmts[1].Text != "SELECT 2" || stmts[1].Offset != len([]rune("SELECT 'é';\n-- only a comment;\n")) {
		t.Errorf("%+v", stmts)
	}
}

func TestDelimiterDirectivesKeepRoutineBodiesWhole(t *testing.T) {
	script := "CREATE TABLE t (a INT);\nDELIMITER $$\nCREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\n  SELECT '$$';\nEND $$\nDELIMITER ;\nCALL p();"
	got := SplitDelimited(sqllex.MySQL, script)
	var texts []string
	for _, s := range got {
		texts = append(texts, s.Text)
	}
	want := []string{"CREATE TABLE t (a INT)", "CREATE PROCEDURE p()\nBEGIN\n  SELECT 1;\n  SELECT '$$';\nEND", "CALL p()"}
	if !reflect.DeepEqual(texts, want) {
		t.Fatalf("%q", texts)
	}
	if !strings.HasPrefix(script[runeToByte(script, got[1].Offset):], "CREATE PROCEDURE") ||
		!strings.HasPrefix(script[runeToByte(script, got[2].Offset):], "CALL") {
		t.Errorf("offsets %d, %d do not point at their statements", got[1].Offset, got[2].Offset)
	}
}

func TestADelimiterLineInsideACommentIsText(t *testing.T) {
	got := SplitDelimited(sqllex.MySQL, "/* note:\nDELIMITER $$\n*/ SELECT 1; SELECT 2")
	if len(got) != 2 {
		t.Errorf("%+v", got)
	}
}

func runeToByte(s string, runes int) int {
	for i := range s {
		if runes == 0 {
			return i
		}
		runes--
	}
	return len(s)
}
