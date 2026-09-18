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

func TestACQLBatchIsOneStatement(t *testing.T) {
	script := `INSERT INTO t (a) VALUES (1);
BEGIN BATCH
  INSERT INTO t (a) VALUES (2);
  UPDATE t SET a = 3 WHERE a = 2;
APPLY BATCH;
SELECT * FROM t`
	var got []string
	for _, st := range SplitWith(sqllex.CQL, script, BatchBlocks, BatchCloses) {
		got = append(got, st.Text)
	}
	if len(got) != 3 {
		t.Fatalf("%d statements: %q", len(got), got)
	}
	if !strings.HasPrefix(got[1], "BEGIN BATCH") || !strings.HasSuffix(got[1], "APPLY BATCH") {
		t.Errorf("the batch split as %q", got[1])
	}
	// Unlogged and counter batches are batches too, and a batch nobody began
	// does not close one.
	for _, script := range []string{
		"BEGIN UNLOGGED BATCH INSERT INTO t (a) VALUES (1); APPLY BATCH",
		"BEGIN COUNTER BATCH UPDATE c SET n = n + 1 WHERE k = 1; APPLY BATCH",
	} {
		if got := SplitWith(sqllex.CQL, script, BatchBlocks, BatchCloses); len(got) != 1 {
			t.Errorf("%s split into %d", script, len(got))
		}
	}
	// A batch ends at APPLY BATCH, not at the word batch: a table may be
	// called one, and a batch that closed there would swallow what follows.
	named := SplitWith(sqllex.CQL, "BEGIN BATCH INSERT INTO batch (a) VALUES (1); APPLY BATCH; SELECT * FROM t", BatchBlocks, BatchCloses)
	if len(named) != 2 || !strings.HasSuffix(named[0].Text, "APPLY BATCH") {
		t.Errorf("a batch writing to a table called batch split into %d: %+v", len(named), named)
	}
	// CASE … END is SQL's too: a CQL column may be called case, and counting
	// it would leave the batch open.
	cased := SplitWith(sqllex.CQL, "BEGIN BATCH INSERT INTO t (case) VALUES (1); APPLY BATCH; SELECT * FROM t", BatchBlocks, BatchCloses)
	if len(cased) != 2 {
		t.Errorf("a batch naming a column case split into %d: %+v", len(cased), cased)
	}
	// A body that ends at END is SQL's, not CQL's: a CQL script that says
	// END ends nothing, and its batch still closes at APPLY BATCH.
	one := SplitWith(sqllex.CQL, "BEGIN BATCH INSERT INTO t (a) VALUES (1); APPLY BATCH; SELECT end FROM t", BatchBlocks, BatchCloses)
	if len(one) != 2 {
		t.Errorf("a script naming end split into %d: %+v", len(one), one)
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
