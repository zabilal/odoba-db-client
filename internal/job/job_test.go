package job

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/transfer"
)

func TestAnImportIsWrittenAsTheFlagsThatDoIt(t *testing.T) {
	base := Import{Connection: "shop", Into: []string{"main", "items"}, From: "people.csv",
		Columns: []string{"Nr", "Name", "spare"}, Pairs: []transfer.Pair{{From: 1, To: "name"}, {From: 0, To: "id"}}}
	for name, c := range map[string]struct {
		change func(*Import)
		want   map[string]any
	}{
		"delimited": {func(i *Import) {
			i.Read = transfer.Options{Format: transfer.CSV, Header: true, Comma: ';', Encoding: transfer.Windows1252}
		}, map[string]any{"format": "csv", "header": true, "delimiter": "semicolon", "encoding": "windows-1252"}},
		"its own delimiter": {func(i *Import) { i.Read = transfer.Options{Format: transfer.TSV} },
			map[string]any{"format": "tsv", "header": false, "encoding": "utf-8"}},
		"an odd delimiter": {func(i *Import) {
			i.Read = transfer.Options{Format: transfer.CSV, Comma: '~', Encoding: transfer.UTF16LE}
		},
			map[string]any{"format": "csv", "header": false, "delimiter": "~", "encoding": "utf-16le"}},
		"a workbook": {func(i *Import) {
			i.Read = transfer.Options{Format: transfer.XLSX, Header: true, Sheet: "Q3", Comma: ','}
		},
			map[string]any{"format": "xlsx", "header": true, "sheet": "Q3"}},
		"its first sheet": {func(i *Import) { i.Read = transfer.Options{Format: transfer.XLSX} },
			map[string]any{"format": "xlsx", "header": false}},
		"records": {func(i *Import) {
			i.Read = transfer.Options{Format: transfer.NDJSON, Header: true, Comma: ',', Sheet: "x"}
		},
			map[string]any{"format": "ndjson", "encoding": "utf-8"}},
		"a table file": {func(i *Import) { i.Read = transfer.Options{Format: transfer.DBF, Encoding: transfer.Windows1252} },
			map[string]any{"format": "dbf", "encoding": "windows-1252"}},
		"parquet": {func(i *Import) {
			i.Read = transfer.Options{Format: transfer.Parquet, Encoding: transfer.UTF16BE, Header: true}
		},
			map[string]any{"format": "parquet"}},
		"replacing": {func(i *Import) {
			i.Read.Format, i.Load = transfer.JSON, transfer.LoadOptions{Replace: true, Batch: 50, OnError: "skip", MaxErrors: 9}
		}, map[string]any{"format": "json", "encoding": "utf-8", "replace": true, "batch": 50, "on-error": "skip"}},
		"by key": {func(i *Import) {
			i.Read.Format, i.Load = transfer.YAML, transfer.LoadOptions{Keys: []string{"id", "name"}, OnError: "collect", MaxErrors: 9}
		}, map[string]any{"format": "yaml", "encoding": "utf-8", "keys": "id,name", "on-error": "collect", "max-errors": 9}},
	} {
		imp := base
		c.change(&imp)
		got, err := imp.Flags()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		// What every import says, and what this one does.
		c.want["connection"], c.want["into"], c.want["from"] = "shop", "main.items", "people.csv"
		c.want["map"] = []string{"Name=name", "Nr=id"}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n%v\nfor\n%v", name, got, c.want)
		}
	}
	for name, c := range map[string]struct {
		change func(*Import)
		want   string
	}{
		"format":     {func(i *Import) { i.Read.Format = transfer.Format(99) }, "cannot say the format"},
		"encoding":   {func(i *Import) { i.Read.Encoding = transfer.Encoding(99) }, "cannot say the encoding"},
		"dotted":     {func(i *Import) { i.Into = []string{"main", "odd.name"} }, `"odd.name" cannot be part of one`},
		"unnamed":    {func(i *Import) { i.Into = []string{"main", ""} }, `"" cannot be part of one`},
		"no column":  {func(i *Import) { i.Pairs = []transfer.Pair{{From: 3, To: "id"}} }, "the file has no column 4 to fill id"},
		"before any": {func(i *Import) { i.Pairs = []transfer.Pair{{From: -1, To: "id"}} }, "the file has no column 0"},
		"equals":     {func(i *Import) { i.Pairs = []transfer.Pair{{From: 0, To: "a=b"}} }, `"a=b" cannot be said that way`},
		"comma key":  {func(i *Import) { i.Load.Keys = []string{"a,b"} }, `"a,b" cannot be in one`},
		"dotted key": {func(i *Import) { i.Load.Keys = []string{"a.b"} }, `"a.b" cannot be in one`},
	} {
		imp := base
		c.change(&imp)
		if _, err := imp.Flags(); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAJobIsWrittenAndReadBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jobs", "a.json")
	if err := os.Mkdir(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	flags := map[string]any{"into": "main.items", "from": filepath.Join(dir, "data", "in.csv"), "header": true, "batch": 5,
		"map": []string{"a<b=c", "d=e"}, "table": "out"}
	if err := Write(path, "import", flags); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	want := `{
  "ikigai_job": 1,
  "command": "import",
  "flags": {
    "batch": 5,
    "from": "../data/in.csv",
    "header": true,
    "into": "main.items",
    "map": [
      "a<b=c",
      "d=e"
    ],
    "table": "out"
  }
}
`
	if string(raw) != want {
		t.Fatalf("the file:\n%s\nfor\n%s", raw, want)
	}
	if flags["from"] != filepath.Join(dir, "data", "in.csv") {
		t.Error("writing a job changed what it was given")
	}
	f, err := Read(path)
	if err != nil || f.Command != "import" || f.Version != 1 || f.Flags["batch"] != json.Number("5") {
		t.Fatalf("%+v, %v", f, err)
	}
	for name, want := range map[string][]string{
		"into": {"main.items"}, "header": {"true"}, "batch": {"5"}, "map": {"a<b=c", "d=e"},
		// Found from the job's own directory, wherever that is said to be.
		"from": {filepath.Join(dir, "jobs", "..", "data", "in.csv")},
	} {
		got, err := Values(name, f.Flags[name], filepath.Join(dir, "jobs"))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %q, %v; for %q", name, got, err, want)
		}
	}
	if got, _ := Values("out", filepath.ToSlash(path), "elsewhere"); got[0] != path {
		t.Errorf("a whole path is where it is: %q", got)
	}
	if got, _ := Values("out", "", dir); got[0] != "" {
		t.Errorf("no path is no path: %q", got)
	}
	for _, bad := range []any{nil, 1.5, []any{"a", nil}, map[string]any{}, []any{[]any{"a"}}} {
		if _, err := Values("table", bad, dir); err == nil || !strings.Contains(err.Error(), "text, a number, or true or false") {
			t.Errorf("%#v: %v", bad, err)
		}
	}
	// What a job never keeps, it does not keep whoever asks.
	for name, c := range map[string]struct {
		command string
		flags   map[string]any
		want    string
	}{
		"url":     {"export", map[string]any{"url": "postgres://u:p@h/d"}, "a job does not keep --url"},
		"consent": {"import", map[string]any{"confirm": true}, "a job does not keep --confirm"},
		"itself":  {"import", map[string]any{"save-job": "x.json"}, "a job does not keep --save-job"},
		"verb":    {"deploy", map[string]any{}, `a job is an export or an import, not "deploy"`},
	} {
		refused := filepath.Join(dir, name+".json")
		if err := Write(refused, c.command, c.flags); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
		if _, err := os.Stat(refused); err == nil {
			t.Errorf("%s: a job that was refused was written", name)
		}
	}
	// Every word a job is written in is one word for one thing.
	for w, r := range Delimiters {
		if delimiterWord(r) != w {
			t.Errorf("%q is written %q", r, delimiterWord(r))
		}
	}
	if delimiterWord('~') != "~" {
		t.Error("a delimiter with no name is itself")
	}
}
