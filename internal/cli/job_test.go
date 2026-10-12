package cli

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/job"
	"github.com/ikigai-db/ikigai-db/internal/transfer"
)

// A command is written down with --save-job and not run; and run, it does what
// the command would have, from wherever it is run.
func TestAnExportIsSavedAsAJobAndRunAgain(t *testing.T) {
	path := db(t)
	work := t.TempDir()
	jobs := filepath.Join(work, "jobs")
	if err := os.Mkdir(jobs, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(work)
	job := filepath.Join("jobs", "items.json")
	status, out, errOut := run(t, "export", "--driver", "sqlite", "--database", path, "--table", "main.items",
		"--where", "id < 3", "--limit", "5", "--format", "json", "--header=false", "--out", "items.json", "--save-job", job)
	if status != OK || out != "" {
		t.Fatalf("status %d; it wrote %q and said %s", status, out, errOut)
	}
	if !strings.Contains(errOut, "was not run") || !strings.Contains(errOut, "ikigai run "+job) {
		t.Errorf("it said %q, which does not say what happened or what to do next", errOut)
	}
	if _, err := os.Stat("items.json"); err == nil {
		t.Fatal("saving a job ran it")
	}
	raw, err := os.ReadFile(job)
	if err != nil {
		t.Fatal(err)
	}
	// The flags by their names, in the alphabet's order, each as what it is,
	// and the file it writes as a path from the job's own directory.
	want := `{
  "ikigai_job": 1,
  "command": "export",
  "flags": {
    "database": ` + quoted(path) + `,
    "driver": "sqlite",
    "format": "json",
    "header": false,
    "limit": 5,
    "out": "../items.json",
    "table": "main.items",
    "where": "id < 3"
  }
}
`
	if string(raw) != want {
		t.Fatalf("the job:\n%s\nfor\n%s", raw, want)
	}

	// Run from somewhere else: the file goes where the job says, not where
	// the run was started.
	t.Chdir(t.TempDir())
	status, _, errOut = run(t, "run", filepath.Join(work, job))
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	got, err := os.ReadFile(filepath.Join(work, "items.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"name":"one"`) || !strings.Contains(string(got), `"name":"two"`) || strings.Contains(string(got), "three") {
		t.Errorf("the job wrote %s", got)
	}
	// What is said after the job wins over the job.
	other := filepath.Join(t.TempDir(), "other.csv")
	status, _, errOut = run(t, "run", filepath.Join(work, job), "--format", "csv", "--out", other, "--where", "id = 3")
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if got, _ := os.ReadFile(other); string(got) != "3,three,3\n" {
		t.Errorf("the job, told otherwise, wrote %q", got)
	}
}

func quoted(s string) string { return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"` }

func TestAnImportIsSavedAsAJobAndRunAgain(t *testing.T) {
	path := db(t)
	work := t.TempDir()
	t.Chdir(work)
	if err := os.WriteFile("more.csv", []byte("id,name,price\n4,four,4.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, _, errOut := run(t, "import", "--driver", "sqlite", "--database", path, "--into", "main.items",
		"--from", "more.csv", "--keys", "id", "--batch", "100", "--confirm", "--save-job", "load.json")
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	// Consent is not written down, and that it was not is said.
	if !strings.Contains(errOut, "--confirm is not kept in a job") {
		t.Errorf("it said %q", errOut)
	}
	raw, _ := os.ReadFile("load.json")
	if strings.Contains(string(raw), "confirm") || !strings.Contains(string(raw), `"from": "more.csv"`) ||
		!strings.Contains(string(raw), `"batch": 100`) || !strings.Contains(string(raw), `"keys": "id"`) {
		t.Fatalf("the job: %s", raw)
	}
	t.Chdir(t.TempDir())
	for range 2 { // again is the point: by its key, the second run changes nothing
		status, _, errOut = run(t, "run", filepath.Join(work, "load.json"))
		if status != OK {
			t.Fatalf("status %d; it said %s", status, errOut)
		}
	}
	status, out, _ := run(t, "query", "--driver", "sqlite", "--database", path, "--sql", "SELECT count(*), max(name) FROM items WHERE id = 4", "--header=false")
	if status != OK || out != "1,four\n" {
		t.Errorf("status %d; the table holds %q", status, out)
	}
	// A dry run of a job is the job's flags and one more.
	status, _, errOut = run(t, "run", filepath.Join(work, "load.json"), "--dry-run")
	if status != OK || !strings.Contains(errOut, "would go into") {
		t.Errorf("status %d; it said %s", status, errOut)
	}
}

func TestAJobNeverHoldsAConnectionString(t *testing.T) {
	path := db(t)
	job := filepath.Join(t.TempDir(), "job.json")
	status, _, errOut := run(t, "export", "--url", "sqlite://"+path, "--table", "main.items", "--save-job", job)
	if status != Usage || !strings.Contains(errOut, "--url is not kept in a job") || !strings.Contains(errOut, "--connection") {
		t.Errorf("status %d; it said %s", status, errOut)
	}
	if _, err := os.Stat(job); err == nil {
		t.Error("a job that was refused was written")
	}
	// And a job with none is given one when it is run.
	status, _, errOut = run(t, "export", "--table", "main.items", "--save-job", job)
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	status, out, errOut := run(t, "run", job, "--driver", "sqlite", "--database", path)
	if status != OK || !strings.Contains(out, "1,one,1.5") {
		t.Errorf("status %d; it wrote %q and said %s", status, out, errOut)
	}
	// A job that cannot be written is a failure, and is said.
	if status, _, errOut := run(t, "export", "--table", "main.items", "--save-job", filepath.Join(filepath.Dir(job), "none", "job.json")); status != Failed || strings.Contains(errOut, "was not run") {
		t.Errorf("a job with nowhere to go: status %d; it said %s", status, errOut)
	}
	// Only an export and an import can be saved.
	status, _, errOut = run(t, "query", "--driver", "sqlite", "--database", path, "--sql", "SELECT 1", "--save-job", job)
	if status != Usage || !strings.Contains(errOut, "save-job") {
		t.Errorf("a query saved as a job: status %d; it said %s", status, errOut)
	}
}

func TestAJobThatIsNotOneIsRefused(t *testing.T) {
	path := db(t)
	dir := t.TempDir()
	for name, c := range map[string]struct{ body, want string }{
		"empty":    {``, "is not a job"},
		"text":     {`SELECT 1`, "is not a job"},
		"unmarked": {`{"command": "export", "flags": {}}`, `it has no "ikigai_job"`},
		"newer":    {`{"ikigai_job": 2, "command": "export", "flags": {}}`, "a job of version 2, and this reads version 1"},
		"verb":     {`{"ikigai_job": 1, "command": "deploy", "flags": {}}`, `a job is an export or an import, not "deploy"`},
		"key":      {`{"ikigai_job": 1, "command": "export", "flag": {}}`, `unknown field "flag"`},
		"flag":     {`{"ikigai_job": 1, "command": "export", "flags": {"tabel": "main.items"}}`, `export has no flag called "tabel"`},
		"url":      {`{"ikigai_job": 1, "command": "export", "flags": {"url": "sqlite://x"}}`, "a job does not say --url"},
		"consent":  {`{"ikigai_job": 1, "command": "import", "flags": {"confirm": true}}`, "a job does not say --confirm"},
		"nested":   {`{"ikigai_job": 1, "command": "export", "flags": {"save-job": "again.json"}}`, "a job does not say --save-job"},
		"list":     {`{"ikigai_job": 1, "command": "export", "flags": {"table": [["a"]]}}`, `"table": a flag's value is text, a number, or true or false`},
		"member":   {`{"ikigai_job": 1, "command": "export", "flags": {"table": ["a", null]}}`, `"table": a flag's value is text`},
		"nothing":  {`{"ikigai_job": 1, "command": "export", "flags": {"table": null}}`, `"table": a flag's value is text`},
		"count":    {`{"ikigai_job": 1, "command": "export", "flags": {"limit": "many"}}`, `"limit": `},
	} {
		job := filepath.Join(dir, name+".json")
		if err := os.WriteFile(job, []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		status, out, errOut := run(t, "run", job, "--driver", "sqlite", "--database", path)
		if status != Usage || out != "" || !strings.Contains(errOut, c.want) || !strings.Contains(errOut, name+".json") {
			t.Errorf("%s: status %d; it wrote %q and said %s", name, status, out, errOut)
		}
	}
	for _, args := range [][]string{{"run"}, {"run", "--out", "x"}} {
		if status, _, errOut := run(t, args...); status != Usage || !strings.Contains(errOut, "run needs a job") {
			t.Errorf("%v: status %d; it said %s", args, status, errOut)
		}
	}
	if status, _, errOut := run(t, "run", filepath.Join(dir, "gone.json")); status != Usage || !strings.Contains(errOut, "gone.json") {
		t.Errorf("a job that is not there: status %d; it said %s", status, errOut)
	}
	// A value in the file as JSON has it is the value: a number, a yes or no.
	typed := filepath.Join(dir, "typed.json")
	body := `{"ikigai_job": 1, "command": "export", "flags": {"table": "main.items", "limit": 1, "header": true, "driver": "sqlite", "database": ` + quoted(path) + `}}`
	if err := os.WriteFile(typed, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, out, errOut := run(t, "run", typed); status != OK || out != "id,name,price\n1,one,1.5\n" {
		t.Errorf("status %d; it wrote %q and said %s", status, out, errOut)
	}
	// A file a job names by where it is, is there, and not under the job.
	whole := filepath.Join(t.TempDir(), "whole.csv")
	body = `{"ikigai_job": 1, "command": "export", "flags": {"table": "main.items", "limit": 1, "out": ` + quoted(filepath.ToSlash(whole)) + `}}`
	abs := filepath.Join(dir, "abs.json")
	if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, _, errOut := run(t, "run", abs, "--driver", "sqlite", "--database", path); status != OK {
		t.Errorf("status %d; it said %s", status, errOut)
	}
	if got, _ := os.ReadFile(whole); string(got) != "id,name,price\n1,one,1.5\n" {
		t.Errorf("the file a job names by its whole path holds %q", got)
	}
	if status, _, errOut := run(t, "run", typed, "stray"); status != Usage || !strings.Contains(errOut, `"stray" is not a flag`) {
		t.Errorf("a word after the job: status %d; it said %s", status, errOut)
	}
	if status, _, errOut := run(t, "run", typed, "--save-job", "again.json"); status != Usage || !strings.Contains(errOut, "save-job") {
		t.Errorf("a job saved from a job: status %d; it said %s", status, errOut)
	}
	for _, help := range []string{"-h", "--help"} {
		if status, _, errOut := run(t, "run", help); status != OK || !strings.Contains(errOut, "ikigai run nightly.json") {
			t.Errorf("%s: status %d; it said %s", help, status, errOut)
		}
	}
	if status, _, errOut := run(t, "help"); status != OK || !strings.Contains(errOut, "run an export or an import that was saved as a job") {
		t.Errorf("the list of commands: %s", errOut)
	}
}

// What the window can be told about a file, a command line can be told too:
// how its text is written, what parts its lines, and which of its columns
// fills which of the table's.
func TestImportIsToldHowToReadAndWhereToPut(t *testing.T) {
	path := db(t)
	dir := t.TempDir()
	// Semicolons, Windows-1252, and names that are nothing like the table's.
	file := filepath.Join(dir, "odd.txt")
	if err := os.WriteFile(file, []byte("Nr;Bezeichnung;Preis;a=b\n7;Caf\xe9;7.5;x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"import", "--driver", "sqlite", "--database", path, "--into", "main.items", "--from", file,
		"--format", "csv", "--delimiter", "semicolon", "--encoding", "windows-1252",
		"--map", "Nr=id", "--map", " Bezeichnung = name ", "--map", "Preis=price"}
	status, _, errOut := run(t, args...)
	if status != OK || !strings.Contains(errOut, "left out, having no column of main.items to go in: a=b") {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	status, out, _ := run(t, "query", "--driver", "sqlite", "--database", path, "--sql", "SELECT name, price FROM items WHERE id = 7", "--header=false")
	if status != OK || out != "Café,7.5\n" {
		t.Errorf("the row went in as %q", out)
	}
	// Told how the text is written, it is read that way and not as it looks:
	// these bytes are good UTF-8, and Windows-1252 all the same.
	if err := os.WriteFile(file, []byte("Nr;Bezeichnung\n70;Caf\xc3\xa9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status, _, errOut := run(t, "import", "--driver", "sqlite", "--database", path, "--into", "main.items", "--from", file,
		"--format", "csv", "--delimiter", "semicolon", "--encoding", "Windows-1252", "--map", "Nr=id", "--map", "Bezeichnung=name"); status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if _, out, _ := run(t, "query", "--driver", "sqlite", "--database", path, "--sql", "SELECT name FROM items WHERE id = 70", "--header=false"); out != "Caf\u00c3\u00a9\n" {
		t.Errorf("the row went in as %q", out)
	}
	if err := os.WriteFile(file, []byte("Nr;Bezeichnung;Preis;a=b\n7;Caf\xe9;7.5;x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The same, kept as a job: a flag said three times is a list of three.
	job := filepath.Join(dir, "odd.json")
	if status, _, errOut := run(t, append(args, "--keys", "id", "--save-job", job)...); status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	raw, _ := os.ReadFile(job)
	for _, want := range []string{`"delimiter": "semicolon"`, `"encoding": "windows-1252"`, "\"map\": [\n      \"Nr=id\",\n      \" Bezeichnung = name \",\n      \"Preis=price\"\n    ]"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the job does not say %s:\n%s", want, raw)
		}
	}
	if status, _, errOut := run(t, "run", job); status != OK || !strings.Contains(errOut, "1 row into main.items") {
		t.Errorf("status %d; it said %s", status, errOut)
	}
	// A delimiter by itself, and a name with an equals sign in it.
	if err := os.WriteFile(file, []byte("a=b~n\n8~eight\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	status, _, errOut = run(t, "import", "--driver", "sqlite", "--database", path, "--into", "main.items", "--from", file,
		"--format", "csv", "--delimiter", "~", "--map", "a=b=id", "--map", "n=name")
	if status != OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if _, out, _ := run(t, "query", "--driver", "sqlite", "--database", path, "--sql", "SELECT name FROM items WHERE id = 8", "--header=false"); out != "eight\n" {
		t.Errorf("the row went in as %q", out)
	}
	// What cannot be acted on is refused before a row is read, by name.
	for name, c := range map[string]struct {
		flags []string
		want  string
	}{
		"encoding":  {[]string{"--encoding", "latin-9"}, `"latin-9" is not an encoding this reads: utf-16be, utf-16le, utf-8, windows-1252`},
		"delimiter": {[]string{"--delimiter", "::"}, `"::" is not a delimiter`},
		"quote":     {[]string{"--delimiter", `"`}, "is not a delimiter"},
		"newline":   {[]string{"--delimiter", "\n"}, "is not a delimiter"},
		"no equals": {[]string{"--delimiter", "~", "--map", "n"}, `--map "n": a pair is "file column=table column"`},
		"no file":   {[]string{"--delimiter", "~", "--map", "m=name"}, `the file has no column called "m"; it has a=b, n`},
		"no table":  {[]string{"--delimiter", "~", "--map", "n=nom"}, `the table has no column called "nom"; it has id, name, price`},
		"twice":     {[]string{"--delimiter", "~", "--map", "n=name", "--map", "a=b=name"}, "name is filled twice"},
	} {
		status, _, errOut := run(t, append([]string{"import", "--driver", "sqlite", "--database", path, "--into", "main.items",
			"--from", file, "--format", "csv"}, c.flags...)...)
		if status != Usage || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: status %d; it said %s", name, status, errOut)
		}
	}
	// Named delimiters are the characters they name.
	for name, want := range map[string]rune{"comma": ',', "TAB": '\t', " pipe ": '|', "semicolon": ';', "x": 'x', "é": 'é'} {
		if got, err := delimiterOf(name); err != nil || got != want {
			t.Errorf("%q is %q, not %q (%v)", name, want, got, err)
		}
	}
}

// The window writes a job in the words internal/job has for an import. They
// are this command's flags or they are nothing, so each is looked for here,
// and a job written that way is run.
func TestAJobTheWindowWritesIsOneThisRuns(t *testing.T) {
	path := db(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "odd.txt")
	if err := os.WriteFile(file, []byte("Nr;Bezeichnung\n2;zwei\n9;neun\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	importCommand().setup(fs)
	// Every flag an import can be written with, between these.
	for _, imp := range []job.Import{
		{Connection: "c", Into: []string{"main", "items"}, From: file, Columns: []string{"Nr", "Bezeichnung"},
			Read:  transfer.Options{Format: transfer.CSV, Header: true, Comma: ';', Encoding: transfer.Windows1252},
			Pairs: []transfer.Pair{{From: 0, To: "id"}, {From: 1, To: "name"}},
			Load:  transfer.LoadOptions{Keys: []string{"id"}, Batch: 10, OnError: "collect", MaxErrors: 3}},
		{Connection: "c", Into: []string{"main", "items"}, From: file, Read: transfer.Options{Format: transfer.XLSX, Sheet: "s"},
			Load: transfer.LoadOptions{Replace: true}},
	} {
		flags, err := imp.Flags()
		if err != nil {
			t.Fatal(err)
		}
		for name := range flags {
			if fs.Lookup(name) == nil {
				t.Errorf("a job says %q, and import has no such flag", name)
			}
		}
		if imp.Read.Format != transfer.CSV {
			continue
		}
		// The connection is the one thing a test has to say otherwise: a
		// saved connection is in a person's settings, and this has none.
		delete(flags, "connection")
		saved := filepath.Join(dir, "window.json")
		if err := job.Write(saved, "import", flags); err != nil {
			t.Fatal(err)
		}
		status, _, errOut := run(t, "run", saved, "--driver", "sqlite", "--database", path)
		if status != OK || !strings.Contains(errOut, "2 rows into main.items") {
			t.Fatalf("status %d; it said %s", status, errOut)
		}
	}
	status, out, _ := run(t, "query", "--driver", "sqlite", "--database", path, "--sql", "SELECT id, name FROM items ORDER BY id", "--header=false")
	if status != OK || out != "1,one\n2,zwei\n3,three\n9,neun\n" {
		t.Errorf("the table holds %q", out)
	}
}
