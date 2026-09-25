package firebird

import (
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// What a statement is allowed to do (NFR-S4, FR-4.9). The rule the contract
// sets is that anything not confidently read-only is mutating, so the cases
// that matter most here are the ones where a wrong answer would let a write
// through a read-only connection.

func TestWhatAStatementIsAllowedToDo(t *testing.T) {
	for name, c := range map[string]struct {
		sql  string
		want source.Access
	}{
		"a read":                     {`SELECT * FROM PEOPLE`, source.AccessRead},
		"a read with a CTE":          {`WITH n AS (SELECT 1 FROM RDB$DATABASE) SELECT * FROM n`, source.AccessRead},
		"a CTE that writes":          {`WITH n AS (SELECT 1 FROM RDB$DATABASE) INSERT INTO T SELECT * FROM n`, source.AccessWrite},
		"a select naming a delete":   {`SELECT * FROM T WHERE (DELETE FROM T)`, source.AccessWrite},
		"an insert":                  {`INSERT INTO T VALUES (1)`, source.AccessWrite},
		"an update":                  {`UPDATE T SET A = 1`, source.AccessWrite},
		"a delete":                   {`DELETE FROM T WHERE A = 1`, source.AccessWrite},
		"a merge":                    {`MERGE INTO T USING S ON T.A = S.A WHEN MATCHED THEN UPDATE SET T.B = S.B`, source.AccessWrite},
		"an update or insert":        {`UPDATE OR INSERT INTO T (A) VALUES (1) MATCHING (A)`, source.AccessWrite},
		"a procedure":                {`EXECUTE PROCEDURE P`, source.AccessWrite},
		"a block":                    {`EXECUTE BLOCK AS BEGIN DELETE FROM T; END`, source.AccessWrite},
		"a statement of text":        {`EXECUTE STATEMENT 'DROP TABLE T'`, source.AccessWrite},
		"a table made":               {`CREATE TABLE T (A INTEGER)`, source.AccessDDL},
		"a table altered":            {`ALTER TABLE T ADD B INTEGER`, source.AccessDDL},
		"a table dropped":            {`DROP TABLE T`, source.AccessDDL},
		"a view remade":              {`RECREATE VIEW V AS SELECT 1 FROM RDB$DATABASE`, source.AccessDDL},
		"a comment written":          {`COMMENT ON TABLE T IS 'x'`, source.AccessDDL},
		"an exception declared":      {`DECLARE EXTERNAL FUNCTION F RETURNS INTEGER`, source.AccessDDL},
		"a grant":                    {`GRANT SELECT ON T TO PUBLIC`, source.AccessAdmin},
		"a revoke":                   {`REVOKE SELECT ON T FROM PUBLIC`, source.AccessAdmin},
		"a transaction begun":        {`SET TRANSACTION READ COMMITTED`, source.AccessRead},
		"statistics set":             {`SET STATISTICS INDEX I`, source.AccessRead},
		"a generator moved":          {`SET GENERATOR G TO 100`, source.AccessWrite},
		"a commit":                   {`COMMIT`, source.AccessRead},
		"a rollback":                 {`ROLLBACK`, source.AccessRead},
		"a savepoint":                {`SAVEPOINT S`, source.AccessRead},
		"nothing at all":             {``, source.AccessRead},
		"a comment and nothing else": {`-- just a note`, source.AccessRead},
		"something unrecognised":     {`FLOOB THE WIDGET`, source.AccessWrite},
		"the worst of a script":      {`SELECT 1 FROM RDB$DATABASE; DROP TABLE T; SELECT 2 FROM RDB$DATABASE`, source.AccessDDL},
	} {
		t.Run(name, func(t *testing.T) {
			if got := classify(c.sql); got != c.want {
				t.Errorf("%q is %v, want %v", c.sql, got, c.want)
			}
		})
	}
}

// A statement that would change every row says so before it runs (FR-4.9).
func TestAChangeWithNoConditionIsNoticed(t *testing.T) {
	for name, c := range map[string]struct {
		sql       string
		unbounded bool
	}{
		"a delete of everything":  {`DELETE FROM PEOPLE`, true},
		"an update of everything": {`UPDATE PEOPLE SET NAME = 'x'`, true},
		"a delete of some":        {`DELETE FROM PEOPLE WHERE ID = 1`, false},
		"an update of some":       {`UPDATE PEOPLE SET NAME = 'x' WHERE ID = 1`, false},
		"one of each in a script": {`DELETE FROM A WHERE ID = 1; DELETE FROM B`, true},
		"a read":                  {`SELECT * FROM PEOPLE`, false},
		"a delete inside a body": {
			`CREATE TRIGGER T FOR PEOPLE AFTER INSERT AS BEGIN DELETE FROM OTHER WHERE ID = 1; END`, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := unboundedIn(c.sql) != nil; got != c.unbounded {
				t.Errorf("%q: unbounded %v, want %v", c.sql, got, c.unbounded)
			}
		})
	}
}

// A script is split where its statements end, whichever way it says so.
func TestAScriptIsSplitWhereItsStatementsEnd(t *testing.T) {
	for name, c := range map[string]struct {
		script string
		want   int
	}{
		"plain statements": {`SELECT 1 FROM RDB$DATABASE; SELECT 2 FROM RDB$DATABASE`, 2},
		"a body's semicolons are its own": {
			"CREATE PROCEDURE P RETURNS (N INTEGER) AS BEGIN N = 1; SUSPEND; END;\nSELECT 1 FROM RDB$DATABASE", 2},
		"a terminator of its own": {
			"SET TERM ^ ;\nCREATE PROCEDURE P AS BEGIN EXIT; END^\nSET TERM ; ^\nSELECT 1 FROM RDB$DATABASE;", 2},
		"a comment alone is nothing": {"-- a note\n", 0},
	} {
		t.Run(name, func(t *testing.T) {
			got := d.SplitScript(c.script)
			if len(got) != c.want {
				for i, st := range got {
					t.Logf("  %d: %q", i, st.Text)
				}
				t.Errorf("it found %d statements, want %d", len(got), c.want)
			}
		})
	}
}
