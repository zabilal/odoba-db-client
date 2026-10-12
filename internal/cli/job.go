package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// An export or an import, written down to be run again (FR-10.11).
//
// A job is a command's flags in a file. Not a second description of an export
// that has to be kept in step with the first: the flags a command takes are
// the only definition there is of what it can be told, so a job is those, by
// their names, and running one is running the command with them. A flag added
// to a command is a thing a job can say the day it is added.
//
// The file is JSON, with its keys in the alphabet's order, so that it reads
// the same whoever wrote it and a change to it is a line in a diff. It is made
// to be committed.
//
// Three things are never in one:
//
//   - A connection string. It may hold a password, and a file made to be
//     committed is the last place for one. A job names a saved connection, or
//     a driver and a database; a pipeline passes --url, or sets IKIGAI_URL,
//     when it runs the job.
//   - Consent. --confirm is what somebody says when they run a thing, about
//     that run. Written into the file it would be said by nobody, every time.
//   - Where the job itself is kept.
//
// A file a job names — what it reads, what it writes — is kept as a path from
// the job's own directory, so that a job and its files can be moved, or
// checked out somewhere else, together.

// jobVersion is the one shape of job file there is.
const jobVersion = 1

// jobFile is a job as it is kept.
type jobFile struct {
	Version int            `json:"ikigai_job"`
	Command string         `json:"command"`
	Flags   map[string]any `json:"flags"`
}

// saveJobFlag is the flag that writes a command down instead of running it.
const saveJobFlag = "save-job"

// jobCommands are the commands a job can be of: the two FR-10.11 names.
var jobCommands = map[string]bool{"export": true, "import": true}

// neverSaved are the flags a job does not keep, each with why, as a person is
// told it.
var neverSaved = map[string]string{
	"url":     "a connection string may hold a password, and a job is a file made to be shared; name a saved connection with --connection, or pass --url when the job is run",
	"confirm": "",
}

// pathFlags are the flags that name a file, which a job keeps as a path from
// its own directory.
var pathFlags = map[string]bool{"out": true, "from": true, "file": true}

// saveJob writes the flags a command was given as a job, and does not run it.
func saveJob(e *env, cmd string, fs *flag.FlagSet, path string) int {
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return e.fail(err)
	}
	job := jobFile{Version: jobVersion, Command: cmd, Flags: map[string]any{}}
	var refused error
	var dropped []string
	fs.Visit(func(f *flag.Flag) {
		if f.Name == saveJobFlag {
			return
		}
		if why, never := neverSaved[f.Name]; never {
			if why != "" && refused == nil {
				refused = misusef("--%s is not kept in a job: %s", f.Name, why)
			}
			dropped = append(dropped, f.Name)
			return
		}
		value := f.Value.String()
		if pathFlags[f.Name] {
			value = pathFrom(dir, value)
		}
		job.Flags[f.Name] = typed(f, value)
	})
	if refused != nil {
		return e.fail(refused)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(job); err != nil {
		return e.fail(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
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
		case bool, int, int64:
			return v
		}
	}
	return value
}

// pathFrom is a file's path as a job keeps it: from the job's directory, with
// the slashes every system reads. A file that cannot be reached from there —
// on another volume — is kept as where it is.
func pathFrom(dir, path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	rel, err := filepath.Rel(dir, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// readJob is the job in a file, or what is wrong with the file.
func readJob(path string) (jobFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return jobFile{}, err
	}
	var job jobFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields() // a key nobody reads is a mistake nobody sees
	dec.UseNumber()
	if err := dec.Decode(&job); err != nil {
		return jobFile{}, fmt.Errorf("%s is not a job: %w", path, err)
	}
	switch {
	case job.Version == 0:
		return jobFile{}, fmt.Errorf("%s is not a job: it has no \"ikigai_job\"", path)
	case job.Version != jobVersion:
		return jobFile{}, fmt.Errorf("%s is a job of version %d, and this reads version %d", path, job.Version, jobVersion)
	case !jobCommands[job.Command]:
		return jobFile{}, fmt.Errorf("%s: a job is an export or an import, not %q", path, job.Command)
	}
	return job, nil
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
			job, err := readJob(path)
			if err != nil {
				return e.fail(badArgs{err})
			}
			dir, err := filepath.Abs(filepath.Dir(path))
			if err != nil {
				return e.fail(err)
			}
			cmd := commands()[job.Command]
			fs := flag.NewFlagSet("ikigai run "+path, flag.ContinueOnError)
			fs.SetOutput(e.err)
			run := cmd.setup(fs)
			// In the alphabet's order, so that what is wrong with a job is the
			// same thing every time it is run.
			names := make([]string, 0, len(job.Flags))
			for name := range job.Flags {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if _, never := neverSaved[name]; never || name == saveJobFlag {
					return e.usagef("%s: a job does not say --%s; pass it when the job is run", path, name)
				}
				if fs.Lookup(name) == nil {
					return e.usagef("%s: %s has no flag called %q", path, job.Command, name)
				}
				value, err := flagText(job.Flags[name])
				if err != nil {
					return e.usagef("%s: %q: %v", path, name, err)
				}
				if pathFlags[name] && value != "" && !filepath.IsAbs(filepath.FromSlash(value)) {
					value = filepath.Join(dir, filepath.FromSlash(value))
				}
				if err := fs.Set(name, value); err != nil {
					return e.usagef("%s: %q: %v", path, name, err)
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

// flagText is a value from a job as a flag reads one.
func flagText(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case bool:
		return fmt.Sprint(x), nil
	case json.Number:
		return x.String(), nil
	}
	return "", errors.New("a flag's value is text, a number, or true or false")
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
