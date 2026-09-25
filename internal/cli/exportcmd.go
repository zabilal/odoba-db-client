package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Writing a table or a query out, from a pipeline (FR-16.1, FR-10.1).
//
// A table is read a page at a time to the end, which is the same reading the
// window's export makes (FR-10.3): a table of ten million rows is a file of ten
// million rows and not ten million rows of memory. A query is the query verb
// with one result and no talking, which is why --sql here is the same flag.

// exportCommand writes rows to a file.
func exportCommand() command {
	return command{
		name:    "export",
		summary: "write a table's rows, or a query's, to a file",
		setup: func(fs *flag.FlagSet) func(context.Context, *env, *flag.FlagSet) int {
			var (
				t     target
				rows  rowsOut
				table = fs.String("table", "", "the table to write, as a dotted name (main.items)")
				sql   = fs.String("sql", "", "a query to write the result of, instead of a table")
				file  = fs.String("file", "", "a file holding that query")
				where = fs.String("where", "", "a condition, in the source's own language, ANDed with nothing else")
				limit = fs.Int64("limit", 0, "at most this many rows; every row when 0")
			)
			t.flags(fs)
			rows.flags(fs, true)
			fs.Usage = func() {
				fmt.Fprint(fs.Output(), `ikigai export — write a table's rows, or a query's, to a file

  ikigai export --table main.items --out items.csv
  ikigai export --table public.orders --where "total > 100" --format json
  ikigai export --sql "SELECT * FROM items" --format ndjson

A table is read to the end a page at a time, so the file may be far larger than
this process's memory. --format xlsx needs --out: a workbook cannot be written
to a stream.

Flags:
`)
				fs.PrintDefaults()
			}
			return func(ctx context.Context, e *env, fs *flag.FlagSet) int {
				named := 0
				for _, s := range []string{*table, *sql, *file} {
					if strings.TrimSpace(s) != "" {
						named++
					}
				}
				switch {
				case named == 0:
					return e.usagef("nothing to export: pass --table, --sql or --file")
				case named > 1:
					return e.usagef("say what to export once: --table, --sql or --file")
				}
				if strings.TrimSpace(*table) == "" {
					if strings.TrimSpace(*where) != "" || *limit != 0 {
						return e.usagef("--where and --limit are for --table; put them in the query")
					}
					script, code := scriptFrom(e, *file, *sql, "", t)
					if code != OK {
						return code
					}
					return runScript(ctx, e, &t, &rows, script, nil)
				}
				return exportTable(ctx, e, &t, &rows, *table, *where, *limit)
			}
		},
	}
}

// exportTable writes a table's rows.
func exportTable(ctx context.Context, e *env, t *target, rows *rowsOut, table, where string, limit int64) int {
	parts := dotted(table)
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return e.usagef("--table needs a name, as a dotted path (main.items)")
	}
	opt, err := rows.options(parts[len(parts)-1])
	if err != nil {
		return e.usagef("%v", err)
	}
	conn, err := t.open(ctx)
	if err != nil {
		return e.fail(err)
	}
	defer conn.close()

	ref := model.NewRef(model.KindTable, parts...)
	b, err := app.NewBrowseSource(ctx, conn.src, ref, source.BrowseOptions{Where: where})
	if err != nil {
		return e.fail(err)
	}
	if opt.Format == exportInserts {
		if !b.CanScriptRows() {
			return e.failf("%s cannot write INSERT statements for %s", conn.name, refName(ref))
		}
		opt.Inserts = b.InsertRows
	}
	stream := b.Rows()
	if limit > 0 {
		stream = &firstRows{rs: stream, left: limit}
	}
	n, err := rows.copyRows(ctx, e, stream, opt)
	stream.Close()
	if err != nil {
		return e.fail(err)
	}
	e.sayf("%s from %s", nounRows(n), refName(ref))
	return OK
}
