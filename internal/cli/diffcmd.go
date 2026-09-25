package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/diff"
)

// Comparing schemas from a pipeline (FR-16.1, FR-7.1).
//
// The comparison is the application's own and is not re-implemented here: two
// models and a tree, with nothing read from a server twice (ADR-0119). What is
// added is a shape a pipeline can act on — a status that says "they differ" and
// a list a person reading a build log can follow.

// diffCommand compares a database with a saved model, or with another database.
func diffCommand() command {
	return command{
		name:    "diff",
		summary: "compare a database with a saved model, or with another database",
		setup: func(fs *flag.FlagSet) func(context.Context, *env, *flag.FlagSet) int {
			var (
				t        target
				other    target
				model    = fs.String("model", "", "a saved model's directory to compare against")
				database = fs.String("in", "", "the database, keyspace or namespace to compare; the connection's own when absent")
				toDB     = fs.String("to-in", "", "the database to compare against, on the other connection")
				code     = fs.Bool("exit-code", false, "answer 3 rather than 0 when they differ")
				same     = fs.Bool("same", false, "list what is the same as well as what differs")
			)
			t.flags(fs)
			fs.StringVar(&other.url, "to-url", "", "another connection string, to compare two databases")
			fs.StringVar(&other.connection, "to-connection", "", "another saved connection, by name or ID")
			fs.StringVar(&other.driver, "to-driver", "", "the other connection's driver")
			fs.StringVar(&other.host, "to-host", "", "the other connection's host")
			fs.IntVar(&other.port, "to-port", 0, "the other connection's port")
			fs.StringVar(&other.database, "to-database", "", "the other connection's database or file")
			fs.StringVar(&other.user, "to-user", "", "the other connection's user")
			fs.Usage = func() {
				fmt.Fprint(fs.Output(), `ikigai diff — compare a database with a saved model, or with another database

  ikigai diff --url $URL --model ./schema --exit-code
  ikigai diff --url $URL --to-url $OTHER

Everything is matched by name: a rename reads as a removal and an addition,
because two snapshots cannot say which column became which. With --exit-code the
status is 3 when anything differs, which is what a pipeline gate reads; without
it, a difference is not a failure.

Flags:
`)
				fs.PrintDefaults()
			}
			return func(ctx context.Context, e *env, fs *flag.FlagSet) int {
				return runDiff(ctx, e, &t, &other, *model, *database, *toDB, *code, *same)
			}
		},
	}
}

// comparison is what both diff and deploy need: the two models and the tree.
func comparison(ctx context.Context, e *env, t, other *target, dir, database, toDB string) (app.Comparison, *opened, int) {
	against := 0
	if strings.TrimSpace(dir) != "" {
		against++
	}
	if other.named() {
		against++
	}
	switch {
	case against == 0:
		return app.Comparison{}, nil, e.usagef("nothing to compare against: pass --model or --to-url")
	case against > 1:
		return app.Comparison{}, nil, e.usagef("compare against one thing: --model or --to-url")
	}
	conn, err := t.open(ctx)
	if err != nil {
		return app.Comparison{}, nil, e.fail(err)
	}
	if strings.TrimSpace(dir) != "" {
		c, err := app.CompareWithSaved(ctx, conn.src, database, dir)
		if err != nil {
			conn.close()
			return app.Comparison{}, nil, e.fail(err)
		}
		return c, conn, OK
	}
	to, err := other.open(ctx)
	if err != nil {
		conn.close()
		return app.Comparison{}, nil, e.fail(err)
	}
	defer to.close()
	c, err := app.Compare(ctx, conn.src, database, to.src, toDB)
	if err != nil {
		conn.close()
		return app.Comparison{}, nil, e.fail(err)
	}
	return c, conn, OK
}

// named reports whether this target was given anything at all, for the other
// side of a comparison, where saying nothing means "no other side" rather than
// "read the environment".
func (t *target) named() bool {
	return strings.TrimSpace(t.url) != "" || strings.TrimSpace(t.connection) != "" ||
		strings.TrimSpace(t.driver) != ""
}

func runDiff(ctx context.Context, e *env, t, other *target, dir, database, toDB string, code, same bool) int {
	c, conn, status := comparison(ctx, e, t, other, dir, database, toDB)
	if status != OK {
		return status
	}
	defer conn.close()

	counts := c.Tree.Count()
	c.Tree.Walk(func(_ string, n diff.Node) {
		if n.Status == diff.Same && !same {
			return
		}
		// Trimmed, because the database node of a source with one database has
		// no name to print and a line ending in a space is a line nobody can
		// grep for reliably.
		fmt.Fprintln(e.out, strings.TrimRight(fmt.Sprintf("%s %s %s", mark(n.Status), n.Kind, n.Name), " "))
		for _, f := range n.Detail {
			fmt.Fprintf(e.out, "    %s: %s -> %s\n", f.Name, f.From, f.To)
		}
	})
	if ignored := c.Ignoring; ignored.Any() {
		// What was left out is said, because a rule can hide a dropped column
		// and a report that does not mention its own rules cannot be trusted
		// (FR-7.5).
		e.sayf("left out by the model's own rules: %s", ignored.Describe())
	}
	e.sayf("%d added, %d removed, %d changed, %d the same",
		counts[diff.Added], counts[diff.Removed], counts[diff.Changed], counts[diff.Same])
	if !c.Differs() {
		return OK
	}
	if code {
		return Differs
	}
	return OK
}

// mark is one character for a status, so that a build log reads at a glance and
// a person searching one can grep for a line.
func mark(s diff.Status) string {
	switch s {
	case diff.Added:
		return "+"
	case diff.Removed:
		return "-"
	case diff.Changed:
		return "~"
	}
	return " "
}
