//go:build conformance

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/cli"
	"github.com/ikigai-db/ikigai-db/internal/diff"
)

// J6 from a pipeline: promote a schema change with no window and nobody to ask
// (FR-7.7, FR-16.1).
//
// Here rather than in internal/cli because it needs a server: the command
// line's own tests run against SQLite, which renders no DDL, so deploying is the
// one verb they cannot reach. This is that verb, against PostgreSQL, doing what
// a pipeline does — save the model, change the database behind its back, and
// have the deploy put it right.

// pipeline runs the command line as a pipeline would and answers what it sees.
func pipeline(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	status := cli.Run(context.Background(), args, &out, &errOut)
	return status, out.String(), errOut.String()
}

func TestAPipelineDeploysAModel(t *testing.T) {
	j := postgresJourney(t)
	c := j.conn
	url := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		c.User, j.secrets["password"], c.Host, c.Port, c.Database)
	on := []string{"--url", url, "--in", c.Database}
	dir := filepath.Join(t.TempDir(), "model")

	if status, _, errOut := pipeline(t, append([]string{"save", "--model", dir}, on...)...); status != cli.OK {
		t.Fatalf("saving the model: status %d; it said %s", status, errOut)
	}
	// Every other schema of this database is left out, so that what another
	// suite is doing in it cannot change what this deploys. The rules go in the
	// model, which is where a comparison reads them from (FR-7.5).
	m, err := app.ReadModel(dir)
	if err != nil {
		t.Fatal(err)
	}
	var others []string
	for _, s := range m.Schemas {
		if s.Name != pgSchema {
			others = append(others, s.Name)
		}
	}
	if err := app.SetIgnored(dir, diff.Options{Schemas: others}); err != nil {
		t.Fatal(err)
	}
	if status, out, errOut := pipeline(t, append([]string{"diff", "--exit-code", "--model", dir}, on...)...); status != cli.OK {
		t.Fatalf("a model differs from the database it came from: status %d\n%s%s", status, out, errOut)
	}

	// The change to promote: the database has lost a table the model has.
	j.ddl(t, "DROP TABLE orders")
	status, out, errOut := pipeline(t, append([]string{"diff", "--exit-code", "--model", dir}, on...)...)
	if status != cli.Differs {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if !strings.Contains(out, "+ table orders") {
		t.Errorf("it wrote\n%s", out)
	}
	if !strings.Contains(errOut, "Leaving out") {
		t.Errorf("it did not say the model's own rules are in force: %q", errOut)
	}

	// Without --apply it prints the statements and runs none of them, which is
	// what makes the reading in ADR-0115 possible for a pipeline.
	status, out, errOut = pipeline(t, append([]string{"deploy", "--model", dir}, on...)...)
	if status != cli.Differs {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if !strings.Contains(out, "CREATE TABLE") || !strings.Contains(out, "orders") {
		t.Errorf("it wrote\n%s", out)
	}
	if !strings.Contains(errOut, "nothing has run") {
		t.Errorf("it said %q", errOut)
	}
	if st, _, _ := pipeline(t, "query", "--url", url, "--sql",
		"SELECT 1 FROM "+pgSchema+".orders"); st != cli.Failed {
		t.Fatal("the table is there, and nothing was applied")
	}

	// And with it, the database matches the model again.
	status, _, errOut = pipeline(t, append([]string{"deploy", "--model", dir, "--apply"}, on...)...)
	if status != cli.OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if st, _, said := pipeline(t, "query", "--url", url, "--sql",
		"SELECT count(*) AS n FROM "+pgSchema+".orders"); st != cli.OK {
		t.Fatalf("the table was not made: %s", said)
	}
	if status, out, errOut := pipeline(t, append([]string{"diff", "--exit-code", "--model", dir}, on...)...); status != cli.OK {
		t.Errorf("after the deploy: status %d\n%s%s", status, out, errOut)
	}
}

// Dropping is not done unasked. A model that lost a table by accident would
// otherwise take the table with it, and the table is the data.
func TestAPipelineWillNotDropWithoutBeingTold(t *testing.T) {
	j := postgresJourney(t)
	c := j.conn
	url := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		c.User, j.secrets["password"], c.Host, c.Port, c.Database)
	on := []string{"--url", url, "--in", c.Database}
	dir := filepath.Join(t.TempDir(), "model")
	if status, _, errOut := pipeline(t, append([]string{"save", "--model", dir}, on...)...); status != cli.OK {
		t.Fatalf("saving the model: status %d; it said %s", status, errOut)
	}
	m, err := app.ReadModel(dir)
	if err != nil {
		t.Fatal(err)
	}
	var others []string
	for _, s := range m.Schemas {
		if s.Name != pgSchema {
			others = append(others, s.Name)
		}
	}
	if err := app.SetIgnored(dir, diff.Options{Schemas: others}); err != nil {
		t.Fatal(err)
	}
	// The database has something the model has not: closing that difference
	// means dropping it.
	j.ddl(t, "CREATE TABLE spare (k text)")

	status, out, errOut := pipeline(t, append([]string{"deploy", "--model", dir, "--apply"}, on...)...)
	if status != cli.Usage {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if !strings.Contains(errOut, "--allow-drops") {
		t.Errorf("it said %q, which does not say what would answer it", errOut)
	}
	if !strings.Contains(out, "DROP TABLE") {
		t.Errorf("it did not show what it would have dropped:\n%s", out)
	}
	if st, _, said := pipeline(t, "query", "--url", url, "--sql",
		"SELECT 1 FROM "+pgSchema+".spare"); st != cli.OK {
		t.Fatalf("the table was dropped anyway: %s", said)
	}

	status, _, errOut = pipeline(t, append([]string{"deploy", "--model", dir,
		"--apply", "--allow-drops"}, on...)...)
	if status != cli.OK {
		t.Fatalf("status %d; it said %s", status, errOut)
	}
	if st, _, _ := pipeline(t, "query", "--url", url, "--sql",
		"SELECT 1 FROM "+pgSchema+".spare"); st != cli.Failed {
		t.Error("told to drop it, it did not")
	}
}
