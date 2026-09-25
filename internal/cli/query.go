package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Running a script from a pipeline (FR-16.1).
//
// The script comes from a file, from an argument, or from a query somebody
// saved in the application. What it returns goes out in the format asked for,
// one result after another, and a statement that returns no rows says how many
// it changed. Nothing is buffered that does not have to be: a pipeline exporting
// a million rows through this should not need the memory for a million rows.

// queryCommand runs a script.
func queryCommand() command {
	return command{
		name:    "query",
		summary: "run a script and write what it returns",
		setup: func(fs *flag.FlagSet) func(context.Context, *env, *flag.FlagSet) int {
			var (
				t     target
				rows  rowsOut
				file  = fs.String("file", "", "a file holding the script")
				sql   = fs.String("sql", "", "the script itself")
				saved = fs.String("saved", "", "a query saved in the application, by name")
				param params
			)
			t.flags(fs)
			rows.flags(fs, true)
			fs.Var(&param, "param", "a value for a :name parameter, as name=value; repeatable")
			fs.Usage = func() {
				fmt.Fprint(fs.Output(), `ikigai query — run a script and write what it returns

  ikigai query --sql "SELECT * FROM items" --format json
  ikigai query --file report.sql --out report.csv
  cat q.sql | ikigai query --file -

A script's statements run in order and stop at the first that fails. What each
returns is written in turn; a statement that returns no rows says how many it
changed. A script that changes data on a production connection needs --confirm.

Flags:
`)
				fs.PrintDefaults()
			}
			return func(ctx context.Context, e *env, fs *flag.FlagSet) int {
				script, code := scriptFrom(e, *file, *sql, *saved, t)
				if code != OK {
					return code
				}
				return runScript(ctx, e, &t, &rows, script, param)
			}
		},
	}
}

// params collects repeated --param name=value flags.
type params map[string]any

func (p *params) String() string {
	if p == nil || len(*p) == 0 {
		return ""
	}
	names := make([]string, 0, len(*p))
	for k := range *p {
		names = append(names, k)
	}
	return strings.Join(names, ",")
}

func (p *params) Set(v string) error {
	name, value, ok := strings.Cut(v, "=")
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return errors.New("a parameter is name=value")
	}
	if *p == nil {
		*p = params{}
	}
	(*p)[name] = value
	return nil
}

// scriptFrom reads the script, from wherever it was said to be.
func scriptFrom(e *env, file, sql, saved string, t target) (string, int) {
	given := 0
	for _, s := range []string{file, sql, saved} {
		if strings.TrimSpace(s) != "" {
			given++
		}
	}
	switch {
	case given == 0:
		return "", e.usagef("nothing to run: pass --sql, --file or --saved")
	case given > 1:
		return "", e.usagef("say what to run once: --sql, --file or --saved")
	}
	switch {
	case strings.TrimSpace(sql) != "":
		return sql, OK
	case strings.TrimSpace(file) != "":
		text, err := readFile(file)
		if err != nil {
			return "", e.fail(err)
		}
		return text, OK
	}
	text, err := savedQuery(saved)
	if err != nil {
		return "", e.fail(err)
	}
	return text, OK
}

// readFile reads a script, taking "-" as standard input: a pipeline that
// generates a script sends it rather than writing it down.
func readFile(name string) (string, error) {
	if strings.TrimSpace(name) == "-" {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// runScript runs it and writes what comes back.
func runScript(ctx context.Context, e *env, t *target, rows *rowsOut, script string, named params) int {
	if strings.TrimSpace(script) == "" {
		return e.usagef("the script is empty")
	}
	opt, err := rows.options("rows")
	if err != nil {
		return e.usagef("%v", err)
	}
	if opt.Format == exportInserts {
		// An INSERT statement needs a table to insert into, and a query's rows
		// are not a table's: they may be two tables joined, or none. Said here
		// rather than by the writer halfway through.
		return e.usagef("--format sql needs --table: a query's rows have no table to be inserted into")
	}
	conn, err := t.open(ctx)
	if err != nil {
		return e.fail(err)
	}
	defer conn.close()

	q, ok := conn.src.(source.Queryer)
	if !ok {
		return e.failf("%s takes no queries", conn.name)
	}
	results, err := q.QueryMulti(ctx, script, source.ScriptOptions{
		Confirmed: t.confirmed, Named: named})
	if err != nil {
		return e.fail(err)
	}
	status := OK
	for sr := range results {
		if sr.Err != nil {
			// The rest of the channel is drained rather than abandoned: a
			// driver writing into a channel nobody reads is a driver that
			// never finishes.
			status = e.fail(sr.Err)
			continue
		}
		if status != OK {
			continue
		}
		if sr.Result == nil {
			continue
		}
		for _, m := range sr.Result.Messages {
			e.sayf("%s", m.Text)
		}
		if sr.Result.Rows == nil {
			e.sayf("%s", affected(sr.Result.Affected))
			continue
		}
		n, err := rows.copyRows(ctx, e, sr.Result.Rows, opt)
		sr.Result.Rows.Close()
		if err != nil {
			status = e.fail(err)
			continue
		}
		e.sayf("%s", nounRows(n))
	}
	return status
}

// affected says what a statement that returned no rows did. A source that does
// not count says so rather than reporting nothing, which reads as none.
func affected(n int64) string {
	if n < 0 {
		return "done; this source does not say how many rows it changed"
	}
	return nounRows(n) + " changed"
}
