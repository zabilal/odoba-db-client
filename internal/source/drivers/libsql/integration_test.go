//go:build conformance

package libsql

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// The live suite, against a libSQL server (T4.3).
//
//	docker run -d --name ikigai-libsql -p 58080:8080 -e SQLD_NODE=primary \
//	  ghcr.io/tursodatabase/libsql-server:latest
//
// IKIGAI_LIBSQL_URL points at it; IKIGAI_REQUIRE_LIBSQL=1 turns a skip into
// a failure, which is what CI sets.
//
// The fixture is the SQLite driver's own, shape for shape, because that is
// the claim this driver makes: the same introspection over a wire. A shape
// that reads differently here is the bug these tests are looking for.

func serverURL() string {
	if u := os.Getenv("IKIGAI_LIBSQL_URL"); u != "" {
		return u
	}
	return "http://127.0.0.1:58080"
}

func required() bool { return os.Getenv("IKIGAI_REQUIRE_LIBSQL") != "" }

func config(g source.Guard) source.ConnectionConfig {
	return source.ConnectionConfig{
		DriverID: driverID,
		Params:   map[string]string{"url": serverURL()},
		Guard:    g,
	}
}

func skipOrFail(t *testing.T, err error) {
	t.Helper()
	if required() {
		t.Fatalf("libSQL required but unavailable: %v", err)
	}
	t.Skipf("no libSQL server at %s: %v", serverURL(), err)
}

// dial connects as the application would, over the seeded schema.
func dial(t *testing.T, g source.Guard) source.Source {
	t.Helper()
	var d Driver
	src, err := d.Open(context.Background(), config(g))
	if err != nil {
		skipOrFail(t, err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

// seed puts the shapes the suite reads into the server. A libSQL server
// holds one database, so the fixture is dropped and rebuilt rather than
// made fresh: what is torn down is named here, in one list, so that a
// shape added later is added in both places or not at all.
func seed(t *testing.T) {
	t.Helper()
	db, err := sql.Open(driverID, serverURL())
	if err != nil {
		skipOrFail(t, err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		skipOrFail(t, err)
	}
	for _, stmt := range []string{
		`DROP VIEW IF EXISTS adults`,
		`DROP TABLE IF EXISTS orders`,
		`DROP TABLE IF EXISTS writes`,
		`DROP TABLE IF EXISTS tags`,
		`DROP TABLE IF EXISTS people`,
		`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, score REAL, born DATE, meta JSON, pic BLOB)`,
		`CREATE TABLE tags (k TEXT PRIMARY KEY, v TEXT) WITHOUT ROWID`,
		`CREATE TABLE writes (id INTEGER PRIMARY KEY, name TEXT NOT NULL DEFAULT 'none', n INTEGER)`,
		`CREATE VIEW adults AS SELECT id, name FROM people WHERE id > 10`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 100)
		 INSERT INTO people (id, name, score, born, meta) SELECT i, 'person ' || i, i * 1.5,
		   CASE WHEN i % 3 = 0 THEN '2000-01-0' || (1 + i % 2) END, '{"i": ' || i || '}' FROM n`,
		`INSERT INTO tags VALUES ('b', '2'), ('a', '1')`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, person_id INTEGER REFERENCES people(id) ON DELETE CASCADE,
		   total NUMERIC, UNIQUE (person_id, total))`,
		`CREATE INDEX orders_total ON orders (total DESC)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seeding (%.40s): %v", stmt, err)
		}
	}
}

func ref(name string) model.ObjectRef { return model.NewRef(model.KindTable, "main", name) }

func TestConformance(t *testing.T) {
	seed(t)
	conformance.Run(t, conformance.Target{
		Name: "libsql",
		Open: func(_ context.Context, t *testing.T) source.Source { return dial(t, source.Guard{}) },
		OpenGuarded: func(_ context.Context, t *testing.T, g source.Guard) source.Source {
			return dial(t, g)
		},
		Browsable: ref("people"),
		Writable:  ref("writes"),
	})
}

// A wrong address says the server could not be reached, rather than
// something about SQL. Nothing listens on port 1, on any machine.
func TestLiveAWrongAddressSaysSo(t *testing.T) {
	var d Driver
	_, err := d.Open(context.Background(), source.ConnectionConfig{
		Params: map[string]string{"url": "http://127.0.0.1:1"}})
	var ce *source.ConnectError
	if !errors.As(err, &ce) {
		t.Fatalf("it said %v", err)
	}
	if ce.Kind != source.ConnectUnreachable {
		t.Errorf("it called that %v", ce.Kind)
	}
}

// The values come back as the engine's types, which are SQLite's types,
// because libSQL is SQLite. A driver that returned everything as a string
// over the wire would still pass every introspection check, and this is
// what would catch it.
func TestLiveTheValuesAreSQLitesOwn(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{})
	ctx := context.Background()
	rs, err := src.Browse(ctx, ref("people"), source.BrowseOptions{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	cols := rs.Columns()
	want := map[string]model.TypeClass{
		"id": model.TypeInteger, "name": model.TypeString,
		"score": model.TypeFloat, "pic": model.TypeBytes,
	}
	at := map[string]int{}
	for i, c := range cols {
		at[c.Name] = i
		if w, ok := want[c.Name]; ok && c.Type.Class != w {
			t.Errorf("%s reads as %v, want %v", c.Name, c.Type.Class, w)
		}
	}
	row, err := rs.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(row) != len(cols) {
		t.Fatalf("a row of %d values for %d columns", len(row), len(cols))
	}
	if _, ok := row[at["name"]].(string); !ok {
		t.Errorf("the name arrived as %T", row[at["name"]])
	}
	if _, ok := row[at["score"]].(float64); !ok {
		t.Errorf("the score arrived as %T", row[at["score"]])
	}
}

// Info says what it is: libSQL rather than SQLite, and the address rather
// than a file, because the address is what somebody recognises it by.
func TestLiveInfoSaysWhatItIs(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{})
	info, err := src.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Product != "libSQL" {
		t.Errorf("it calls itself %q", info.Product)
	}
	if info.Version == "" {
		t.Error("it reports no version")
	}
	if got := info.Attrs["url"]; got != serverURL() {
		t.Errorf("it reports the address %q", got)
	}
	if _, ok := info.Attrs["file"]; ok {
		t.Error("it reports a file, and there is not one")
	}
}

// A server is not a file, so the engine cannot be told to refuse writes for
// the whole connection the way a local database can: the guard is the only
// thing holding read-only over a wire, and it holds over a real one.
func TestLiveReadOnlyIsTheGuards(t *testing.T) {
	seed(t)
	src := dial(t, source.Guard{ReadOnly: true})
	w, ok := src.(source.Writer)
	if !ok {
		t.Fatal("it writes rows over a wire but is not a Writer")
	}
	_, err := w.Apply(context.Background(), &source.WritePlan{
		Target:     ref("writes"),
		Statements: []source.Statement{{SQL: `INSERT INTO writes (name) VALUES ('blocked')`}},
	})
	if !errors.Is(err, source.ErrReadOnly) {
		t.Fatalf("the write said %v", err)
	}
	// And nothing was written, which is the part that matters.
	rs, err := dial(t, source.Guard{}).Browse(context.Background(), ref("writes"), source.BrowseOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if _, err := rs.Next(context.Background()); err == nil {
		t.Error("a row arrived in a table the write was refused on")
	}
}
