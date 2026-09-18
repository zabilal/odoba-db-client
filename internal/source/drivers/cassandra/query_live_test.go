//go:build conformance

package cassandra

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Running CQL against a real cluster (T2.50).

// rows reads everything a result holds.
func rows(t *testing.T, res *source.Result) ([]model.Row, []model.ColumnDef) {
	t.Helper()
	if res == nil || res.Rows == nil {
		t.Fatal("a result with no rows in it")
	}
	defer res.Rows.Close()
	ctx := context.Background()
	var out []model.Row
	for {
		row, err := res.Rows.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out, res.Rows.Columns()
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		out = append(out, row)
	}
}

// session opens a console on the fixture keyspace.
func session(t *testing.T, src source.Source) source.Session {
	t.Helper()
	s, err := src.(source.Sessioner).Session(context.Background())
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLiveRunsAStatement(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	c := session(t, src)
	ctx := context.Background()
	// A write, then a read of what it wrote: a console is where a person
	// types CQL, and what they type runs.
	if _, err := c.Query(ctx, source.Statement{SQL: `INSERT INTO people (country, id, name, score, tags, seen)
		VALUES ('GB', 1, 'Ada', 9.5, {'maths'}, '2026-09-18 10:30:00+0000')`}); err != nil {
		t.Fatalf("an insert: %v", err)
	}
	t.Cleanup(func() {
		c.Query(context.Background(), source.Statement{SQL: "DELETE FROM people WHERE country = 'GB' AND id = 1"})
	})
	res, err := c.Query(ctx, source.Statement{SQL: "SELECT country, id, name, score, tags, seen FROM people WHERE country = 'GB' AND id = 1"})
	if err != nil {
		t.Fatalf("a select: %v", err)
	}
	// Cassandra counts nothing it read or changed, and a result says so
	// rather than inventing a number.
	if res.Affected != -1 {
		t.Errorf("a read counted %d rows", res.Affected)
	}
	got, cols := rows(t, res)
	if len(got) != 1 || len(cols) != 6 {
		t.Fatalf("%d rows of %d columns", len(got), len(cols))
	}
	// Every value is one the model holds, in the class the column says.
	if cols[0].Type.Class != model.TypeString || cols[1].Type.Class != model.TypeInteger ||
		cols[3].Type.Class != model.TypeFloat || cols[4].Type.Class != model.TypeArray ||
		cols[5].Type.Class != model.TypeTimestamp {
		t.Errorf("the columns are %+v", cols)
	}
	if got[0][0] != "GB" || got[0][1] != int64(1) || got[0][2] != "Ada" || got[0][3] != 9.5 {
		t.Errorf("the row reads %v", got[0])
	}
	if tags, ok := got[0][4].(model.JSON); !ok || string(tags) != `["maths"]` {
		t.Errorf("a set reads as %v", got[0][4])
	}
	if when, ok := got[0][5].(time.Time); !ok || when.UTC().Hour() != 10 {
		t.Errorf("an instant reads as %v", got[0][5])
	}
	// A column says the table it came from, so a result knows where it is
	// from even when nothing yet edits it (FR-4.8).
	if !cols[0].Type.Nullable {
		t.Error("a statement's result says a column cannot be empty, and nothing here knows that")
	}
	if cols[0].Origin.Name() != "people" || cols[0].OriginColumn != "country" {
		t.Errorf("a column comes from %+v", cols[0])
	}
	// A statement that answers with no rows still says it ran.
	res, err = c.Query(ctx, source.Statement{SQL: "INSERT INTO orders (id, total) VALUES (1, 2.5)"})
	if err != nil || res.Rows != nil || res.Affected != -1 {
		t.Errorf("an insert answered %+v: %v", res, err)
	}
	t.Cleanup(func() {
		c.Query(context.Background(), source.Statement{SQL: "DELETE FROM orders WHERE id = 1"})
	})
}

func TestLiveRunsAScriptOfStatements(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	c := session(t, src)
	results, err := c.QueryMulti(context.Background(),
		"SELECT country FROM people LIMIT 1; SELECT id FROM orders LIMIT 1", source.ScriptOptions{})
	if err != nil {
		t.Fatalf("a script: %v", err)
	}
	seen := 0
	for r := range results {
		if r.Err != nil {
			t.Fatalf("statement %d: %v", r.Index, r.Err)
		}
		if r.Index != seen {
			t.Errorf("results out of order: %d after %d", r.Index, seen)
		}
		if r.Result != nil && r.Result.Rows != nil {
			r.Result.Rows.Close()
		}
		seen++
	}
	if seen != 2 {
		t.Errorf("%d results, want 2", seen)
	}
	// A script stops where it failed: what follows was written to run after
	// what did not.
	results, err = c.QueryMulti(context.Background(),
		"SELECT * FROM nosuchtable; SELECT country FROM people LIMIT 1", source.ScriptOptions{})
	if err != nil {
		t.Fatalf("a script that fails: %v", err)
	}
	failed, ran := false, 0
	for r := range results {
		ran++
		if r.Err != nil {
			failed = true
		}
		if r.Result != nil && r.Result.Rows != nil {
			r.Result.Rows.Close()
		}
	}
	if !failed || ran != 1 {
		t.Errorf("a script that fails ran %d statements, failing %v", ran, failed)
	}
}

func TestLiveUSEMovesOneConsoleAndNotTheOthers(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	ctx := context.Background()
	moved, still := session(t, src), session(t, src)

	res, err := moved.Query(ctx, source.Statement{SQL: "USE " + empty})
	if err != nil {
		t.Fatalf("use: %v", err)
	}
	if len(res.Messages) != 1 || !strings.Contains(res.Messages[0].Text, empty) {
		t.Errorf("moving says %+v", res.Messages)
	}
	// The console that moved is on the empty keyspace, where people is not.
	if _, err := moved.Query(ctx, source.Statement{SQL: "SELECT country FROM people LIMIT 1"}); err == nil {
		t.Error("the console that moved still reads the keyspace it left")
	}
	// The other is where it was, and so is the connection the tree reads
	// through.
	res, err = still.Query(ctx, source.Statement{SQL: "SELECT country FROM people LIMIT 1"})
	if err != nil {
		t.Errorf("the console that did not move: %v", err)
	} else if res.Rows != nil {
		res.Rows.Close()
	}
	if nodes, err := src.Root(ctx); err != nil || len(nodes) == 0 {
		t.Errorf("the tree after a console moved: %v %v", labels(nodes), err)
	}
}

func TestLiveRefusesStatementsWhereItMayNot(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	ctx := context.Background()

	ro := liveConfig(fixture)
	ro.Guard = source.Guard{ReadOnly: true}
	readOnly := session(t, live(t, ro))
	for _, text := range []string{
		"INSERT INTO people (country, id) VALUES ('GB', 2)",
		"DROP TABLE orders",
		"TRUNCATE people",
	} {
		if _, err := readOnly.Query(ctx, source.Statement{SQL: text}); !errors.Is(err, source.ErrReadOnly) {
			t.Errorf("%s on a read-only connection: %v", text, err)
		}
	}
	// A read is still a read.
	if res, err := readOnly.Query(ctx, source.Statement{SQL: "SELECT country FROM people LIMIT 1"}); err != nil {
		t.Errorf("a read on a read-only connection: %v", err)
	} else if res.Rows != nil {
		res.Rows.Close()
	}
	// And a script is refused whole, before any of it runs.
	if _, err := readOnly.QueryMulti(ctx, "SELECT country FROM people LIMIT 1; TRUNCATE people",
		source.ScriptOptions{}); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a script with a write in it: %v", err)
	}

	prod := liveConfig(fixture)
	prod.Guard = source.Guard{Environment: source.EnvProduction}
	production := session(t, live(t, prod))
	write := source.Statement{SQL: "INSERT INTO orders (id, total) VALUES (2, 1.5)"}
	if _, err := production.Query(ctx, write); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("production without consent: %v", err)
	}
	write.Confirmed = true
	tidy := session(t, src)
	t.Cleanup(func() {
		tidy.Query(context.Background(), source.Statement{SQL: "DELETE FROM orders WHERE id = 2"})
	})
	if _, err := production.Query(ctx, write); err != nil {
		t.Errorf("production with consent: %v", err)
	}
}

func TestLiveReadingStopsWhenItIsCancelled(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	c := session(t, src)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Query(ctx, source.Statement{SQL: "SELECT country FROM people"}); err == nil {
		t.Error("a statement ran on a context that was given up")
	}
}
