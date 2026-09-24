//go:build conformance

package cockroach

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// Integration tests against a real CockroachDB (REQ-DRV-1, T3.33). They
// expect the ikigai-cockroach container on port 56257 and skip if it is
// not running; IKIGAI_REQUIRE_COCKROACH=1 makes that a failure, as in CI.

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func port() int {
	if v := os.Getenv("IKIGAI_COCKROACH_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 56257
}

func database() string { return env("IKIGAI_COCKROACH_DATABASE", "ikigai") }
func user() string     { return env("IKIGAI_COCKROACH_USER", "root") }

func config(guard source.Guard) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: port(),
		Database: database(), User: user(),
		TLS: source.TLSConfig{Mode: "disable"}, Guard: guard}
}

// admin is a plain pool, for building the fixture.
func admin(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	dsn := "postgresql://" + user() + "@127.0.0.1:" + strconv.Itoa(port()) + "/defaultdb?sslmode=disable"
	pool, err := pgxpool.New(ctx, dsn)
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		if pool != nil {
			pool.Close()
		}
		if os.Getenv("IKIGAI_REQUIRE_COCKROACH") != "" {
			t.Fatalf("CockroachDB required but unavailable: %v", err)
		}
		t.Skipf("no CockroachDB on port %d (docker start ikigai-cockroach): %v", port(), err)
	}
	return pool
}

var fixture = []string{
	`CREATE TABLE people (
	   id INT8 PRIMARY KEY,
	   name STRING NOT NULL,
	   score DECIMAL(30,10),
	   born DATE,
	   seen TIMESTAMPTZ,
	   meta JSONB,
	   tag UUID,
	   ok BOOL,
	   addr INET,
	   blob BYTES,
	   tags STRING[])`,
	`COMMENT ON TABLE people IS 'Everyone we know'`,
	`COMMENT ON COLUMN people.name IS 'What they are called'`,
	`INSERT INTO people (id, name, score, born, meta, ok)
	 SELECT i, 'person ' || i::STRING, i * 1.5,
	        CASE WHEN i % 3 = 0 THEN DATE '2000-01-01' + (i % 2) END,
	        ('{"i": ' || i::STRING || '}')::JSONB, i % 2 = 0
	 FROM generate_series(1, 100) AS g(i)`,
	`INSERT INTO people (id, name, score, tag, addr, blob, tags, seen)
	 VALUES (1000, 'exact', 12345678901234567890.1234567890,
	         '6f9619ff-8b86-d011-b42d-00c04fc964ff', '192.168.0.1', b'\x01\x02',
	         ARRAY['a','b'], TIMESTAMPTZ '2024-03-01 12:00:00+00')`,
	`CREATE TABLE orders (
	   id INT8 PRIMARY KEY,
	   person_id INT8 REFERENCES people (id) ON DELETE CASCADE,
	   total DECIMAL(10,2),
	   CONSTRAINT ck_total CHECK (total >= 0))`,
	`CREATE INDEX orders_total ON orders (total DESC)`,
	// An index of two columns, so that reading one in the catalogue's
	// order rather than the index's would read it wrongly.
	`CREATE INDEX orders_pair ON orders (total, person_id)`,
	// A key of two columns, declared in the other order from the table's,
	// and a key that refers to it.
	`CREATE TABLE pair (a INT8, b INT8, c INT8, CONSTRAINT pair_pk PRIMARY KEY (b, a))`,
	`INSERT INTO pair VALUES (1, 2, 3), (2, 1, 4)`,
	`CREATE TABLE pairref (x INT8, y INT8,
	   CONSTRAINT fk_pair FOREIGN KEY (y, x) REFERENCES pair (b, a))`,
	// Rows written out of their key's order, so that a browse which did
	// not order by the key would read them in the order they were written.
	`CREATE TABLE jumbled (id INT8 PRIMARY KEY, v STRING)`,
	`INSERT INTO jumbled VALUES (3, 'three'), (1, 'one'), (2, 'two')`,
	// The conformance suite's writable table: an id the server numbers
	// from 1 when it is not given. CockroachDB's SERIAL is unique_rowid(),
	// which is unique and enormous rather than counted, so the counting is
	// a sequence's.
	`CREATE SEQUENCE writes_id_seq`,
	`CREATE TABLE writes (
	   id INT8 PRIMARY KEY DEFAULT nextval('writes_id_seq'),
	   name STRING NOT NULL DEFAULT 'none',
	   n INT8)`,
	// No key of its own: the engine makes one, hidden, and that is what
	// addresses these rows.
	`CREATE TABLE nokey (a INT8, b STRING)`,
	`INSERT INTO nokey VALUES (2, 'y'), (1, 'x'), (1, 'x'), (3, 'z')`,
	`CREATE SEQUENCE people_seq START 2000`,
	// A second schema, so that public sorting first is an order rather
	// than the only entry in a list.
	`CREATE SCHEMA app`,
	`CREATE TABLE app.elsewhere (id INT8 PRIMARY KEY)`,
	// A routine that writes. The classifier reads "SELECT grow()" and sees
	// nothing to refuse, which is what the second read-only defence is
	// for: the server refuses it inside a READ ONLY transaction.
	`CREATE FUNCTION grow() RETURNS INT8 LANGUAGE SQL AS $$ INSERT INTO writes (n) VALUES (1) RETURNING n $$`,
	// A routine that turns read-only mode off for the session. The
	// classifier reads "SELECT sneak()" and sees nothing, which is what
	// the third defence cannot survive and the second does not need to:
	// every statement gets its own READ ONLY transaction whatever the
	// session default has become.
	`CREATE FUNCTION sneak() RETURNS STRING LANGUAGE SQL AS $$ SELECT set_config('default_transaction_read_only', 'off', false) $$`,
	`CREATE VIEW adults AS SELECT id, name FROM people WHERE id > 10`,
	// The badges are the estimates the engine keeps, and nothing has
	// measured a table made a moment ago.
	// Measured twice, with rows written in between: a badge reads the
	// newest gather, and the older one still says what it said. The first
	// is over a pair of columns, which is a set the second does not
	// gather and so does not replace.
	`CREATE STATISTICS early ON meta, tag FROM people`,
	`INSERT INTO people (id, name) SELECT i, 'later ' || i::STRING FROM generate_series(2001, 2010) AS g(i)`,
	`CREATE STATISTICS fixture FROM people`,
	`CREATE STATISTICS fixture FROM nokey`,
	`CREATE STATISTICS fixture FROM jumbled`,
	`CREATE STATISTICS fixture FROM pair`,
}

// other is a second database, named so that its name has to be quoted.
// What proves a connection reads the whole cluster is finding a table only
// the other database holds: a driver reading whichever database it is
// attached to would answer with this one's tables instead.
func other() string { return env("IKIGAI_COCKROACH_OTHER", "Ikigai Other") }

// shutOut is a login with no right to open that second database.
func shutOut() string { return env("IKIGAI_COCKROACH_SHUTOUT", "probe_user") }

var otherFixture = []string{
	`CREATE TABLE only_here (id INT8 PRIMARY KEY, note STRING)`,
	`INSERT INTO only_here VALUES (1, 'over here')`,
	`CREATE STATISTICS fixture FROM only_here`,
}

// quoted writes a name the way the driver would have to.
func quoted(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// build makes the fixture, fresh, before each test that reads it.
func build(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	pool := admin(t)
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP DATABASE IF EXISTS `+database()+` CASCADE`); err != nil {
		t.Fatalf("clearing: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE DATABASE `+database()); err != nil {
		t.Fatalf("making the database: %v", err)
	}
	db, err := pgxpool.New(ctx, "postgresql://"+user()+"@127.0.0.1:"+strconv.Itoa(port())+
		"/"+database()+"?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range fixture {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture %.60q: %v", stmt, err)
		}
	}
	buildOther(ctx, t, pool)
}

// buildOther makes the second database and the login that may not open it.
func buildOther(ctx context.Context, t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, stmt := range []string{
		`DROP DATABASE IF EXISTS ` + quoted(other()) + ` CASCADE`,
		`CREATE DATABASE ` + quoted(other()),
		`CREATE USER IF NOT EXISTS ` + quoted(shutOut()),
		// A database this login may not open. FR-2.1 hides one rather than
		// listing it for every expansion to fail with a refusal.
		`REVOKE CONNECT ON DATABASE ` + quoted(other()) + ` FROM public`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("second database %.60q: %v", stmt, err)
		}
	}
	db, err := pgxpool.New(ctx, "postgresql://"+user()+"@127.0.0.1:"+strconv.Itoa(port())+
		"/"+url.PathEscape(other())+"?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range otherFixture {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatalf("second database %.60q: %v", stmt, err)
		}
	}
}

func open(t *testing.T, guard source.Guard) *crdbSource {
	t.Helper()
	build(t)
	return opened(t, guard)
}

// opened is a connection on the fixture as it stands, without making it
// again: a test that writes needs its other connections to see the rows it
// has already written.
func opened(t *testing.T, guard source.Guard) *crdbSource {
	t.Helper()
	src, err := Driver{}.Open(context.Background(), config(guard))
	if err != nil {
		t.Fatal(err)
	}
	// Closing waits for every connection to come back, so a connection
	// something is still holding — a transaction nobody ended, a result
	// nobody closed — would make this wait for ever, and every failure in
	// the test would arrive as a timeout instead of as itself.
	t.Cleanup(func() {
		done := make(chan struct{})
		go func() { src.Close(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("closing the connection did not finish: something is still holding one")
		}
	})
	return src.(*crdbSource)
}

func ref(kind model.ObjectKind, name string) model.ObjectRef {
	return model.NewRef(kind, database(), "public", name)
}

func people() model.ObjectRef { return ref(model.KindTable, "people") }

func drain(t *testing.T, rs model.RowStream) []model.Row {
	t.Helper()
	return drainWith(context.Background(), t, rs)
}

// drainWith reads the rows on the caller's own context. A read can block —
// on a row an unfinished transaction has claimed and not let go — and a
// test that read it on a context of its own would wait for ever instead
// of failing with what it was given.
func drainWith(ctx context.Context, t *testing.T, rs model.RowStream) []model.Row {
	t.Helper()
	defer rs.Close()
	var out []model.Row
	for {
		r, err := rs.Next(ctx)
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
}

func labels(ns []model.Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Label
	}
	return out
}

func find(ns []model.Node, label string) *model.Node {
	for i := range ns {
		if ns[i].Label == label {
			return &ns[i]
		}
	}
	return nil
}

func TestConformance(t *testing.T) {
	build(t)
	conformance.Run(t, conformance.Target{
		Name: "cockroach",
		Open: func(ctx context.Context, t *testing.T) source.Source {
			src, err := Driver{}.Open(ctx, config(source.Guard{}))
			if err != nil {
				t.Fatal(err)
			}
			return src
		},
		Browsable: people(),
		Writable:  ref(model.KindTable, "writes"),
		OpenGuarded: func(ctx context.Context, t *testing.T, g source.Guard) source.Source {
			src, err := Driver{}.Open(ctx, config(g))
			if err != nil {
				t.Fatal(err)
			}
			return src
		},
	})
}

// What the driver hands over for each type, which is what the grid shows
// (FR-3.8). Written against what the server really answers rather than
// against anybody's documentation.
func TestLiveWhatTheDriverHandsOver(t *testing.T) {
	src := open(t, source.Guard{})
	rs, err := src.Browse(context.Background(), people(), source.BrowseOptions{Limit: 1,
		Filters: []source.Filter{{Column: "id", Op: source.OpEqual, Values: []any{int64(1000)}}}})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	at := map[string]any{}
	native := map[string]string{}
	for i, c := range rs.Columns() {
		at[c.Name] = rows[0][i]
		native[c.Name] = c.Type.Native
		t.Logf("%-6s native=%-16s class=%-10v value=%#v", c.Name, c.Type.Native, c.Type.Class, rows[0][i])
	}

	// An exact number keeps every digit, and its column's places with them.
	if got := at["score"]; got != model.Decimal("12345678901234567890.1234567890") {
		t.Errorf("score reads %#v, which is not the number the row holds", got)
	}
	if native["score"] != "numeric(30,10)" {
		t.Errorf("score's type reads %q", native["score"])
	}
	// An address is written with only the mask it has: the server shows
	// 192.168.0.1, and a /32 added back would be a value the column does
	// not hold.
	if got := at["addr"]; got != "192.168.0.1" {
		t.Errorf("addr reads %#v", got)
	}
	if got := at["tag"]; got != "6f9619ff-8b86-d011-b42d-00c04fc964ff" {
		t.Errorf("tag reads %#v", got)
	}
	if got, ok := at["blob"].([]byte); !ok || !slices.Equal(got, []byte{1, 2}) {
		t.Errorf("blob reads %#v", at["blob"])
	}
	list, ok := at["tags"].([]any)
	if !ok || !slices.Equal(list, []any{"a", "b"}) {
		t.Errorf("tags reads %#v", at["tags"])
	}
	if got := at["seen"]; got == nil {
		t.Error("seen reads as nothing")
	}
	_ = strings.TrimSpace
}

// The explorer walks the cluster: databases, schemas, folders, tables and
// their columns (FR-2.1, FR-2.2).
func TestLiveTheExplorerWalksTheCluster(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()

	roots, err := src.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ours := find(roots, database())
	if ours == nil {
		t.Fatalf("the databases are %q", labels(roots))
	}
	if ours.Attrs["current"] != "true" {
		t.Errorf("%s is the database this connection is on and does not say so", database())
	}
	// The cluster's own database is not somebody's data, and reading it is
	// how a person breaks something.
	if find(roots, "system") != nil {
		t.Errorf("the system database is listed: %q", labels(roots))
	}

	schemas, err := src.Children(ctx, ours.Ref)
	if err != nil {
		t.Fatal(err)
	}
	pub := find(schemas, "public")
	if pub == nil {
		t.Fatalf("the schemas are %q", labels(schemas))
	}
	// public first, because it is where most people's tables are, and the
	// rest by name after it.
	if got := labels(schemas); !slices.Equal(got, []string{"public", "app"}) {
		t.Errorf("the schemas read %q", got)
	}
	for _, hidden := range []string{"crdb_internal", "pg_catalog", "information_schema", "pg_extension"} {
		if find(schemas, hidden) != nil {
			t.Errorf("%s is listed as a schema somebody would browse: %q", hidden, labels(schemas))
		}
	}

	folders, err := src.Children(ctx, pub.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if find(folders, "Tables") == nil {
		t.Fatalf("the folders are %q", labels(folders))
	}

	tables, err := src.Children(ctx, model.ClassRef(pub.Ref, model.KindTable))
	if err != nil {
		t.Fatal(err)
	}
	pe := find(tables, "people")
	if pe == nil {
		t.Fatalf("the tables are %q", labels(tables))
	}
	if !pe.Browsable {
		t.Error("a table is not browsable")
	}
	// A view is not a table, and the engine's listing tells them apart.
	if find(tables, "adults") != nil {
		t.Errorf("a view is listed among the tables: %q", labels(tables))
	}

	cols, err := src.Children(ctx, pe.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if find(cols, "id") == nil || find(cols, "score") == nil {
		t.Fatalf("the columns are %q", labels(cols))
	}
	if got := find(cols, "score").Attrs["type"]; got != "numeric(30,10)" {
		t.Errorf("score's type reads %q", got)
	}
	if got := find(cols, "id").Attrs["key"]; got != "primary" {
		t.Errorf("id is the key and says %q", got)
	}
}

// One connection reads every database in the cluster. That is what this
// driver is arranged around, and it is the thing PostgreSQL will not do
// (ADR-0145).
func TestLiveOneConnectionReadsAnotherDatabase(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()

	// A table only the other database holds, reached by a name that has to
	// be quoted.
	tables, err := src.Children(ctx,
		model.ClassRef(model.NewRef(model.KindSchema, other(), "public"), model.KindTable))
	if err != nil {
		t.Fatalf("reading another database's tables: %v", err)
	}
	if find(tables, "only_here") == nil {
		t.Fatalf("the other database holds %q", labels(tables))
	}
	// And not this one's: a driver reading whichever database it happens to
	// be attached to would answer with the fixture's tables instead.
	if find(tables, "people") != nil {
		t.Fatalf("that is this database's own listing: %q", labels(tables))
	}

	there := model.NewRef(model.KindTable, other(), "public", "only_here")
	// Its rows.
	rs, err := src.Browse(ctx, there, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatalf("browsing another database's table: %v", err)
	}
	rows := drain(t, rs)
	if len(rows) != 1 || rows[0][1] != "over here" {
		t.Fatalf("the other database's rows read %v", rows)
	}
	// Its structure, which is read through a different set of statements.
	got, err := src.Describe(ctx, there)
	if err != nil {
		t.Fatalf("describing a table in another database: %v", err)
	}
	if k := got.(*model.Table).PrimaryKey; k == nil || !slices.Equal(k.Columns, []string{"id"}) {
		t.Errorf("a table in another database has key %+v", k)
	}
	// And its badge, which is read through another again.
	if _, ok, err := src.Badge(ctx, there); err != nil || !ok {
		t.Errorf("the other database's table has no badge: ok=%v err=%v", ok, err)
	}
}

// A database this login may not open is hidden rather than listed: showing
// it only for every expansion to fail is noise (FR-2.1).
func TestLiveADatabaseNobodyMayOpenIsHidden(t *testing.T) {
	build(t)
	cfg := config(source.Guard{})
	cfg.User = shutOut()
	src, err := Driver{}.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("opening as %s: %v", shutOut(), err)
	}
	defer src.Close()

	roots, err := src.(*crdbSource).Root(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if find(roots, other()) != nil {
		t.Errorf("a database this login may not open is listed: %q", labels(roots))
	}
	if find(roots, database()) == nil {
		t.Errorf("the database it may open is missing: %q", labels(roots))
	}
}

// A table with no key of its own is still addressed, and still editable:
// the engine makes a key where nobody declared one. It is hidden, so it is
// no part of the table's structure, and it is what a browse writes by
// (ADR-0145).
func TestLiveATableWithNoKeyIsStillAddressed(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	nokey := ref(model.KindTable, "nokey")

	// The structure is the two columns somebody declared.
	cols, err := src.Children(ctx, nokey)
	if err != nil {
		t.Fatal(err)
	}
	if got := labels(cols); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("the structure shows %q, which is not what was declared", got)
	}

	rs, err := src.Browse(ctx, nokey, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	id := model.IdentityOf(rs)
	rows := drain(t, rs)
	if id.Kind != model.IdentityPrimaryKey {
		t.Fatalf("rows are addressed as %v", id.Kind)
	}
	if !slices.Equal(id.Columns, []string{"rowid"}) {
		t.Fatalf("the address is %q", id.Columns)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d rows", len(rows))
	}
	// Two of these rows are the same in every declared column. The address
	// is what tells them apart, so it has to be in the grid's projection.
	names := []string{}
	for _, c := range rs.Columns() {
		names = append(names, c.Name)
	}
	if !slices.Equal(names, []string{"a", "b", "rowid"}) {
		t.Fatalf("the browse reads %q", names)
	}

	// A browse asked for columns reads those columns: the engine's key is
	// added to a browse that asked for none, not to every browse.
	chosen, err := src.Browse(ctx, nokey, source.BrowseOptions{Limit: 10, Columns: []string{"b"}})
	if err != nil {
		t.Fatal(err)
	}
	defer chosen.Close()
	var only []string
	for _, c := range chosen.Columns() {
		only = append(only, c.Name)
	}
	if !slices.Equal(only, []string{"b"}) {
		t.Errorf("a browse asked for one column read %q", only)
	}
	// And those rows cannot be written, their key not being among them.
	if k := model.IdentityOf(chosen).Kind; k != model.IdentityNone {
		t.Errorf("rows with no key on the screen are addressed as %v", k)
	}
}

// A key of two columns is read in the order it was declared, which here is
// not the order its columns are in.
func TestLiveAKeyIsReadInTheOrderItWasDeclared(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	pair := ref(model.KindTable, "pair")

	key, err := src.tableKey(ctx, pair)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(key.columns, []string{"b", "a"}) {
		t.Errorf("the key reads %q, not the order it was declared in", key.columns)
	}
	if key.hidden {
		t.Error("a key somebody declared is called the engine's own")
	}
	got, err := src.Describe(ctx, pair)
	if err != nil {
		t.Fatal(err)
	}
	if k := got.(*model.Table).PrimaryKey; k == nil || !slices.Equal(k.Columns, []string{"b", "a"}) {
		t.Errorf("the structure's key reads %+v", k)
	}
	// A browse addresses those rows by both columns, in that order.
	rs, err := src.Browse(ctx, pair, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	if id := model.IdentityOf(rs); !slices.Equal(id.Columns, []string{"b", "a"}) {
		t.Errorf("the rows are addressed by %q", id.Columns)
	}
}

// Each table's key is its own. They are remembered between pages, and a
// connection that remembered one for all of them would write by the wrong
// columns.
func TestLiveEachTableKeepsItsOwnKey(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	for _, c := range []struct {
		table string
		key   []string
	}{
		{"people", []string{"id"}},
		{"pair", []string{"b", "a"}},
		{"nokey", []string{"rowid"}},
		{"jumbled", []string{"id"}},
		// And again, now that the first answers are remembered.
		{"people", []string{"id"}},
		{"pair", []string{"b", "a"}},
	} {
		key, err := src.tableKey(ctx, ref(model.KindTable, c.table))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(key.columns, c.key) {
			t.Errorf("%s is addressed by %q, want %q", c.table, key.columns, c.key)
		}
	}
}

// What a table is made of (FR-2.4): its key, its foreign keys with their
// actions, its checks with their expressions, and its indexes as they were
// declared.
func TestLiveTheStructureOfATable(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()

	got, err := src.Describe(ctx, ref(model.KindTable, "orders"))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := got.(*model.Table)
	if !ok {
		t.Fatalf("described an orders table as %T", got)
	}
	if tbl.PrimaryKey == nil || !slices.Equal(tbl.PrimaryKey.Columns, []string{"id"}) {
		t.Fatalf("the key is %+v", tbl.PrimaryKey)
	}
	if len(tbl.ForeignKeys) != 1 {
		t.Fatalf("there are %d foreign keys", len(tbl.ForeignKeys))
	}
	fk := tbl.ForeignKeys[0]
	if !slices.Equal(fk.Columns, []string{"person_id"}) || fk.RefTable != "people" ||
		!slices.Equal(fk.RefColumns, []string{"id"}) {
		t.Errorf("the foreign key reads %+v", fk)
	}
	if fk.OnDelete != model.ActionCascade {
		t.Errorf("ON DELETE CASCADE reads as %q", fk.OnDelete)
	}
	if len(tbl.Checks) != 1 {
		t.Fatalf("there are %d checks", len(tbl.Checks))
	}
	// The expression is the point: a check with its name and nothing else
	// says only that something is checked.
	if !strings.Contains(tbl.Checks[0].Expression, "total") {
		t.Errorf("the check reads %q", tbl.Checks[0].Expression)
	}

	byName := map[string]model.Index{}
	for _, ix := range tbl.Indexes {
		byName[ix.Name] = ix
	}
	total, ok := byName["orders_total"]
	if !ok {
		t.Fatalf("the indexes are %v", byName)
	}
	// One column, descending — and not the key column the engine appends
	// to every secondary index so that it can find the row.
	if len(total.Columns) != 1 || total.Columns[0].Name != "total" || !total.Columns[0].Descending {
		t.Errorf("orders_total reads %+v", total.Columns)
	}
	pair, ok := byName["orders_pair"]
	if !ok || len(pair.Columns) != 2 {
		t.Fatalf("orders_pair reads %+v", pair.Columns)
	}
	if pair.Columns[0].Name != "total" || pair.Columns[1].Name != "person_id" {
		t.Errorf("orders_pair is in the order %+v, not the order it was declared", pair.Columns)
	}
	// The primary index is the table's storage, so every other column rides
	// along in it. Those are its payload, not its key.
	pk, ok := byName["orders_pkey"]
	if !ok {
		t.Fatalf("the indexes are %v", byName)
	}
	if len(pk.Columns) != 1 || pk.Columns[0].Name != "id" {
		t.Errorf("the primary index's key reads %+v", pk.Columns)
	}
	if !slices.Equal(pk.Include, []string{"person_id", "total"}) {
		t.Errorf("the primary index carries %q", pk.Include)
	}
	if !pk.Unique {
		t.Error("the primary index is not unique")
	}
}

// A table's comment and a column's are what somebody wrote about them.
func TestLiveTheCommentsAreRead(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), people())
	if err != nil {
		t.Fatal(err)
	}
	tbl := got.(*model.Table)
	if tbl.Comment != "Everyone we know" {
		t.Errorf("the table's comment reads %q", tbl.Comment)
	}
	for _, c := range tbl.Columns {
		if c.Name == "name" && c.Comment != "What they are called" {
			t.Errorf("name's comment reads %q", c.Comment)
		}
	}
}

// A view carries the statement it was made with.
func TestLiveAViewCarriesItsDefinition(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), ref(model.KindView, "adults"))
	if err != nil {
		t.Fatal(err)
	}
	v, ok := got.(*model.View)
	if !ok {
		t.Fatalf("described a view as %T", got)
	}
	if !strings.Contains(v.Definition, "id > 10") {
		t.Errorf("the definition reads %q", v.Definition)
	}
}

// Paging is by the key, so a page is a page and not whatever order the
// engine happened to return (NFR-P11).
func TestLivePagingIsOrderedByTheKey(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	jumbled := ref(model.KindTable, "jumbled")

	var seen []any
	for offset := 0; offset < 3; offset++ {
		rs, err := src.Browse(ctx, jumbled, source.BrowseOptions{Limit: 1, Offset: int64(offset)})
		if err != nil {
			t.Fatal(err)
		}
		rows := drain(t, rs)
		if len(rows) != 1 {
			t.Fatalf("page %d has %d rows", offset, len(rows))
		}
		seen = append(seen, rows[0][0])
	}
	if !slices.Equal(seen, []any{int64(1), int64(2), int64(3)}) {
		t.Errorf("the pages read %v, which is the order they were written", seen)
	}
}

// Paging a sort that has ties is still a page: every row once, and none
// twice. The key is what settles the ties, and without it this engine
// really does return the same row on two pages and skip another.
func TestLivePagingASortWithTiesIsStillAPage(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()

	seen := map[int64]int{}
	total := 0
	for offset := int64(0); ; offset += 10 {
		rs, err := src.Browse(ctx, people(), source.BrowseOptions{
			Limit: 10, Offset: offset, Sorts: []source.Sort{{Column: "ok"}}})
		if err != nil {
			t.Fatal(err)
		}
		rows := drain(t, rs)
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			seen[r[0].(int64)]++
			total++
		}
		if offset > 1000 {
			t.Fatal("the pages never ran out")
		}
	}
	if len(seen) != total {
		var twice []int64
		for id, n := range seen {
			if n > 1 {
				twice = append(twice, id)
			}
		}
		slices.Sort(twice)
		t.Errorf("%d rows over the pages but only %d of them different; these came twice: %v",
			total, len(seen), twice)
	}
}

// A filter means what it means on every engine (FR-3.6).
func TestLiveFiltersSelectWhatTheyMean(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()

	count := func(t *testing.T, opt source.BrowseOptions) int {
		t.Helper()
		opt.Limit = 2000
		rs, err := src.Browse(ctx, people(), opt)
		if err != nil {
			t.Fatal(err)
		}
		return len(drain(t, rs))
	}

	// A per cent somebody typed is a per cent, not a pattern.
	if n := count(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "name", Op: source.OpContains, Values: []any{"son 1%"}}}}); n != 0 {
		t.Errorf("a search for a literal per cent matched %d rows", n)
	}
	if n := count(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "name", Op: source.OpContains, Values: []any{"PERSON 1"}}}}); n == 0 {
		t.Error("a search is not about case and this one found nothing")
	}
	if n := count(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "name", Op: source.OpRegex, Values: []any{"^person 1[0-9]$"}}}}); n != 10 {
		t.Errorf("a regular expression matched %d rows, not 10", n)
	}
	// NULL is a value somebody can pick, and IN cannot say so by itself.
	withNull := count(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "born", Op: source.OpIn, Values: []any{nil}}}})
	nulls := count(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "born", Op: source.OpIsNull}}})
	if withNull != nulls || nulls == 0 {
		t.Errorf("IN (NULL) found %d rows where IS NULL found %d", withNull, nulls)
	}
	// An exact number is filtered by the text it travels in.
	if n := count(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "score", Op: source.OpEqual,
			Values: []any{model.Decimal("12345678901234567890.1234567890")}}}}); n != 1 {
		t.Errorf("filtering by an exact number found %d rows", n)
	}
}

// The picklist offers the values that are there, most frequent first
// (FR-3.7).
func TestLiveTheDistinctValuesAreOffered(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Distinct(context.Background(), ref(model.KindTable, "nokey"), "a",
		source.BrowseOptions{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("the distinct values are %+v", got)
	}
	if got[0].Value != int64(1) || got[0].Count != 2 {
		t.Errorf("the most frequent value is %+v", got[0])
	}
}

// A read-only connection refuses a write in the data layer, not in the
// window, and refuses it twice over (NFR-S4).
//
// The two defences are told apart here because each is the only one that
// can catch its own case. The first is the classifier, which refuses
// before anything is sent; the second is an explicit READ ONLY
// transaction, which is what catches a write the classifier cannot see.
func TestLiveAReadOnlyConnectionRefusesAWrite(t *testing.T) {
	src := open(t, source.Guard{ReadOnly: true})
	// Bounded: a statement left open would hold its connection, and a test
	// that waited for one would hang rather than say what was wrong.
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()

	// run reads a statement to its end and gives back what went wrong,
	// closing the result either way. A result left open holds the
	// connection it came from, and the transaction wrapped round it.
	run := func(sql string) error {
		res, err := src.Query(ctx, source.Statement{SQL: sql})
		if err != nil {
			return err
		}
		if res.Rows == nil {
			return nil
		}
		defer res.Rows.Close()
		for {
			_, err := res.Rows.Next(ctx)
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}

	// The first defence: refused before the server hears of it, and the
	// refusal is the guard's own.
	err := run(`INSERT INTO writes (id, n) VALUES (1, 1)`)
	if err == nil {
		t.Fatal("a read-only connection wrote a row")
	}
	if !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("the refusal came from somewhere else: %v", err)
	}
	if err := run(`SELECT crdb_internal.force_error('XXUUU', 'x')`); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("an internal function was not refused by the guard: %v", err)
	}

	// The second defence: a routine that writes. The classifier reads
	// "SELECT grow()" and has nothing to object to, so what refuses this
	// is the server, inside the transaction this driver wraps it in.
	err = run(`SELECT grow()`)
	if err == nil {
		t.Fatal("a routine wrote a row on a read-only connection")
	}
	if errors.Is(err, source.ErrReadOnly) {
		t.Fatalf("the classifier caught this, so it proves nothing about the server: %v", err)
	}
	var se *source.StatementError
	if !errors.As(err, &se) || se.Message.Code != "25006" {
		t.Errorf("the server refused it as %v", err)
	}

	// And it holds after the session default has been turned off
	// underneath it, which is the whole reason it is there. A routine does
	// the turning off, so the classifier never sees a SET.
	if err := run(`SELECT sneak()`); err != nil {
		t.Fatalf("a routine that changes a setting was refused: %v", err)
	}
	if err := run(`SELECT grow()`); err == nil {
		t.Error("read-only mode was turned off underneath the connection and a row was written")
	}

	// And nothing was written by any of it.
	writes := ref(model.KindTable, "writes")
	plain := opened(t, source.Guard{})
	rs, err := plain.Browse(ctx, writes, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if rows := drain(t, rs); len(rows) != 0 {
		t.Errorf("the table holds %d rows", len(rows))
	}
}

// A script is weighed whole before any of it runs, so a write at the end
// stops the read at the start (NFR-S4).
func TestLiveAScriptIsWeighedWhole(t *testing.T) {
	src := open(t, source.Guard{ReadOnly: true})
	ctx := context.Background()

	out, err := src.QueryMulti(ctx, "SELECT 1;\nINSERT INTO writes (id, n) VALUES (1, 1)",
		source.ScriptOptions{})
	if err == nil {
		// If it was accepted, nothing in it may have run either.
		for r := range out {
			t.Logf("statement %d: err=%v", r.Index, r.Err)
		}
		t.Fatal("a script holding a write was accepted on a read-only connection")
	}
	if !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("the refusal came from somewhere else: %v", err)
	}
}

// A statement is stopped by the session running it, which is what CANCEL
// QUERY takes (ADR-0145).
func TestLiveAStatementIsStoppedByItsSession(t *testing.T) {
	src := open(t, source.Guard{})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ss, err := src.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	handle := ss.Handle()
	if handle == "" {
		t.Fatal("a session has no name to be stopped by")
	}

	// Nothing is running yet, so there is nothing to stop, and saying it
	// stopped something would be an answer nobody could act on.
	if err := src.KillQuery(ctx, handle); err == nil {
		t.Error("stopping an idle session reported that it stopped something")
	}

	done := make(chan error, 1)
	go func() {
		// A sleep, not a large scan. What is being proved is that a
		// statement stops when it is told to, and a statement that costs
		// the server nothing while it waits proves it exactly as well as
		// one that saturates the node — which is what a scan wide enough
		// to outlast the test would do.
		_, err := ss.Query(ctx, source.Statement{SQL: `SELECT pg_sleep(20)`})
		done <- err
	}()
	// The name is readable while the statement is running, which is the
	// only moment anything wants it.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := src.KillQuery(ctx, handle); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("nothing was running to stop")
		}
		time.Sleep(100 * time.Millisecond)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the statement was stopped and finished anyway")
		}
		// Stopped, not failed: a statement somebody stopped is not an
		// error to show them (FR-5.10).
		if !errors.Is(err, context.Canceled) {
			t.Errorf("a stopped statement reads as %v", err)
		}
	case <-ctx.Done():
		t.Fatal("the statement did not stop")
	}
}

// The grid writes rows back, addressed by the key, in one transaction
// (FR-4.4, FR-4.5).
func TestLiveTheGridWritesRows(t *testing.T) {
	build(t)
	src := opened(t, source.Guard{})
	ctx := context.Background()
	writes := ref(model.KindTable, "writes")

	plan, err := src.Plan(ctx, source.Changeset{Target: writes,
		Identity: model.RowIdentity{Kind: model.IdentityPrimaryKey,
			Columns: []string{"id"}, Target: writes},
		Changes: []source.RowChange{
			{Kind: source.ChangeInsert, Values: map[string]any{"id": int64(1), "n": int64(10)}},
			{Kind: source.ChangeInsert, Values: map[string]any{"id": int64(2), "n": int64(20)}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Atomic {
		t.Error("a changeset here is atomic and the plan says it is not")
	}
	out, err := src.Apply(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if out.Err != nil {
		t.Fatalf("writing: %v", out.Err)
	}

	// A row nobody gave a name to takes the column's default.
	rs, err := src.Browse(ctx, writes, source.BrowseOptions{Limit: 10,
		Sorts: []source.Sort{{Column: "id"}}})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	if len(rows) != 2 {
		t.Fatalf("the table holds %d rows", len(rows))
	}
	if rows[0][1] != "none" {
		t.Errorf("the default was not taken: %#v", rows[0][1])
	}
}

// A changeset that fails leaves nothing behind, gives back the connection
// it ran on, and is not attempted again (FR-4.5).
//
// The rollback is all three: without it the half that ran would stay, the
// connection would go back to the pool with a transaction open on it, and
// closing the source would wait for a connection that never comes.
func TestLiveAChangesetThatFailsLeavesNothing(t *testing.T) {
	build(t)
	src := opened(t, source.Guard{})
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	writes := ref(model.KindTable, "writes")
	id := model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: writes}

	// The first row is good; the second leaves out a name the column will
	// not do without. Each takes a number from the sequence as it is
	// attempted, and a sequence does not give its numbers back — which is
	// what says how many times this was tried.
	plan, err := src.Plan(ctx, source.Changeset{Target: writes, Identity: id,
		Changes: []source.RowChange{
			{Kind: source.ChangeInsert, Values: map[string]any{"n": int64(10)}},
			{Kind: source.ChangeInsert, Values: map[string]any{"name": nil}},
		}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := src.Apply(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if out.Err == nil {
		t.Fatal("a row with no name was written")
	}
	if !out.RolledBack {
		t.Error("the changeset failed and says it was not undone")
	}

	// The connection it ran on is back. A transaction nobody ended holds
	// the connection it is on, and closing the source would then wait for
	// one that never comes.
	if n := src.pool.Stat().AcquiredConns(); n != 0 {
		t.Errorf("the failed changeset kept %d connections", n)
	}

	// Nothing of it is there, the first row included.
	rs, err := src.Browse(ctx, writes, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatalf("reading the table again: %v", err)
	}
	if rows := drainWith(ctx, t, rs); len(rows) != 0 {
		t.Errorf("the half that ran was kept: %v", rows)
	}

	// And the connection is still fit to write with. Its number says the
	// failed changeset was attempted once: a permanent failure is not one
	// the server asked to be tried again.
	plan, err = src.Plan(ctx, source.Changeset{Target: writes, Identity: id,
		Changes: []source.RowChange{{Kind: source.ChangeInsert, Values: map[string]any{"n": int64(1)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := src.Apply(ctx, plan); err != nil || out.Err != nil {
		t.Fatalf("the connection could not write again: %v %v", err, out.Err)
	}
	rs, err = src.Browse(ctx, writes, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	rows := drainWith(ctx, t, rs)
	if len(rows) != 1 {
		t.Fatalf("the table holds %d rows", len(rows))
	}
	// Two numbers went to the attempt that failed, and this is the third.
	if got := rows[0][0]; got != int64(3) {
		t.Errorf("the new row is number %v, so the changeset was attempted more than once", got)
	}
}

// A row in a table with no key of its own is written by the key the engine
// made for it.
func TestLiveARowWithNoKeyOfItsOwnIsWritten(t *testing.T) {
	build(t)
	src := opened(t, source.Guard{})
	ctx := context.Background()
	nokey := ref(model.KindTable, "nokey")

	rs, err := src.Browse(ctx, nokey, source.BrowseOptions{Limit: 10,
		Filters: []source.Filter{{Column: "a", Op: source.OpEqual, Values: []any{int64(3)}}}})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	address := rows[0][2] // the hidden key, selected last

	plan, err := src.Plan(ctx, source.Changeset{Target: nokey,
		Identity: model.RowIdentity{Kind: model.IdentityPrimaryKey,
			Columns: []string{"rowid"}, Target: nokey},
		Changes: []source.RowChange{{
			Kind:   source.ChangeUpdate,
			Key:    []any{address},
			Values: map[string]any{"b": "written"},
		}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := src.Apply(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if out.Err != nil {
		t.Fatalf("writing by the engine's own key: %v", out.Err)
	}
	if out.Affected != 1 {
		t.Errorf("the change reached %d rows", out.Affected)
	}
}

// A badge is an estimate the engine gathered, and it is right as soon as
// it is gathered — which the listing's own figure is not, being a cache
// that catches up minutes later (FR-2.5).
func TestLiveABadgeIsAnEstimateOrNothing(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()

	b, ok, err := src.Badge(ctx, people())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("a table measured a moment ago has no badge")
	}
	if b.Exact {
		t.Error("an estimate says it is exact")
	}
	// The newest gather, which counted the ten rows written after the
	// first one. The older gather says 101, and a badge reading that would
	// be a figure from before the rows arrived.
	if b.Text != "111" {
		t.Errorf("people's badge reads %q", b.Text)
	}
	// An empty table is not badged "0": in a listing that figure means
	// both an empty table and one nobody has measured.
	if _, ok, err := src.Badge(ctx, ref(model.KindTable, "writes")); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("an empty table was given a badge")
	}
	// A kind that has no rows to count has no badge at all.
	if _, ok, err := src.Badge(ctx, ref(model.KindSequence, "people_seq")); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("a sequence was given a row count")
	}
}
