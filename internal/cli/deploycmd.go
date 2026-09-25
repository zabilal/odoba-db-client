package cli

import (
	"context"
	"flag"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/diff"
)

// Making a database match a saved model, from a pipeline (FR-7.7, FR-16.1).
//
// This is the one verb that changes a schema, so it is the one with a rule of
// its own: it prints the statements and stops. Running them takes --apply.
//
// ADR-0115 says a structural change is read before it runs, and in the window a
// person reads it. A pipeline has no person at the moment it runs, so the
// reading is done earlier, by whoever wrote the pipeline, and the printing is
// what makes that possible: the statements in the build log are the same
// statements --apply would run, from the same comparison.
//
// Nothing is chosen for you the other way either. The window lets somebody pick
// which differences to close; here every difference is closed, or the run is
// stopped by a rule in the model. A pipeline that wants less than everything
// wants a model that says less.

// deployCommand makes a database match a saved model.
func deployCommand() command {
	return command{
		name:    "deploy",
		summary: "make a database match a saved model (prints the statements; --apply runs them)",
		setup: func(fs *flag.FlagSet) func(context.Context, *env, *flag.FlagSet) int {
			var (
				t        target
				dir      = fs.String("model", "", "the saved model's directory")
				database = fs.String("in", "", "the database to change; the connection's own when absent")
				apply    = fs.Bool("apply", false, "run the statements, rather than only printing them")
				drops    = fs.Bool("allow-drops", false, "allow statements that drop something")
			)
			t.flags(fs)
			fs.Usage = func() {
				fmt.Fprint(fs.Output(), `ikigai deploy — make a database match a saved model

  ikigai deploy --url $URL --model ./schema              # prints what it would run
  ikigai deploy --url $URL --model ./schema --apply      # runs it

Without --apply nothing runs and the status is 3 where there is work to do,
which is what a pipeline gate reads. Anything that drops an object needs
--allow-drops as well, because a model that lost a table by accident would
otherwise take the table with it. A production connection needs --confirm.

Flags:
`)
				fs.PrintDefaults()
			}
			return func(ctx context.Context, e *env, fs *flag.FlagSet) int {
				return runDeploy(ctx, e, &t, *dir, *database, *apply, *drops)
			}
		},
	}
}

func runDeploy(ctx context.Context, e *env, t *target, dir, database string, apply, drops bool) int {
	if dir == "" {
		return e.usagef("--model is needed: a deploy is a saved model's shape, put onto a database")
	}
	c, conn, status := comparison(ctx, e, t, &target{}, dir, database, "")
	if status != OK {
		return status
	}
	defer conn.close()

	if !c.Differs() {
		e.sayf("%s already matches the model", conn.name)
		return OK
	}
	// Everything that differs is chosen: a pipeline has nobody to choose.
	chosen := app.Selection{}
	removing := 0
	c.Tree.Walk(func(id string, n diff.Node) {
		if !n.Differs() {
			return
		}
		chosen[id] = true
		if n.Status == diff.Removed {
			removing++
		}
	})
	stmts, err := app.SyncScript(conn.src, c.Live, c.Wanted, c.Tree, chosen)
	if err != nil {
		return e.fail(err)
	}
	for _, st := range stmts {
		fmt.Fprintln(e.out, st.SQL)
	}
	e.sayf("%d statements would make %s match the model", len(stmts), conn.name)
	if removing > 0 && !drops {
		// Said as a refusal rather than a warning, and before anything runs: a
		// model that lost an object by accident would take the object with it.
		e.sayf("%d of the differences remove something, and --allow-drops was not given", removing)
		if apply {
			return Usage
		}
		return Differs
	}
	if !apply {
		e.sayf("nothing has run: pass --apply to run it")
		return Differs
	}
	out, err := app.ApplyDDL(ctx, conn.src, stmts, t.confirmed)
	if err != nil {
		e.sayf("%d of %d statements ran; this one stopped it:\n%s", out.Ran, len(stmts), out.Failed)
		return e.fail(err)
	}
	e.sayf("%d statements ran; %s matches the model", out.Ran, conn.name)
	return OK
}
