// Package cli is the command line (FR-16.1): the five things a pipeline does
// with a database, with no window and nobody to ask.
//
// A pipeline is not a person. It cannot answer a dialog, it reads exit codes
// rather than sentences, and what it does must be the same every time. So the
// rules here differ from the window's in three ways, and only three:
//
//   - Nothing is asked. Where the window would put up a dialog, this refuses
//     and says which flag would have answered it.
//   - A change is a choice made in the arguments. A deploy prints its
//     statements and stops unless told to apply them, which is ADR-0115's
//     "read before it runs" with the reading done by whoever wrote the
//     pipeline (FR-7.7).
//   - The status says what happened. Nothing else can: nobody is watching.
//
// Everything below the flags is the application's own: the drivers, the
// comparison, the export and the loader are the ones the window uses, and this
// adds no behaviour of its own to any of them (ARCH-1).
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Exit codes. A pipeline reads these, so they are part of the interface and
// are not to be renumbered.
const (
	// OK is success: what was asked for happened.
	OK = 0
	// Failed is a failure of the thing asked for — a connection refused, a
	// statement rejected, a file that cannot be read.
	Failed = 1
	// Usage is an argument this could not act on. Nothing was done.
	Usage = 2
	// Differs is a difference found where one was asked about: diff with
	// --exit-code, and deploy without --apply where there is work to do. It
	// is not a failure, which is why it is not 1: a pipeline gate wants to
	// tell "they differ" from "it broke".
	Differs = 3
)

// version is what --version prints. Set at build time via -ldflags, as the
// application's is.
var version = "dev"

// command is one verb.
type command struct {
	name string
	// summary is one line, for the list of commands.
	summary string
	// run does the work. Its flags are already parsed; what is left of the
	// arguments is in fs.Args().
	run func(ctx context.Context, e *env, fs *flag.FlagSet) int
	// setup declares the command's flags and returns nothing: the values are
	// closed over by run.
	setup func(fs *flag.FlagSet) func(ctx context.Context, e *env, fs *flag.FlagSet) int
}

// env is where output goes, and nothing else. A command writes what a pipeline
// reads through these rather than to the process's own streams, so that a test
// reads exactly what a pipeline would.
type env struct {
	out io.Writer
	err io.Writer
}

// sayf writes a line of explanation. Explanations go to the error stream even
// when nothing is wrong, because the other stream is where the data is: a
// pipeline redirecting rows into a file must not find "42 rows" among them.
func (e *env) sayf(format string, a ...any) {
	fmt.Fprintf(e.err, format+"\n", a...)
}

func (e *env) failf(format string, a ...any) int {
	fmt.Fprintf(e.err, "ikigai: "+format+"\n", a...)
	return Failed
}

// fail reports an error, saying nothing a connection string could be read out
// of: every error that carries one has been through redact already, and this
// is the last place it could be undone (NFR-S2).
//
// An error about the arguments is reported as one whoever noticed it: some are
// only discoverable where the work is, and a pipeline reading 1 for a flag it
// got wrong would go looking at the database.
func (e *env) fail(err error) int {
	var bad badArgs
	switch {
	case errors.As(err, &bad):
		return e.usagef("%v", err)
	case errors.Is(err, context.Canceled):
		return e.failf("stopped")
	}
	return e.failf("%v", err)
}

// badArgs is a mistake in the arguments rather than in the world: nothing was
// attempted, so nothing has half happened.
type badArgs struct{ error }

// misusef is an error about the arguments, from wherever it was noticed.
func misusef(format string, a ...any) error { return badArgs{fmt.Errorf(format, a...)} }

func (e *env) usagef(format string, a ...any) int {
	fmt.Fprintf(e.err, "ikigai: "+format+"\n", a...)
	return Usage
}

// Run is the whole command line: it reads args, does what they say, and
// answers the status a pipeline reads. It writes nothing anywhere but out and
// err, and it never calls os.Exit.
func Run(ctx context.Context, args []string, out, err io.Writer) int {
	e := &env{out: out, err: err}
	if len(args) == 0 {
		usage(e)
		return Usage
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage(e)
		return OK
	case "-version", "--version", "version":
		fmt.Fprintln(out, "ikigai", version)
		return OK
	}
	// The plugins somebody turned on, before a command asks for a driver: a
	// plugin source has to be in the registry by the time a connection names
	// it (FR-16.2). They are closed when the command ends, whatever it did.
	defer loadPlugins(ctx, e).Close()

	cmd, ok := commands()[args[0]]
	if !ok {
		e.sayf("ikigai: %q is not a command", args[0])
		usage(e)
		return Usage
	}
	fs := flag.NewFlagSet("ikigai "+cmd.name, flag.ContinueOnError)
	fs.SetOutput(err)
	run := cmd.setup(fs)
	if perr := fs.Parse(args[1:]); perr != nil {
		if errors.Is(perr, flag.ErrHelp) {
			return OK
		}
		return Usage
	}
	return run(ctx, e, fs)
}

// commands is every verb, by name.
func commands() map[string]command {
	out := map[string]command{}
	for _, c := range []command{queryCommand(), exportCommand(), importCommand(),
		deployCommand(), diffCommand(), saveCommand()} {
		out[c.name] = c
	}
	return out
}

func usage(e *env) {
	fmt.Fprint(e.err, `ikigai — the Ikigai DB command line

Usage:
  ikigai <command> [flags]

Commands:
`)
	names := make([]string, 0, len(commands()))
	for name := range commands() {
		names = append(names, name)
	}
	sort.Strings(names)
	all := commands()
	for _, name := range names {
		fmt.Fprintf(e.err, "  %-8s %s\n", name, all[name].summary)
	}
	fmt.Fprint(e.err, `
Say which database with one of:
  --url STRING            a connection string, as the application takes them
  --driver ID --database NAME [--host H --port P --user U]
  --connection NAME       a connection saved in the application

A password comes from the environment, never from a flag: IKIGAI_PASSWORD, or
IKIGAI_URL for a whole connection string. What is on a command line is visible
to everything else on the machine.

Run "ikigai <command> --help" for a command's own flags.

Exit status:
  0  it happened          2  the arguments could not be acted on
  1  it failed            3  there is a difference (diff --exit-code, deploy)
`)
}

// refName is a reference as a flag says it: the dotted path. ObjectRef prints
// its kind as well, which is right in a log and noise in a sentence that has
// just said what kind of thing it is talking about.
func refName(ref model.ObjectRef) string { return strings.Join(ref.Path, ".") }

// dotted splits a dotted name — main.items, or db.schema.table — into the
// parts a reference is made of. Quoting is not taken: a name with a dot in it
// cannot be said this way, and pretending otherwise would be a parser nobody
// asked for.
func dotted(s string) []string {
	parts := strings.Split(s, ".")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}
