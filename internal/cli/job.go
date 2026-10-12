package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/job"
)

// An export or an import, written down to be run again (FR-10.11): saving
// one from a command line, and running one. What a job is, and what is never
// in one, is internal/job's to say.

// saveJob writes the flags a command was given as a job, and does not run it.
func saveJob(e *env, cmd string, fs *flag.FlagSet, path string) int {
	flags := map[string]any{}
	var refused error
	var dropped []string
	fs.Visit(func(f *flag.Flag) {
		if f.Name == job.SaveFlag {
			return
		}
		if why, never := job.NeverSaved[f.Name]; never {
			if why != "" && refused == nil {
				refused = misusef("--%s is not kept in a job: %s", f.Name, why)
			}
			dropped = append(dropped, f.Name)
			return
		}
		flags[f.Name] = typed(f, f.Value.String())
	})
	if refused != nil {
		return e.fail(refused)
	}
	if err := job.Write(path, cmd, flags); err != nil {
		return e.fail(err)
	}
	e.sayf("the %s is written to %s, and was not run; run it with: ikigai run %s", cmd, path, path)
	for _, name := range dropped {
		// Said, because a flag that was typed and is not in the file would
		// otherwise be found missing on the night it mattered.
		e.sayf("--%s is not kept in a job: it is said each time the job is run", name)
	}
	return OK
}

// typed is a flag's value as JSON would write it: a yes or no as one, a count
// as a number, anything else as text.
func typed(f *flag.Flag, value string) any {
	if g, ok := f.Value.(flag.Getter); ok {
		switch v := g.Get().(type) {
		case bool, int, int64, []string:
			return v // a flag said more than once is a list of what it was told
		}
	}
	return value
}

// runCommand runs a job.
func runCommand() command {
	return command{
		name:    "run",
		summary: "run an export or an import that was saved as a job",
		raw: func(ctx context.Context, e *env, args []string) int {
			if len(args) == 0 || strings.HasPrefix(args[0], "-") {
				if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "-help") {
					runUsage(e)
					return OK
				}
				return e.usagef("run needs a job: ikigai run JOB [flags]")
			}
			path := args[0]
			saved, err := job.Read(path)
			if err != nil {
				return e.fail(badArgs{err})
			}
			dir, err := filepath.Abs(filepath.Dir(path))
			if err != nil {
				return e.fail(err)
			}
			cmd := commands()[saved.Command]
			fs := flag.NewFlagSet("ikigai run "+path, flag.ContinueOnError)
			fs.SetOutput(e.err)
			run := cmd.setup(fs)
			// In the alphabet's order, so that what is wrong with a job is the
			// same thing every time it is run.
			names := make([]string, 0, len(saved.Flags))
			for name := range saved.Flags {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if _, never := job.NeverSaved[name]; never || name == job.SaveFlag {
					return e.usagef("%s: a job does not say --%s; pass it when the job is run", path, name)
				}
				if fs.Lookup(name) == nil {
					return e.usagef("%s: %s has no flag called %q", path, saved.Command, name)
				}
				values, err := job.Values(name, saved.Flags[name], dir)
				if err != nil {
					return e.usagef("%s: %q: %v", path, name, err)
				}
				for _, value := range values {
					if err := fs.Set(name, value); err != nil {
						return e.usagef("%s: %q: %v", path, name, err)
					}
				}
			}
			// What is said on the command line is said last, and wins: the
			// connection, the consent, and anything about this run that
			// differs from the job.
			if perr := fs.Parse(args[1:]); perr != nil {
				if errors.Is(perr, flag.ErrHelp) {
					return OK
				}
				return Usage
			}
			if fs.NArg() > 0 {
				return e.usagef("%q is not a flag; after the job come flags", fs.Arg(0))
			}
			return run(ctx, e, fs)
		},
	}
}

func runUsage(e *env) {
	_, _ = fmt.Fprint(e.err, `ikigai run — run an export or an import that was saved as a job

  ikigai export --connection shop --table main.items --format parquet --out items.parquet --save-job nightly.json
  ikigai run nightly.json
  ikigai run nightly.json --url $URL --confirm

A job is a command's flags in a file, written by --save-job on export and
import. Flags after the job are added to the job's own and win over them. A job
never holds a connection string or --confirm: pass those here. The files a job
names are found from the job's own directory, wherever it is run from.
`)
}
