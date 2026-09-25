package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/app"
)

// Writing a database's structure to a directory, from a pipeline (FR-7.6).
//
// Not in FR-16.1's list, and here because the list does not work without it: a
// pipeline that deploys a model and a pipeline that compares against one both
// need a model to have been written, and until this existed the only thing that
// could write one was the window. "Fail the build where the committed schema and
// the database differ" is diff; "commit what the database now is" is this.
//
// The format is the one the window writes and the comparison reads — a tree of
// files, one per object, made for version control (ADR-0121). Nothing about it
// is the command line's own.

// saveCommand writes a model.
func saveCommand() command {
	return command{
		name:    "save",
		summary: "write a database's structure to a directory, as a model",
		setup: func(fs *flag.FlagSet) func(context.Context, *env, *flag.FlagSet) int {
			var (
				t        target
				dir      = fs.String("model", "", "the directory to write the model to")
				database = fs.String("in", "", "the database to read; the connection's own when absent")
			)
			t.flags(fs)
			fs.Usage = func() {
				fmt.Fprint(fs.Output(), `ikigai save — write a database's structure to a directory, as a model

  ikigai save --url $URL --model ./schema

The model is a tree of files, one per object, made to be committed: the same
format the application writes and "ikigai diff --model" and "ikigai deploy" read.
Nothing of the data is in it.

Flags:
`)
				fs.PrintDefaults()
			}
			return func(ctx context.Context, e *env, fs *flag.FlagSet) int {
				if strings.TrimSpace(*dir) == "" {
					return e.usagef("--model is needed: it is where the model goes")
				}
				conn, err := t.open(ctx)
				if err != nil {
					return e.fail(err)
				}
				defer conn.close()
				if err := app.SaveModel(ctx, conn.src, *database, *dir); err != nil {
					return e.fail(err)
				}
				e.sayf("the structure of %s is written under %s", conn.name, *dir)
				return OK
			}
		},
	}
}
