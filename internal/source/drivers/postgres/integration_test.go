//go:build conformance

package postgres

// Integration tests against a real PostgreSQL server (REQ-DRV-1, NFR-Q1).
//
// Run with:
//
//	go test -tags conformance ./internal/source/drivers/postgres/
//
// They build their own fixture schema, ikigai_it, and drop it afterwards, so
// they need nothing but a server. Locally they target the ikigai-pg container
// on port 55432 and skip if it is not running; in CI, IKIGAI_REQUIRE_PG=1
// turns a missing server into a failure rather than a silent skip.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func testConfig(readOnly bool) source.ConnectionConfig {
	port, _ := strconv.Atoi(env("IKIGAI_PG_PORT", "55432"))
	return source.ConnectionConfig{
		DriverID: driverID,
		Host:     env("IKIGAI_PG_HOST", "localhost"),
		Port:     port,
		Database: env("IKIGAI_PG_DB", "ikigai_test"),
		User:     env("IKIGAI_PG_USER", "postgres"),
		Secret:   func(string) (string, error) { return env("IKIGAI_PG_PASSWORD", "ikigai"), nil },
		TLS:      source.TLSConfig{Mode: "disable"},
		Guard:    source.Guard{ReadOnly: readOnly, Environment: source.EnvLocal},
	}
}

func dsn() string {
	c := testConfig(false)
	pw, _ := c.Secret("password")
	return fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=disable",
		c.Host, c.Port, c.Database, c.User, pw)
}

var pgUnavailable error

// fixture is created once. orders has a primary key and every awkward type;
// nopk has none; the probe objects exist to prove read-only enforcement.
const fixture = `
DROP SCHEMA IF EXISTS ikigai_it CASCADE;
CREATE SCHEMA ikigai_it;
CREATE TABLE ikigai_it.orders (
    id        bigserial PRIMARY KEY,
    status    text NOT NULL,
    total     numeric(12,2),
    placed_at timestamptz NOT NULL,
    meta      jsonb,
    note      text,
    tags      text[]
);
INSERT INTO ikigai_it.orders (status, total, placed_at, meta, note, tags)
SELECT (ARRAY['pending','paid','shipped','refunded'])[1 + i % 4],
       CASE WHEN i % 7 = 0 THEN NULL ELSE (i % 500) + 0.10 END,
       timestamptz '2024-01-01 00:00:00+00' + i * interval '1 hour',
       CASE WHEN i % 5 = 0 THEN NULL ELSE jsonb_build_object('n', i, 'ratio', 1.0) END,
       CASE WHEN i % 3 = 0 THEN NULL ELSE 'note ' || i END,
       ARRAY['t' || (i % 3), 't' || (i % 5)]
FROM generate_series(1, 2000) AS i;
CREATE TABLE ikigai_it.nopk (a int, b text);
INSERT INTO ikigai_it.nopk SELECT i, 'b' || i FROM generate_series(1, 600) i;
CREATE TABLE ikigai_it.probe_log (at timestamptz DEFAULT now());
CREATE FUNCTION ikigai_it.write_probe() RETURNS int LANGUAGE sql
    AS $$ INSERT INTO ikigai_it.probe_log DEFAULT VALUES RETURNING 1 $$;
CREATE FUNCTION ikigai_it.flip_read_only() RETURNS text LANGUAGE sql
    AS $$ SELECT set_config('default_transaction_read_only', 'off', false) $$;
CREATE VIEW ikigai_it.paid AS SELECT id, total FROM ikigai_it.orders WHERE status = 'paid';
CREATE MATERIALIZED VIEW ikigai_it.totals AS SELECT status, count(*) AS n FROM ikigai_it.orders GROUP BY status;
CREATE FUNCTION ikigai_it.touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$;
CREATE TRIGGER probe_touch BEFORE UPDATE ON ikigai_it.probe_log FOR EACH ROW EXECUTE FUNCTION ikigai_it.touch();
CREATE TYPE ikigai_it.mood AS ENUM ('calm', 'cross');
CREATE DOMAIN ikigai_it.positive AS int CHECK (VALUE > 0);
CREATE TYPE ikigai_it.pair AS (a int, b int);
CREATE TYPE ikigai_it.span AS RANGE (subtype = int4);
CREATE TABLE ikigai_it.order_notes (order_id bigint REFERENCES ikigai_it.orders (id), note text);
CREATE TABLE ikigai_it.events (id int, at date, PRIMARY KEY (id, at)) PARTITION BY RANGE (at);
ANALYZE ikigai_it.orders;
`

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	conn, err := pgx.Connect(ctx, dsn())
	if err == nil {
		_, err = conn.Exec(ctx, fixture)
		conn.Close(ctx)
	}
	cancel()
	pgUnavailable = err

	code := m.Run()

	if pgUnavailable == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if c, err := pgx.Connect(ctx, dsn()); err == nil {
			c.Exec(ctx, "DROP SCHEMA IF EXISTS ikigai_it CASCADE")
			c.Close(ctx)
		}
		cancel()
	}
	os.Exit(code)
}

func requirePG(t *testing.T) {
	t.Helper()
	if pgUnavailable == nil {
		return
	}
	if os.Getenv("IKIGAI_REQUIRE_PG") != "" {
		t.Fatalf("PostgreSQL required but unavailable: %v", pgUnavailable)
	}
	t.Skipf("no PostgreSQL at port %s (docker start ikigai-pg): %v", env("IKIGAI_PG_PORT", "55432"), pgUnavailable)
}

func openSource(t *testing.T, readOnly bool) *pgSource {
	t.Helper()
	requirePG(t)
	src, err := Driver{}.Open(context.Background(), testConfig(readOnly))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { src.Close() })
	return src.(*pgSource)
}

var ordersRef = model.NewRef(model.KindTable, env("IKIGAI_PG_DB", "ikigai_test"), "ikigai_it", "orders")

func drain(t *testing.T, rs model.RowStream) []model.Row {
	t.Helper()
	defer rs.Close()
	var out []model.Row
	for {
		row, err := rs.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		out = append(out, row)
	}
}

// --- the shared battery ------------------------------------------------------

func TestTheTreeListsEachClassOfASchema(t *testing.T) {
	s := openSource(t, false)
	ctx := context.Background()
	schema := model.NewRef(model.KindSchema, testConfig(false).Database, "ikigai_it")
	classes, err := s.Children(ctx, schema)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	count := map[model.ObjectKind]string{}
	for _, c := range classes {
		names = append(names, c.Label)
		k, _ := model.ClassOf(c.Ref)
		count[k] = c.Badge.Text
	}
	if got, want := strings.Join(names, ", "), "Tables, Views, Materialized Views, Indexes, Triggers, Routines, Sequences, Types"; got != want {
		t.Fatalf("classes %q, want %q", got, want)
	}
	list := func(k model.ObjectKind) string {
		kids, err := s.Children(ctx, model.ClassRef(schema, k))
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if n := strconv.Itoa(len(kids)); count[k] != n {
			t.Errorf("%s counted %s, and lists %s", k, count[k], n)
		}
		rows := k == model.KindTable || k == model.KindView || k == model.KindMaterializedView
		var out []string
		for _, n := range kids {
			if n.Ref.Kind != k || n.Browsable != rows {
				t.Errorf("%s holds %v, browsable %v", k, n.Ref, n.Browsable)
			}
			l := n.Label
			if v := n.Attrs["type"]; v != "" {
				l += " (" + v + ")"
			}
			out = append(out, l)
		}
		return strings.Join(out, ", ")
	}
	// Other tests add tables of their own to the schema, with their keys and
	// sequences: those classes are checked for the fixture's alone. The
	// foreign key's own triggers are the system's, as are the tables' row
	// types and the arrays and multirange made beside each type.
	for k, want := range map[model.ObjectKind]string{
		model.KindView:             "paid",
		model.KindMaterializedView: "totals",
		model.KindTrigger:          "probe_touch on probe_log",
		model.KindRoutine:          "flip_read_only(), touch(), write_probe()",
		model.KindUserType:         "mood (enum), pair (composite), positive (domain), span (range)",
	} {
		if got := list(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
	// events is partitioned: its parent is the table, and its key the index.
	for _, c := range []struct {
		k    model.ObjectKind
		want string
	}{
		{model.KindTable, "events"}, {model.KindIndex, "events_pkey on events"},
		{model.KindIndex, "orders_pkey on orders"}, {model.KindSequence, "orders_id_seq"},
	} {
		if got := list(c.k); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q among them", c.k, got, c.want)
		}
	}
	trig, _ := s.Children(ctx, model.ClassRef(schema, model.KindTrigger))
	want := model.NewRef(model.KindTrigger, schema.Path[0], "ikigai_it", "probe_log", "probe_touch")
	if len(trig) != 1 || !trig[0].Ref.Equal(want) || trig[0].Attrs["table"] != "probe_log" {
		t.Errorf("a trigger is addressed by its table: %+v", trig)
	}
}

func TestConformance(t *testing.T) {
	requirePG(t)
	conformance.Run(t, conformance.Target{
		Name:      "postgres",
		Open:      func(ctx context.Context, t *testing.T) source.Source { return openSource(t, false) },
		Browsable: ordersRef,
	})
}

// --- cancellation (FR-5.5, NFR-P9) ---------------------------------------------

func backendRunning(t *testing.T, src *pgSource, pid string) bool {
	t.Helper()
	p, err := src.pool(context.Background(), src.primary)
	if err != nil {
		t.Fatal(err)
	}
	var running bool
	err = p.QueryRow(context.Background(), `
		SELECT COALESCE(bool_or(state = 'active' AND query ILIKE '%pg_sleep%'), false)
		FROM pg_stat_activity WHERE pid = $1::int`, pid).Scan(&running)
	if err != nil {
		t.Fatal(err)
	}
	return running
}

func TestCancelStopsTheQueryOnTheServer(t *testing.T) {
	src := openSource(t, false)
	sess, err := src.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	pid := sess.Handle()
	if _, err := sess.Query(context.Background(), source.Statement{SQL: "CREATE TEMP TABLE survives_cancel (a int)"}); err != nil {
		t.Fatal(err)
	}

	qctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := sess.Query(qctx, source.Statement{SQL: "SELECT pg_sleep(30)"})
		done <- err
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !backendRunning(t, src, pid) {
		if time.Now().After(deadline) {
			t.Fatal("pg_sleep never started")
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancelled := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Query did not return after cancellation")
	}
	returned := time.Since(cancelled)
	t.Logf("query returned %v after cancel", returned.Round(time.Millisecond))
	if returned > 200*time.Millisecond {
		t.Errorf("NFR-P9: cancellation took %v, budget 200ms", returned)
	}

	// The point of the whole exercise: the SERVER stopped, not just the client.
	time.Sleep(100 * time.Millisecond)
	if backendRunning(t, src, pid) {
		t.Error("the query is still running on the server after cancel (FR-5.5)")
	}
	// The session survives: the temp table made before the cancel is still
	// there. pgx's default would have destroyed it along with the whole
	// backend (TestPgxDefaultCancelDestroysTheConnection).
	res, err := sess.Query(context.Background(), source.Statement{SQL: "SELECT count(*) FROM survives_cancel"})
	if err != nil {
		t.Fatalf("session state lost across the cancel: %v", err)
	}
	drain(t, res.Rows)
}

func backendExists(t *testing.T, src *pgSource, pid string) bool {
	t.Helper()
	p, err := src.pool(context.Background(), src.primary)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := p.QueryRow(context.Background(),
		"SELECT count(*) FROM pg_stat_activity WHERE pid = $1::int", pid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// TestPgxDefaultCancelDestroysTheConnection pins the behaviour this driver
// works around (see BuildContextWatcherHandler in source.go): pgx's default
// stops a cancelled query only by tearing down the whole connection, and the
// user's session with it. If pgx changes that, this test fails and the
// workaround can be revisited.
//
// An earlier version concluded the opposite — that the default never reaches
// the server at all — because it looked once, at the instant Exec returned,
// before pgconn's asynchronous teardown had run. Sampled over time the backend
// is still active at +0ms and gone by +5ms. So this test samples, and it
// verifies the query really started before cancelling it.
func TestPgxDefaultCancelDestroysTheConnection(t *testing.T) {
	src := openSource(t, false)
	conn, err := pgx.Connect(context.Background(), dsn()) // pgx defaults, deliberately
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	pid := strconv.FormatUint(uint64(conn.PgConn().PID()), 10)
	t.Cleanup(func() { src.KillQuery(context.Background(), pid) })

	qctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = conn.Exec(qctx, "SELECT pg_sleep(10)")
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !backendRunning(t, src, pid) {
		if time.Now().After(deadline) {
			t.Fatal("pg_sleep never started")
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pgx did not return after cancellation")
	}

	// The whole backend goes, not just the statement.
	deadline = time.Now().Add(time.Second)
	for backendExists(t, src, pid) {
		if time.Now().After(deadline) {
			t.Fatal("the backend survived pgx's default cancel; pgx may now keep the " +
				"connection, and the CancelRequest handler in source.go may no longer be needed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !conn.IsClosed() {
		t.Error("pgx's default left the client connection open after cancel; " +
			"revisit the CancelRequest handler in source.go")
	}
}

func TestKillQueryFromAnotherConnection(t *testing.T) {
	src := openSource(t, false)
	sess, err := src.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	done := make(chan error, 1)
	go func() {
		_, err := sess.Query(context.Background(), source.Statement{SQL: "SELECT pg_sleep(30)"})
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !backendRunning(t, src, sess.Handle()) {
		if time.Now().After(deadline) {
			t.Fatal("pg_sleep never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := src.KillQuery(context.Background(), sess.Handle()); err != nil {
		t.Fatalf("KillQuery: %v", err)
	}
	select {
	case err := <-done:
		var se *source.StatementError
		if !errors.As(err, &se) || se.Message.Code != "57014" {
			t.Errorf("want query_canceled (57014), got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("KillQuery did not stop the query")
	}
}

// --- read-only enforcement (NFR-S4) --------------------------------------------

func TestReadOnlyHasThreeLayers(t *testing.T) {
	ro := openSource(t, true)
	rw := openSource(t, false)
	ctx := context.Background()
	sess, err := ro.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	run := func(sql string) (*source.Result, error) {
		res, err := sess.Query(ctx, source.Statement{SQL: sql})
		if err == nil && res.Rows != nil {
			drain(t, res.Rows)
		}
		return res, err
	}
	sqlstate := func(err error) string {
		var se *source.StatementError
		if errors.As(err, &se) {
			return se.Message.Code
		}
		return ""
	}

	// Layer 1: a visible write is refused before it reaches the server.
	if _, err := run("DELETE FROM ikigai_it.probe_log"); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("layer 1: want ErrReadOnly, got %v", err)
	}

	// Layer 2: a write hidden in a function body passes the lexer, and the
	// server refuses it inside the explicit READ ONLY transaction.
	if got := ro.Classify("SELECT ikigai_it.write_probe()"); got != source.AccessRead {
		t.Fatalf("precondition: the lexer should not see this write, classified %v", got)
	}
	if _, err := run("SELECT ikigai_it.write_probe()"); sqlstate(err) != "25006" {
		t.Errorf("layer 2: want read_only_sql_transaction (25006), got %v", err)
	}

	// The escape layer 3 cannot stop: switch the session default off from
	// inside a function, where the lexer cannot see set_config...
	if _, err := run("SELECT ikigai_it.flip_read_only()"); err != nil {
		t.Fatalf("flip: %v", err)
	}
	res, err := sess.Query(ctx, source.Statement{SQL: "SHOW default_transaction_read_only"})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, res.Rows)
	if len(rows) != 1 || rows[0][0] != "off" {
		t.Fatalf("precondition: the session default should now be off, got %v", rows)
	}

	// ...and layer 2 still holds, because every statement gets its own
	// explicit READ ONLY transaction whatever the default says.
	if _, err := run("SELECT ikigai_it.write_probe()"); sqlstate(err) != "25006" {
		t.Errorf("layer 2 after the escape: want 25006, got %v", err)
	}

	// Nothing reached the table.
	var n int64
	p, _ := rw.pool(ctx, rw.primary)
	if err := p.QueryRow(ctx, "SELECT count(*) FROM ikigai_it.probe_log").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d rows were written through a read-only connection", n)
	}
}

func TestReadOnlyKeepsLegitimateSessionState(t *testing.T) {
	// The wrapper transaction commits rather than rolls back, so an ordinary
	// SET survives into the next statement. Rolling back would silently undo it.
	ro := openSource(t, true)
	sess, err := ro.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if _, err := sess.Query(context.Background(), source.Statement{SQL: "SET search_path = ikigai_it"}); err != nil {
		t.Fatal(err)
	}
	res, err := sess.Query(context.Background(), source.Statement{SQL: "SELECT count(*) FROM orders"})
	if err != nil {
		t.Fatalf("SET search_path did not persist on a read-only session: %v", err)
	}
	if rows := drain(t, res.Rows); len(rows) != 1 || rows[0][0] != int64(2000) {
		t.Errorf("got %v", rows)
	}
}

func TestQueryMultiRefusesWholeScriptUpFront(t *testing.T) {
	ro := openSource(t, true)
	if _, err := ro.QueryMulti(context.Background(),
		"SELECT 1; DELETE FROM ikigai_it.orders", false); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("want the script refused before running, got %v", err)
	}
}

// --- sessions and scripts -------------------------------------------------------

func TestScriptRunsOnOneConnection(t *testing.T) {
	// A temporary table exists only on the connection that created it, so this
	// fails if the statements are spread across the pool.
	src := openSource(t, false)
	ch, err := src.QueryMulti(context.Background(), `
		CREATE TEMP TABLE tt (a int);
		INSERT INTO tt VALUES (1), (2);
		SELECT * FROM tt ORDER BY a;
		SELECT count(*) FROM tt`, false)
	if err != nil {
		t.Fatal(err)
	}
	var results []source.ScriptResult
	for r := range ch {
		if r.Err != nil {
			t.Fatalf("statement %d (%q): %v", r.Index, r.Statement, r.Err)
		}
		results = append(results, r)
	}
	if len(results) != 4 {
		t.Fatalf("got %d results, want 4", len(results))
	}
	if results[1].Result.Affected != 2 {
		t.Errorf("INSERT affected %d, want 2", results[1].Result.Affected)
	}
	if rows := drain(t, results[2].Result.Rows); len(rows) != 2 {
		t.Errorf("buffered SELECT returned %d rows", len(rows))
	}
	if rows := drain(t, results[3].Result.Rows); len(rows) != 1 || rows[0][0] != int64(2) {
		t.Errorf("final SELECT returned %v", rows)
	}
}

func TestStatementErrorCarriesPosition(t *testing.T) {
	src := openSource(t, false)
	_, err := src.Query(context.Background(), source.Statement{SQL: "SELECT nonexistent FROM ikigai_it.orders"})
	var se *source.StatementError
	if !errors.As(err, &se) {
		t.Fatalf("want StatementError, got %T %v", err, err)
	}
	if se.Message.Code != "42703" || se.Message.Position != 8 {
		t.Errorf("code %q position %d, want 42703 at 8", se.Message.Code, se.Message.Position)
	}
}

func TestNamedParameters(t *testing.T) {
	src := openSource(t, false)
	res, err := src.Query(context.Background(), source.Statement{
		SQL:   "SELECT :n::int + :n::int AS twice, ':n is not a param' AS lit",
		Named: map[string]any{"n": 21},
	})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, res.Rows)
	if rows[0][0] != int64(42) || rows[0][1] != ":n is not a param" {
		t.Errorf("got %v", rows[0])
	}
}

// --- browsing -----------------------------------------------------------------

func TestBrowseNarrowsTypesExactly(t *testing.T) {
	src := openSource(t, false)
	rs, err := src.Browse(context.Background(), ordersRef, source.BrowseOptions{
		Filters: []source.Filter{{Column: "id", Op: source.OpEqual, Values: []any{1}}},
		Limit:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	cols := rs.Columns()
	rows := drain(t, rs)
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	got := map[string]any{}
	for i, c := range cols {
		got[c.Name] = rows[0][i]
	}
	if got["id"] != int64(1) {
		t.Errorf("id = %#v", got["id"])
	}
	// Exact decimal text, trailing zero intact — the reason numeric is read
	// in text format.
	if got["total"] != model.Decimal("1.10") {
		t.Errorf("total = %#v, want Decimal(\"1.10\")", got["total"])
	}
	// jsonb as the server holds it: 1.0 stays 1.0 rather than becoming 1.
	if j, ok := got["meta"].(model.JSON); !ok || !strings.Contains(string(j), "1.0") {
		t.Errorf("meta = %#v", got["meta"])
	}
	if _, ok := got["placed_at"].(time.Time); !ok {
		t.Errorf("placed_at = %T", got["placed_at"])
	}
	if a, ok := got["tags"].([]any); !ok || len(a) != 2 {
		t.Errorf("tags = %#v", got["tags"])
	}
}

func TestBrowsePagingIsStable(t *testing.T) {
	// Sorted by a column with only four distinct values, pages without a
	// tiebreaker repeat and skip rows.
	src := openSource(t, false)
	seen := map[int64]bool{}
	for page := int64(0); page < 8; page++ {
		rs, err := src.Browse(context.Background(), ordersRef, source.BrowseOptions{
			Sorts: []source.Sort{{Column: "status"}}, Limit: 256, Offset: page * 256,
		})
		if err != nil {
			t.Fatal(err)
		}
		idCol := -1
		for i, c := range rs.Columns() {
			if c.Name == "id" {
				idCol = i
			}
		}
		for _, row := range drain(t, rs) {
			id := row[idCol].(int64)
			if seen[id] {
				t.Fatalf("row %d appeared on two pages", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != 2000 {
		t.Errorf("paged through %d distinct rows, want 2000", len(seen))
	}
}

func TestBrowseTableWithoutPrimaryKey(t *testing.T) {
	src := openSource(t, false)
	ref := model.NewRef(model.KindTable, ordersRef.Path[0], "ikigai_it", "nopk")
	seen := map[int64]bool{}
	for page := int64(0); page < 3; page++ {
		rs, err := src.Browse(context.Background(), ref, source.BrowseOptions{Limit: 256, Offset: page * 256})
		if err != nil {
			t.Fatal(err)
		}
		if id, ok := rs.(model.Identified); !ok || id.Identity().Editable() {
			t.Error("rows of a table without a key must not be editable (FR-4.7)")
		}
		for _, row := range drain(t, rs) {
			seen[row[0].(int64)] = true
		}
	}
	if len(seen) != 600 {
		t.Errorf("paged through %d distinct rows, want 600", len(seen))
	}
}

func TestEveryFilterOperatorProducesValidSQL(t *testing.T) {
	// The unit tests check the SQL text; this checks PostgreSQL accepts it.
	src := openSource(t, false)
	filters := []source.Filter{
		{Column: "status", Op: source.OpEqual, Values: []any{"paid"}},
		{Column: "status", Op: source.OpNotEqual, Values: []any{"paid"}},
		{Column: "total", Op: source.OpGreater, Values: []any{10}},
		{Column: "total", Op: source.OpBetween, Values: []any{10, 20}},
		{Column: "note", Op: source.OpLike, Values: []any{"note 1%"}},
		{Column: "note", Op: source.OpNotLike, Values: []any{"note 1%"}},
		{Column: "note", Op: source.OpContains, Values: []any{"50%_"}},
		{Column: "note", Op: source.OpRegex, Values: []any{"^note [0-9]+$"}},
		{Column: "total", Op: source.OpIsNull},
		{Column: "total", Op: source.OpIsNotNull},
		{Column: "status", Op: source.OpIn, Values: []any{"paid", "shipped", nil}},
		{Column: "status", Op: source.OpNotIn, Values: []any{"paid"}},
		{Column: "note", Op: source.OpEqual, Values: []any{nil}},
		{Column: "id", Op: source.OpLess, Values: []any{100}, Negate: true},
	}
	for _, f := range filters {
		rs, err := src.Browse(context.Background(), ordersRef, source.BrowseOptions{
			Filters: []source.Filter{f}, Limit: 5,
		})
		if err != nil {
			t.Errorf("%s %v: %v", f.Op, f.Values, err)
			continue
		}
		drain(t, rs)
	}
}

func TestNotInKeepsNullRows(t *testing.T) {
	// The picklist semantics, against the real server.
	src := openSource(t, false)
	count := func(f source.Filter) int {
		rs, err := src.Browse(context.Background(),
			model.NewRef(model.KindTable, ordersRef.Path[0], "ikigai_it", "orders"),
			source.BrowseOptions{Filters: []source.Filter{f}, Columns: []string{"id"}, Limit: 5000})
		if err != nil {
			t.Fatal(err)
		}
		return len(drain(t, rs))
	}
	all := count(source.Filter{Column: "total", Op: source.OpIsNotNull}) +
		count(source.Filter{Column: "total", Op: source.OpIsNull})
	excl := count(source.Filter{Column: "total", Op: source.OpNotIn, Values: []any{"1.10"}})
	if excl != all-count(source.Filter{Column: "total", Op: source.OpEqual, Values: []any{"1.10"}}) {
		t.Errorf("NOT IN dropped rows it was not asked to: %d of %d", excl, all)
	}
}

// --- connection errors (FR-1.4) --------------------------------------------------

func TestConnectErrorsSayWhatToFix(t *testing.T) {
	requirePG(t)
	cases := []struct {
		name string
		mut  func(*source.ConnectionConfig)
		want source.ConnectKind
	}{
		{"wrong password", func(c *source.ConnectionConfig) {
			c.Secret = func(string) (string, error) { return "wrong", nil }
		}, source.ConnectAuth},
		{"missing database", func(c *source.ConnectionConfig) { c.Database = "no_such_db" }, source.ConnectNoDatabase},
		{"nothing listening", func(c *source.ConnectionConfig) { c.Port = 1 }, source.ConnectUnreachable},
		{"unresolvable host", func(c *source.ConnectionConfig) { c.Host = "no-such-host.invalid" }, source.ConnectUnreachable},
		{"TLS demanded of a plaintext server", func(c *source.ConnectionConfig) { c.TLS.Mode = "verify-full" }, source.ConnectTLS},
	}
	for _, c := range cases {
		cfg := testConfig(false)
		c.mut(&cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_, err := Driver{}.Open(ctx, cfg)
		cancel()
		var ce *source.ConnectError
		if !errors.As(err, &ce) {
			t.Errorf("%s: want ConnectError, got %T %v", c.name, err, err)
			continue
		}
		if ce.Kind != c.want {
			t.Errorf("%s: kind %d, want %d (%v)", c.name, ce.Kind, c.want, err)
		}
		if strings.Contains(err.Error(), "wrong") && c.name == "wrong password" {
			t.Errorf("%s: the password leaked into the error: %v", c.name, err)
		}
	}
}
