package cli

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/job"
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
				format  = fs.String("format", "", readList()+"; read from the file when absent")
				sheet   = fs.String("sheet", "", "the sheet of a workbook; the first when absent")
				header  = fs.Bool("header", true, "the file's first row holds the column names")
				replace = fs.Bool("replace", false, "empty the table first, in the same transaction")
				keys    = fs.String("keys", "", "update the row with these columns' values instead of adding one")
				onError = fs.String("on-error", "abort", "a row that will not go in: abort, skip or collect")
				most    = fs.Int("max-errors", 0, "with --on-error collect, how many rows may be left out")
				batch   = fs.Int("batch", 0, "rows a transaction; the source's own when 0")
				dry     = fs.Bool("dry-run", false, "read the file and say what would happen, writing nothing")
				enc     = fs.String("encoding", "", encodingList()+"; read from the file when absent")
				comma   = fs.String("delimiter", "", "what parts a line of text: comma, tab, semicolon, pipe, or one character; read from the file when absent")
				pairs   listFlag
			)
			fs.Var(&pairs, "map", "a file column and the table column it fills, as \"file column=table column\"; "+
				"said once for each, and then no column is paired by its name")
			t.flags(fs)
			fs.Usage = func() {
				_, _ = fmt.Fprint(fs.Output(), `ikigai import — read a file into a table

  ikigai import --into main.items --from items.csv
  ikigai import --into main.items --from items.csv --dry-run
  ikigai import --into public.people --from people.xlsx --keys id --confirm

The file's columns are paired with the table's by name, telling no difference
between cases, spaces, underscores and hyphens. A file column with no match is
left out and is named here. With --map the pairs are the ones said and no
others. Replacing a table's rows, and writing to a production connection, both
need --confirm.

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
					encoding: *enc, delimiter: *comma, pairs: pairs,
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
	encoding, delimiter       string
	pairs                     []string // "file column=table column", where they were said
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
			return e.usagef("%q is not a format this reads: %s", s, readList())
		}
		read.Format = fmtID
		// The delimiter goes with the format: a file detected as comma-separated
		// carries a comma, and keeping it while being told the file is
		// tab-separated would obey half the flag and read commas anyway. Zero is
		// the format's own (transfer.Options).
		read.Comma = 0
	}
	if s := strings.TrimSpace(a.encoding); s != "" {
		enc, ok := encodings[strings.ToLower(s)]
		if !ok {
			return e.usagef("%q is not an encoding this reads: %s", s, encodingList())
		}
		read.Encoding = enc
	}
	if a.delimiter != "" {
		comma, err := delimiterOf(a.delimiter)
		if err != nil {
			return e.usagef("%v", err)
		}
		read.Comma = comma
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
	if len(a.pairs) > 0 {
		// Said, so not guessed at: the pairs are these and no others.
		if pairs, err = pairsSaid(a.pairs, rs.Columns(), b.Columns()); err != nil {
			return e.usagef("%v", err)
		}
	}
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

// readFormats, encodings and delimiters are the words a command line uses for
// the formats a file is read as, how its text is written and what parts its
// lines: the same words a job is written in, and kept with it.
var readFormats, encodings, delimiters = job.ReadFormats, job.Encodings, job.Delimiters

func encodingList() string { return strings.Join(slices.Sorted(maps.Keys(encodings)), ", ") }

// delimiterOf is the character a --delimiter names: by its name, or itself.
func delimiterOf(s string) (rune, error) {
	if r, ok := delimiters[strings.ToLower(strings.TrimSpace(s))]; ok {
		return r, nil
	}
	if r := []rune(s); len(r) == 1 && r[0] != '\n' && r[0] != '\r' && r[0] != '"' {
		return r[0], nil
	}
	return 0, fmt.Errorf("%q is not a delimiter: say comma, tab, semicolon or pipe, or the one character it is", s)
}

// listFlag is a flag that may be said more than once, each time adding one.
type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ", ") }
func (l *listFlag) Set(s string) error { *l = append(*l, s); return nil }
func (l *listFlag) Get() any           { return []string(*l) }

// pairsSaid is the pairs a command line gave, each "file column=table
// column", checked against the columns there are: a name that is neither's is
// a mistake to hear about before a row is read, and a table column filled
// twice is one of two mistakes nobody can choose between.
func pairsSaid(said []string, from []model.ColumnDef, to []model.ColumnDef) ([]transfer.Pair, error) {
	at := map[string]int{} // a file's columns are named apart as they are read
	for i, c := range from {
		at[c.Name] = i
	}
	known := map[string]bool{}
	for _, c := range to {
		known[c.Name] = true
	}
	var pairs []transfer.Pair
	filled := map[string]bool{}
	for _, s := range said {
		// The last "=": a file's column may have one in its name, and a
		// table's cannot be said with one any other way either.
		cut := strings.LastIndex(s, "=")
		if cut < 0 {
			return nil, fmt.Errorf("--map %q: a pair is \"file column=table column\"", s)
		}
		file, table := strings.TrimSpace(s[:cut]), strings.TrimSpace(s[cut+1:])
		i, ok := at[file]
		switch {
		case !ok:
			return nil, fmt.Errorf("--map %q: the file has no column called %q; it has %s", s, file, columnNames(from))
		case !known[table]:
			return nil, fmt.Errorf("--map %q: the table has no column called %q; it has %s", s, table, columnNames(to))
		case filled[table]:
			return nil, fmt.Errorf("--map %q: %s is filled twice", s, table)
		}
		filled[table] = true
		pairs = append(pairs, transfer.Pair{From: i, To: table})
	}
	return pairs, nil
}

func columnNames(cols []model.ColumnDef) string {
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return strings.Join(names, ", ")
}

// readList is every one of those words, in the order of the alphabet.
func readList() string { return strings.Join(slices.Sorted(maps.Keys(readFormats)), ", ") }

// dryProblems is how many rows a dry run names. Enough to see the shape of
// what is wrong, few enough that a file where every row is wrong does not
// print itself.
const dryProblems = 20
