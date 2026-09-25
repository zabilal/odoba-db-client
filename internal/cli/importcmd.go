package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/transfer"
)

// Reading a file into a table, from a pipeline (FR-16.1, FR-10.3).
//
// The window's import is a wizard: read the file, show what was found, let
// somebody pair the columns up and look at what would happen before it does.
// A pipeline has nobody to show it to, so the pairing is by name — the same
// pairing the wizard suggests — and what cannot be paired is said rather than
// guessed at. A column of the file that no column of the table matches is left
// out, and this says which, because a load that silently dropped a column
// would be a file that looked imported and was not.

// importCommand reads a file into a table.
func importCommand() command {
	return command{
		name:    "import",
		summary: "read a file into a table",
		setup: func(fs *flag.FlagSet) func(context.Context, *env, *flag.FlagSet) int {
			var (
				t       target
				into    = fs.String("into", "", "the table to write, as a dotted name (main.items)")
				from    = fs.String("from", "", "the file to read")
				format  = fs.String("format", "", "csv, tsv, json, ndjson or xlsx; read from the file when absent")
				sheet   = fs.String("sheet", "", "the sheet of a workbook; the first when absent")
				header  = fs.Bool("header", true, "the file's first row holds the column names")
				replace = fs.Bool("replace", false, "empty the table first, in the same transaction")
				keys    = fs.String("keys", "", "update the row with these columns' values instead of adding one")
				onError = fs.String("on-error", "abort", "a row that will not go in: abort, skip or collect")
				most    = fs.Int("max-errors", 0, "with --on-error collect, how many rows may be left out")
				batch   = fs.Int("batch", 0, "rows a transaction; the source's own when 0")
				dry     = fs.Bool("dry-run", false, "read the file and say what would happen, writing nothing")
			)
			t.flags(fs)
			fs.Usage = func() {
				fmt.Fprint(fs.Output(), `ikigai import — read a file into a table

  ikigai import --into main.items --from items.csv
  ikigai import --into main.items --from items.csv --dry-run
  ikigai import --into public.people --from people.xlsx --keys id --confirm

The file's columns are paired with the table's by name, telling no difference
between cases, spaces, underscores and hyphens. A file column with no match is
left out and is named here. Replacing a table's rows, and writing to a
production connection, both need --confirm.

Flags:
`)
				fs.PrintDefaults()
			}
			return func(ctx context.Context, e *env, fs *flag.FlagSet) int {
				if strings.TrimSpace(*into) == "" || strings.TrimSpace(*from) == "" {
					return e.usagef("--into and --from are both needed")
				}
				opt := transfer.LoadOptions{
					Batch: *batch, Confirmed: t.confirmed, Replace: *replace,
					OnError: strings.ToLower(strings.TrimSpace(*onError)), MaxErrors: *most,
				}
				if opt.OnError == "abort" {
					opt.OnError = ""
				}
				if k := strings.TrimSpace(*keys); k != "" {
					opt.Keys = dotted(strings.ReplaceAll(k, ",", "."))
				}
				return importFile(ctx, e, &t, importArgs{
					into: *into, from: *from, format: *format, sheet: *sheet,
					header: *header, dry: *dry, opt: opt,
				})
			}
		},
	}
}

// importArgs is what a load was told, gathered so that one function can do it.
type importArgs struct {
	into, from, format, sheet string
	header, dry               bool
	opt                       transfer.LoadOptions
}

func importFile(ctx context.Context, e *env, t *target, a importArgs) int {
	parts := dotted(a.into)
	if parts[len(parts)-1] == "" {
		return e.usagef("--into needs a name, as a dotted path (main.items)")
	}
	f, err := os.Open(a.from)
	if err != nil {
		return e.fail(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return e.fail(err)
	}
	read, err := transfer.Detect(f, fi.Size(), f.Name())
	if err != nil {
		return e.fail(err)
	}
	// What was detected is a starting point, as it is in the window: a flag
	// overrides it, and nothing else is guessed at afterwards.
	if s := strings.TrimSpace(a.format); s != "" {
		fmtID, ok := readFormats[strings.ToLower(s)]
		if !ok {
			return e.usagef("%q is not a format this reads: csv, tsv, json, ndjson, xlsx", s)
		}
		read.Format = fmtID
		// The delimiter goes with the format: a file detected as comma-separated
		// carries a comma, and keeping it while being told the file is
		// tab-separated would obey half the flag and read commas anyway. Zero is
		// the format's own (transfer.Options).
		read.Comma = 0
	}
	read.Header = a.header
	if s := strings.TrimSpace(a.sheet); s != "" {
		read.Sheet = s
	}

	conn, err := t.open(ctx)
	if err != nil {
		return e.fail(err)
	}
	defer conn.close()

	ref := model.NewRef(model.KindTable, parts...)
	b, err := app.NewBrowseSource(ctx, conn.src, ref, source.BrowseOptions{})
	if err != nil {
		return e.fail(err)
	}
	rs, err := transfer.Open(f, fi.Size(), read)
	if err != nil {
		return e.fail(err)
	}
	defer rs.Close()

	pairs := transfer.Suggest(rs.Columns(), b.Columns())
	if len(pairs) == 0 {
		return e.failf("none of the file's columns matches a column of %s", refName(ref))
	}
	if left := unpaired(rs.Columns(), pairs); len(left) > 0 {
		e.sayf("left out, having no column of %s to go in: %s", refName(ref), strings.Join(left, ", "))
	}
	to := map[string]model.ColumnDef{}
	for _, c := range b.Columns() {
		to[c.Name] = c
	}
	if a.dry {
		// A dry run reads the file and coerces every value, which is where a
		// load fails if it is going to. Nothing is written, and nothing about
		// the connection is touched beyond reading the table's columns.
		checked, err := transfer.Check(ctx, rs, pairs, to, dryProblems, nil)
		if err != nil {
			return e.fail(err)
		}
		e.sayf("%s would go into %s, %d of them with a value that would not",
			nounRows(checked.Rows), refName(ref), checked.Bad)
		for _, p := range checked.Problems {
			e.sayf("  row %d: %s", p.Row, p.Error())
		}
		if checked.Bad > 0 {
			// Not a failure: nothing was asked to happen and nothing did. It is
			// the status a pipeline gate reads, as a diff's is.
			return Differs
		}
		return OK
	}
	var left []*transfer.LoadError
	if a.opt.OnError != "" {
		a.opt.Skipped = func(le *transfer.LoadError) { left = append(left, le) }
	}
	l, err := transfer.Load(ctx, rs, pairs, to, ref, b, a.opt, nil)
	for _, le := range left {
		e.sayf("  left out: %v", le)
	}
	if err != nil {
		e.sayf("%s of %s written before it stopped", nounRows(l.Written), nounRows(l.Rows))
		return e.fail(err)
	}
	e.sayf("%s into %s, %s read, %d left out", nounRows(l.Written), refName(ref), nounRows(l.Rows), l.Left)
	return OK
}

// unpaired is the file's columns that no column of the table matched, by name,
// so that a load never leaves one out quietly.
func unpaired(from []model.ColumnDef, pairs []transfer.Pair) []string {
	taken := make(map[int]bool, len(pairs))
	for _, p := range pairs {
		taken[p.From] = true
	}
	var out []string
	for i, c := range from {
		if !taken[i] {
			out = append(out, c.Name)
		}
	}
	return out
}

// readFormats are the words a command line uses for the formats a file is read
// as. The same words as --format on the way out, minus the ones only written.
var readFormats = map[string]transfer.Format{
	"csv":    transfer.CSV,
	"tsv":    transfer.TSV,
	"json":   transfer.JSON,
	"ndjson": transfer.NDJSON,
	"xlsx":   transfer.XLSX,
}

// dryProblems is how many rows a dry run names. Enough to see the shape of
// what is wrong, few enough that a file where every row is wrong does not
// print itself.
const dryProblems = 20
