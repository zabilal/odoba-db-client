package cli

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/sqlite"
)

// The command line, against a database (FR-16.1).
//
// Against SQLite, because a file is a database: every verb below runs for real —
// a real connection, a real schema read, real rows written to a real file — with
// no server and nothing to skip. What a test here cannot reach is what SQLite
// cannot do, and there is exactly one of those: it renders no DDL, so deploying
// to it is refused, which is itself asserted below. Deploying for real is an
// end-to-end test against PostgreSQL (internal/e2e).
//
// Every test reads what a pipeline would read: the two streams and the status.

// db makes a database file with three rows in it, and answers its path.
func db(t *testing.T, extra ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	conn, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stmts := append([]string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL, price REAL)`,
		`INSERT INTO items VALUES (1, 'one', 1.5), (2, 'two', 2.5), (3, 'three', 3.0)`,
	}, extra...)
	for _, s := range stmts {
		if _, err := conn.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return path
}

// run runs the command line as a pipeline would, and answers what it would see.
func run(t *testing.T, args ...string) (status int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	status = Run(context.Background(), args, &o, &e)
	return status, o.String(), e.String()
}

// on is the flags that name a database file, which every verb below takes.
func on(path string) []string { return []string{"--driver", "sqlite", "--database", path} }

func TestAQueryWritesItsRows(t *testing.T) {
	status, out, errOut := run(t, append([]string{"query"}, append(on(db(t)),
		"--sql", "SELECT id, name FROM items ORDER BY id")...)...)
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	want := "id,name\n1,one\n2,two\n3,three\n"
	if out != want {
		t.Errorf("it wrote\n%q\nwant\n%q", out, want)
	}
	// The count goes to the other stream: a pipeline sending rows into a file
	// must not find "3 rows" among them.
	if !strings.Contains(errOut, "3 rows") {
		t.Errorf("it said %q", errOut)
	}
}

func TestTheRowsGoOutInTheFormatAskedFor(t *testing.T) {
	path := db(t)
	for name, c := range map[string]struct {
		format string
		says   string
	}{
		"json":   {"json", `{"id":1,"name":"one"`},
		"ndjson": {"ndjson", `{"id":1,"name":"one"`},
		"tsv":    {"tsv", "1\tone"},
		"md":     {"md", "| id |"},
		"xml":    {"xml", "<row>"},
		"html":   {"html", "<table"},
	} {
		t.Run(name, func(t *testing.T) {
			status, out, errOut := run(t, append([]string{"query", "--format", c.format},
				append(on(path), "--sql", "SELECT id, name FROM items ORDER BY id")...)...)
			if status != OK {
				t.Fatalf("status %d; it said %s", status, errOut)
			}
			if !strings.Contains(out, c.says) {
				t.Errorf("it wrote\n%s\nwhich does not hold %q", out, c.says)
			}
		})
	}
}

// A table's rows can be written as INSERT statements, in the source's own
// dialect. A query's cannot: they may be two tables joined, or none, so the
// statement has no table to name.
func TestInsertStatementsNeedATable(t *testing.T) {
	path := db(t)
	status, out, errOut := run(t, append([]string{"export", "--table", "main.items",
		"--format", "sql"}, on(path)...)...)
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if !strings.Contains(out, "INSERT INTO") {
		t.Errorf("it wrote\n%s", out)
	}
	status, _, errOut = run(t, append([]string{"query", "--format", "sql"},
		append(on(path), "--sql", "SELECT 1")...)...)
	if status != Usage {
		t.Errorf("status %d", status)
	}
	if !strings.Contains(errOut, "--table") {
		t.Errorf("it said %q, which does not say what would answer it", errOut)
	}
}

// A format nobody has, and a workbook with nowhere to go, are refused before
// anything is opened: the status is the one that means "the arguments".
func TestAFormatThisCannotWriteIsRefused(t *testing.T) {
	path := db(t)
	status, _, errOut := run(t, append([]string{"query", "--format", "parquet"},
		append(on(path), "--sql", "SELECT 1")...)...)
	if status != Usage {
		t.Errorf("status %d", status)
	}
	if !strings.Contains(errOut, "parquet") || !strings.Contains(errOut, "csv") {
		t.Errorf("it said %q, which does not say what is wrong or what is taken", errOut)
	}
	status, _, errOut = run(t, append([]string{"query", "--format", "xlsx"},
		append(on(path), "--sql", "SELECT 1")...)...)
	if status != Usage {
		t.Errorf("a workbook to standard output: status %d", status)
	}
	if !strings.Contains(errOut, "--out") {
		t.Errorf("it said %q, which does not say which flag would answer it", errOut)
	}
}

// A file is written under another name and moved into place at the end, so a
// pipeline reading the output of a run that failed finds nothing rather than
// half of something.
func TestAFileIsWrittenOrNotWrittenAtAll(t *testing.T) {
	path := db(t)
	out := filepath.Join(t.TempDir(), "rows.csv")
	status, _, errOut := run(t, append([]string{"query", "--out", out},
		append(on(path), "--sql", "SELECT id FROM items ORDER BY id")...)...)
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "id\n1\n2\n3\n" {
		t.Errorf("the file holds %q", got)
	}
	if _, err := os.Stat(out + ".part"); err == nil {
		t.Error("the half-written name is still there")
	}

	failed := filepath.Join(t.TempDir(), "none.csv")
	status, _, errOut = run(t, append([]string{"query", "--out", failed},
		append(on(path), "--sql", "SELECT * FROM nothing_of_the_sort")...)...)
	if status != Failed {
		t.Errorf("status %d; it said %s", status, errOut)
	}
	for _, name := range []string{failed, failed + ".part"} {
		if _, err := os.Stat(name); err == nil {
			t.Errorf("%s was left behind by a run that failed", filepath.Base(name))
		}
	}
}

func TestAStatementThatChangesRowsSaysHowMany(t *testing.T) {
	path := db(t)
	status, out, errOut := run(t, append([]string{"query"}, append(on(path),
		"--sql", "UPDATE items SET price = 9 WHERE id = 1")...)...)
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if out != "" {
		t.Errorf("it wrote %q where there were no rows to write", out)
	}
	if !strings.Contains(errOut, "1 row changed") {
		t.Errorf("it said %q", errOut)
	}
}

// Read-only is the connection's own guard, in the data layer (NFR-S4), which is
// why this asserts the row as well as the refusal: a refusal printed by
// something that had already written would be no refusal.
func TestReadOnlyRefusesToChangeAnything(t *testing.T) {
	path := db(t)
	status, _, errOut := run(t, append([]string{"query", "--read-only"},
		append(on(path), "--sql", "DELETE FROM items")...)...)
	if status != Failed {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if !strings.Contains(errOut, "read-only") {
		t.Errorf("it said %q", errOut)
	}
	if _, out, _ := run(t, append([]string{"query"}, append(on(path),
		"--sql", "SELECT count(*) AS n FROM items")...)...); !strings.Contains(out, "3") {
		t.Errorf("the rows are now %q", out)
	}
}

func TestExportWritesAWholeTable(t *testing.T) {
	path := db(t)
	status, out, errOut := run(t, append([]string{"export", "--table", "main.items"}, on(path)...)...)
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if n := strings.Count(strings.TrimSpace(out), "\n"); n != 3 {
		t.Errorf("it wrote %d lines after the header:\n%s", n, out)
	}
	// A condition and a count, which is what a pipeline sampling a table asks
	// for. Both are the browse's own, not a filter written here.
	status, out, _ = run(t, append([]string{"export", "--table", "main.items",
		"--where", "price > 2"}, on(path)...)...)
	if status != OK || strings.Contains(out, "one") {
		t.Errorf("status %d; it wrote\n%s", status, out)
	}
	status, out, _ = run(t, append([]string{"export", "--table", "main.items",
		"--limit", "1"}, on(path)...)...)
	if status != OK || strings.Count(strings.TrimSpace(out), "\n") != 1 {
		t.Errorf("status %d; it wrote\n%s", status, out)
	}
}

func TestImportPairsColumnsByNameAndSaysWhatItLeftOut(t *testing.T) {
	path := db(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "more.csv")
	// "Name" is the same column as name, and "colour" is a column the table
	// has not got: one is paired, the other is named as left out.
	if err := os.WriteFile(file, []byte("id,Name,price,colour\n4,four,4.5,red\n5,five,5.5,blue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, _, errOut := run(t, append([]string{"import", "--into", "main.items", "--from", file},
		on(path)...)...)
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	left := lineWith(errOut, "left out")
	if !strings.Contains(left, "colour") {
		t.Errorf("it said %q, which does not name the column it left out", errOut)
	}
	// And names only that one: a line that listed every column would be a line
	// nobody could read for the one that matters.
	for _, paired := range []string{"id", "name", "price"} {
		if strings.Contains(left, paired) {
			t.Errorf("%q is named as left out, and it was paired: %q", paired, left)
		}
	}
	if !strings.Contains(errOut, "2 rows into") {
		t.Errorf("it said %q", errOut)
	}
	_, out, _ := run(t, append([]string{"query"}, append(on(path),
		"--sql", "SELECT name FROM items WHERE id = 4")...)...)
	if !strings.Contains(out, "four") {
		t.Errorf("the row did not arrive: %q", out)
	}
}

// A dry run reads the file and makes every value the table's, which is where a
// load fails if it is going to, and writes nothing at all.
func TestADryRunWritesNothing(t *testing.T) {
	path := db(t)
	file := filepath.Join(t.TempDir(), "bad.csv")
	if err := os.WriteFile(file, []byte("id,name,price\n4,four,4.5\n5,five,not a number\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, _, errOut := run(t, append([]string{"import", "--into", "main.items",
		"--from", file, "--dry-run"}, on(path)...)...)
	if status != Differs {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if !strings.Contains(errOut, "row 2") || !strings.Contains(errOut, "price") {
		t.Errorf("it said %q, which does not say which row and which column", errOut)
	}
	if _, out, _ := run(t, append([]string{"query"}, append(on(path),
		"--sql", "SELECT count(*) AS n FROM items")...)...); !strings.Contains(out, "3") {
		t.Errorf("a dry run wrote something: %q", out)
	}
}

func TestDiffSaysWhatDiffers(t *testing.T) {
	a := db(t)
	b := db(t, `CREATE TABLE extra (k TEXT)`, `ALTER TABLE items DROP COLUMN price`)
	status, out, errOut := run(t, append([]string{"diff"}, append(on(a),
		"--to-driver", "sqlite", "--to-database", b)...)...)
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	for _, want := range []string{"+ table extra", "- column price"} {
		if !strings.Contains(out, want) {
			t.Errorf("it wrote\n%s\nwhich does not hold %q", out, want)
		}
	}
	if !strings.Contains(errOut, "1 added, 1 removed") {
		t.Errorf("it said %q", errOut)
	}
	// A difference is not a failure unless a pipeline asked for it to be one.
	status, _, _ = run(t, append([]string{"diff", "--exit-code"}, append(on(a),
		"--to-driver", "sqlite", "--to-database", b)...)...)
	if status != Differs {
		t.Errorf("with --exit-code the status is %d", status)
	}
	status, _, _ = run(t, append([]string{"diff", "--exit-code"}, append(on(a),
		"--to-driver", "sqlite", "--to-database", a)...)...)
	if status != OK {
		t.Errorf("a database compared with itself: status %d", status)
	}
}

// Save and diff are two halves of the same claim, so they are tested as one: a
// model written from a database differs from that database in nothing.
//
// This is also what proves the schema was read at all. A source with one
// database has no node above its class folders, and reading the children of a
// database reference it never makes answers nothing — which compares equal to
// everything. An empty model and a wrong comparison both look like success.
func TestAModelWrittenFromADatabaseMatchesIt(t *testing.T) {
	path := db(t)
	dir := filepath.Join(t.TempDir(), "model")
	status, _, errOut := run(t, append([]string{"save", "--model", dir}, on(path)...)...)
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if _, err := os.Stat(filepath.Join(dir, "database.json")); err != nil {
		t.Fatalf("no model was written: %v", err)
	}
	status, out, errOut := run(t, append([]string{"diff", "--exit-code", "--model", dir}, on(path)...)...)
	if status != OK {
		t.Fatalf("a model differs from the database it was written from: status %d\n%s%s",
			status, out, errOut)
	}
	// And it found something: a comparison of two empty models is also equal.
	if !strings.Contains(errOut, "the same") {
		t.Errorf("it said %q", errOut)
	}
	counts := strings.TrimSpace(errOut)
	if strings.Contains(counts, "0 the same") {
		t.Errorf("it compared nothing: %q", counts)
	}
	// A model of a different database does differ, which is the other half.
	other := db(t, `CREATE TABLE extra (k TEXT)`)
	status, _, _ = run(t, append([]string{"diff", "--exit-code", "--model", dir}, on(other)...)...)
	if status != Differs {
		t.Errorf("a database with a table the model lacks: status %d", status)
	}
}

// SQLite renders no DDL, so a deploy to it cannot be written and says so. This
// is the documented limit (docs/KNOWN-DEFECTS.md) held as a test, so that the
// day SQLite grows a DDL generator, this fails and the limit is removed.
func TestDeployingWhereNothingRendersDDLIsRefused(t *testing.T) {
	path := db(t)
	dir := filepath.Join(t.TempDir(), "model")
	if status, _, errOut := run(t, append([]string{"save", "--model", dir}, on(path)...)...); status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	other := db(t, `CREATE TABLE extra (k TEXT)`)
	status, _, errOut := run(t, append([]string{"deploy", "--model", dir}, on(other)...)...)
	if status != Failed {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if !strings.Contains(errOut, "statements") {
		t.Errorf("it said %q, which does not say what it cannot do", errOut)
	}
}

func TestWhatTheArgumentsHaveToSay(t *testing.T) {
	path := db(t)
	for name, c := range map[string]struct {
		args []string
		says string
	}{
		"no command":           {nil, "Usage"},
		"a command nobody has": {[]string{"nonsense"}, "not a command"},
		"no database":          {[]string{"query", "--sql", "SELECT 1"}, "--url"},
		"two databases": {append([]string{"query", "--sql", "SELECT 1", "--url",
			"postgres://localhost/x"}, on(path)...), "once"},
		"nothing to run": {append([]string{"query"}, on(path)...), "--sql"},
		"two things to run": {append([]string{"query", "--sql", "SELECT 1", "--file", "q.sql"},
			on(path)...), "once"},
		"an empty script":    {append([]string{"query", "--sql", "  "}, on(path)...), "nothing to run"},
		"nothing to export":  {append([]string{"export"}, on(path)...), "--table"},
		"nothing to import":  {append([]string{"import"}, on(path)...), "--into"},
		"nothing to compare": {append([]string{"diff"}, on(path)...), "--model"},
		"nowhere to save":    {append([]string{"save"}, on(path)...), "--model"},
		// In deploy's own words: falling through to the comparison's refusal
		// would offer --to-url, which deploy has not got.
		"no model to deploy":     {append([]string{"deploy"}, on(path)...), "saved model"},
		"a condition on a query": {append([]string{"export", "--sql", "SELECT 1", "--where", "x"}, on(path)...), "--where"},
		"a table and a query": {append([]string{"export", "--table", "main.items",
			"--sql", "SELECT 1"}, on(path)...), "once"},
		"a driver nobody has": {[]string{"query", "--driver", "nonsense",
			"--database", path, "--sql", "SELECT 1"}, "nonsense"},
	} {
		t.Run(name, func(t *testing.T) {
			status, _, errOut := run(t, c.args...)
			if status != Usage {
				t.Errorf("status %d; it said %s", status, errOut)
			}
			if !strings.Contains(errOut, c.says) {
				t.Errorf("it said %q, which does not mention %q", errOut, c.says)
			}
		})
	}
}

func TestItSaysWhatItIsAndWhatItDoes(t *testing.T) {
	status, out, _ := run(t, "--version")
	if status != OK || !strings.Contains(out, "ikigai") {
		t.Errorf("status %d; it wrote %q", status, out)
	}
	status, _, errOut := run(t, "--help")
	if status != OK {
		t.Errorf("status %d", status)
	}
	// Every verb is in the help, written out here rather than asked of the
	// code: a list that came from commands() would agree with it whatever it
	// held, including a verb that had gone missing from both.
	for _, name := range []string{"query", "export", "import", "save", "diff", "deploy"} {
		if !strings.Contains(errOut, name) {
			t.Errorf("the help does not mention %q:\n%s", name, errOut)
		}
		if _, ok := commands()[name]; !ok {
			t.Errorf("%q is in the help above and is not a command", name)
		}
	}
	if n := len(commands()); n != 6 {
		t.Errorf("there are %d commands, and six are named here", n)
	}
	for _, want := range []string{"IKIGAI_PASSWORD", "Exit status"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("the help does not mention %q", want)
		}
	}
}

// Nothing says a password, anywhere, whatever happens (NFR-S2). A connection
// string is exactly where one is, so a failure to connect must not repeat it.
func TestNothingSaysAPassword(t *testing.T) {
	const password = "hunter2-not-a-real-password"
	status, out, errOut := run(t, "query", "--url",
		"postgres://someone:"+password+"@127.0.0.1:1/nothing?sslmode=disable",
		"--sql", "SELECT 1")
	if status != Failed {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	for stream, text := range map[string]string{"what it wrote": out, "what it said": errOut} {
		if strings.Contains(text, password) {
			t.Errorf("%s holds the password:\n%s", stream, text)
		}
	}
	// And the same for a connection string that cannot be read at all, which is
	// the case that tempts an error into quoting its input.
	_, out, errOut = run(t, "query", "--url", "postgres://someone:"+password+"@ho st/db",
		"--sql", "SELECT 1")
	for stream, text := range map[string]string{"what it wrote": out, "what it said": errOut} {
		if strings.Contains(text, password) {
			t.Errorf("a malformed URL: %s holds the password:\n%s", stream, text)
		}
	}
}

// A connection string in the environment is how a pipeline passes one: a flag
// is visible in every process list on the machine.
func TestTheEnvironmentCanSayWhichDatabase(t *testing.T) {
	path := db(t)
	t.Setenv("IKIGAI_URL", "sqlite://"+path)
	status, _, errOut := run(t, "query", "--sql", "SELECT 1 AS n")
	// SQLite claims no URL scheme — a file is chosen, not dialled — so this is
	// refused, and the refusal says so rather than saying nothing.
	if status != Failed {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if !strings.Contains(errOut, "sqlite") {
		t.Errorf("it said %q", errOut)
	}
}

// The mechanism behind a file written whole: nothing is at the name until the
// end, and a run that fails leaves nothing at all.
//
// Tested here rather than through a verb because a write that fails halfway
// cannot be arranged from outside — and what matters is not that a query failed,
// it is that a reader watching the name sees a whole file or no file.
func TestNothingIsAtTheNameUntilItIsWhole(t *testing.T) {
	dir := t.TempDir()
	e := &env{out: new(bytes.Buffer), err: new(bytes.Buffer)}
	for name, ok := range map[string]bool{"it worked": true, "it did not": false} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".csv")
			r := &rowsOut{out: path}
			w, done, err := r.writer(e)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("half a file")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err == nil {
				t.Error("it is at its name while it is still being written")
			}
			if err := done(ok); err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(path)
			if ok && err != nil {
				t.Errorf("a file that was written is not there: %v", err)
			}
			if !ok && err == nil {
				t.Error("a file that was not written is there")
			}
			if _, err := os.Stat(path + ".part"); err == nil {
				t.Error("the half-written name is still there")
			}
		})
	}
}

// A source that does not say how many rows a statement changed says so, rather
// than reporting nothing — which reads as none.
func TestAStatementNobodyCountedSaysSo(t *testing.T) {
	if got := affected(-1); strings.Contains(got, "0") || !strings.Contains(got, "does not say") {
		t.Errorf("it says %q", got)
	}
	if got := affected(0); got != "0 rows changed" {
		t.Errorf("it says %q", got)
	}
	if got := affected(1); got != "1 row changed" {
		t.Errorf("it says %q", got)
	}
}

// What is the same is listed when it is asked for, and not otherwise: a
// comparison of two large schemas is unreadable if everything identical is in it,
// and unprovable if it cannot be.
func TestWhatIsTheSameIsListedWhenAskedFor(t *testing.T) {
	a := db(t)
	b := db(t, `CREATE TABLE extra (k TEXT)`)
	status, out, _ := run(t, append([]string{"diff"}, append(on(a),
		"--to-driver", "sqlite", "--to-database", b)...)...)
	if status != OK {
		t.Fatalf("status %d", status)
	}
	if strings.Contains(out, "  table items") {
		t.Errorf("a table that is the same is listed:\n%s", out)
	}
	status, out, _ = run(t, append([]string{"diff", "--same"}, append(on(a),
		"--to-driver", "sqlite", "--to-database", b)...)...)
	if status != OK {
		t.Fatalf("status %d", status)
	}
	if !strings.Contains(out, "table items") {
		t.Errorf("asked for, it does not list what is the same:\n%s", out)
	}
}

// What a file is read as is detected from the file itself, and a flag overrides
// the detection.
//
// The detection is good — it reads tab-separated rows in a file named .csv as
// what they are — so the flag is proved the other way round, by obeying it into a
// worse answer: told that a comma-separated file is tab-separated, this reads one
// column per line and nothing pairs. A flag that was ignored would load the rows
// and pass.
func TestTheFormatFlagOverridesWhatWasDetected(t *testing.T) {
	path := db(t)
	file := filepath.Join(t.TempDir(), "commas.csv")
	if err := os.WriteFile(file, []byte("id,name,price\n4,four,4.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, _, errOut := run(t, append([]string{"import", "--into", "main.items", "--from", file},
		on(path)...)...)
	if status != OK {
		t.Fatalf("read as what it is: status %d; it said %s", status, errOut)
	}
	status, _, errOut = run(t, append([]string{"import", "--into", "main.items", "--from", file,
		"--format", "tsv"}, on(path)...)...)
	if status != Failed {
		t.Fatalf("told it is something else: status %d; it said %s", status, errOut)
	}
	if !strings.Contains(errOut, "none of the file's columns") {
		t.Errorf("it said %q", errOut)
	}
	// And a format nobody reads is refused rather than detected around.
	status, _, errOut = run(t, append([]string{"import", "--into", "main.items", "--from", file,
		"--format", "parquet"}, on(path)...)...)
	if status != Usage || !strings.Contains(errOut, "parquet") {
		t.Errorf("status %d; it said %s", status, errOut)
	}
}

// lineWith is the line of a message that holds a phrase, so that a test can read
// one line of several rather than the lot.
func lineWith(text, phrase string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, phrase) {
			return line
		}
	}
	return ""
}
