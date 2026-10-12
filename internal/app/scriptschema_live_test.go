//go:build conformance

package app

// A whole schema's DDL, in an order it can be run in (FR-6.7).
//
// The claim is ScriptSchema's, not a driver's: the statements come out in an
// order a server will accept, which needs two tables that refer to each other
// and two views whose build order is the opposite of their names. Only a server
// can settle it, so it runs against PostgreSQL — and it lives here rather than
// in the driver's own suite, because a driver package that imports internal/app
// is the dependency pointing the wrong way (ARCH-1, ADR-0170).

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// livePG is a PostgreSQL source opened through the registry, as the application
// opens one.
//
// It is returned as the source itself rather than wrapped in something narrower,
// because what the application does with a source is ask it what else it is —
// ScriptSchema asks for a DDLGenerator — and a wrapper hides every interface it
// was not built to carry.
func livePG(t *testing.T) source.Source {
	t.Helper()
	d, err := source.Lookup("postgres")
	if err != nil {
		t.Fatalf("the PostgreSQL driver is not registered: %v", err)
	}
	c := pgConn()
	cfg := source.ConnectionConfig{DriverID: "postgres", Host: c.Host, Port: c.Port,
		Database: c.Database, User: c.User, TLS: source.TLSConfig{Mode: "disable"},
		Secret: func(string) (string, error) { return envOr("IKIGAI_PG_PASSWORD", "ikigai"), nil }}
	src, err := d.Open(context.Background(), cfg)
	if err != nil {
		if envOr("IKIGAI_REQUIRE_PG", "") != "" {
			t.Fatalf("PostgreSQL required but unavailable: %v", err)
		}
		t.Skipf("no PostgreSQL (docker start ikigai-pg): %v", err)
	}
	t.Cleanup(func() { _ = src.Close() })
	return src
}

// sessionOn is a session for running the fixture's own statements.
func sessionOn(t *testing.T, src source.Source, ctx context.Context) source.Session {
	t.Helper()
	sn, ok := src.(source.Sessioner)
	if !ok {
		t.Fatal("the PostgreSQL driver runs statements and does not offer sessions")
	}
	ss, err := sn.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

// oneValue is the first column of the first row, for a count.
func oneValue(t *testing.T, res *source.Result) string {
	t.Helper()
	if res == nil || res.Rows == nil {
		t.Fatal("no rows came back")
	}
	defer res.Rows.Close()
	row, err := res.Rows.Next(context.Background())
	if err != nil || len(row) == 0 {
		t.Fatalf("reading one value: %v", err)
	}
	return fmt.Sprint(row[0])
}
func TestAWholeSchemaIsWrittenInAnOrderThatRuns(t *testing.T) {
	src := livePG(t)
	stamp := time.Now().UnixNano() % 100000
	// Two tables that refer to each other, which no order of CREATE TABLE
	// could satisfy; and two views where the one that must be created first
	// sorts last by name.
	a := fmt.Sprintf("sch_a_%d", stamp)
	b := fmt.Sprintf("sch_b_%d", stamp)
	base := fmt.Sprintf("sch_zz_%d", stamp)
	over := fmt.Sprintf("sch_aa_%d", stamp)
	mine := []string{a, b, base, over}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	session := sessionOn(t, src, ctx)
	defer session.Close()
	run := func(sql string) error {
		res, err := session.Query(ctx, source.Statement{SQL: sql})
		if res != nil && res.Rows != nil {
			res.Rows.Close()
		}
		return err
	}
	must := func(sql string) {
		t.Helper()
		if err := run(sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	// On a session of its own, every time. This runs from t.Cleanup as well
	// as from the body, and a cleanup runs after the test's own deferred
	// Close — so a drop sharing that session would quietly do nothing and
	// leave these in the fixture schema for the next test to trip over.
	drop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s := sessionOn(t, src, ctx)
		defer s.Close()
		for _, sql := range []string{
			fmt.Sprintf(`DROP VIEW IF EXISTS "ikigai_it"."%s"`, over),
			fmt.Sprintf(`DROP VIEW IF EXISTS "ikigai_it"."%s"`, base),
			fmt.Sprintf(`DROP TABLE IF EXISTS "ikigai_it"."%s" CASCADE`, a),
			fmt.Sprintf(`DROP TABLE IF EXISTS "ikigai_it"."%s" CASCADE`, b),
		} {
			res, err := s.Query(ctx, source.Statement{SQL: sql})
			if res != nil && res.Rows != nil {
				res.Rows.Close()
			}
			if err != nil {
				t.Errorf("%s: %v", sql, err)
			}
		}
	}
	t.Cleanup(drop)

	// The fixture schema is the driver suite's, and this test no longer lives
	// beside it, so it asks for it rather than assuming somebody else ran first.
	must(`CREATE SCHEMA IF NOT EXISTS "ikigai_it"`)
	must(fmt.Sprintf(`CREATE TABLE "ikigai_it"."%s" (id integer PRIMARY KEY, b_id integer)`, a))
	must(fmt.Sprintf(`CREATE TABLE "ikigai_it"."%s" (id integer PRIMARY KEY, a_id integer)`, b))
	must(fmt.Sprintf(`ALTER TABLE "ikigai_it"."%s" ADD CONSTRAINT "%s_b_fkey"
		FOREIGN KEY (b_id) REFERENCES "ikigai_it"."%s"(id)`, a, a, b))
	must(fmt.Sprintf(`ALTER TABLE "ikigai_it"."%s" ADD CONSTRAINT "%s_a_fkey"
		FOREIGN KEY (a_id) REFERENCES "ikigai_it"."%s"(id)`, b, b, a))
	must(fmt.Sprintf(`CREATE VIEW "ikigai_it"."%s" AS SELECT id FROM "ikigai_it"."%s"`, base, a))
	must(fmt.Sprintf(`CREATE VIEW "ikigai_it"."%s" AS SELECT id FROM "ikigai_it"."%s"`, over, base))

	stmts, err := ScriptSchema(ctx, src,
		model.NewRef(model.KindSchema, envOr("IKIGAI_PG_DB", "ikigai_test"), "ikigai_it"))
	if err != nil {
		t.Fatalf("scripting the schema: %v", err)
	}

	// The view that must exist first is written first, although it sorts
	// last by name and the catalogue lists it last.
	// Matched on the statement that creates each one, not on any mention of
	// the name: the view over it names it in its own body, so "the first
	// statement mentioning it" would be satisfied by the wrong order.
	if i, j := creates(stmts, base), creates(stmts, over); i < 0 || j < 0 || i > j {
		t.Errorf("%s is created at %d and the view over it at %d", base, i, j)
	}

	// Now the claim itself: drop the four and run back only the statements
	// that build them, in the order they came out in.
	drop()
	var ran int
	for _, st := range stmts {
		if !mentionsAny(st.SQL, mine) {
			continue
		}
		if err := run(st.SQL); err != nil {
			t.Fatalf("statement %d of the script did not run:\n\t%s\n%v", ran+1, st.SQL, err)
		}
		ran++
	}
	if ran < 6 {
		t.Fatalf("only %d statements of the script were for these objects", ran)
	}
	// And what it built is what was there: both references are back.
	res, err := session.Query(ctx, source.Statement{SQL: fmt.Sprintf(`
		SELECT count(*) FROM pg_constraint WHERE contype = 'f' AND conname IN ('%s_b_fkey', '%s_a_fkey')`, a, b)})
	if err != nil {
		t.Fatal(err)
	}
	if got := oneValue(t, res); got != "2" {
		t.Errorf("%s of the two references came back", got)
	}
}

// creates is where the statement that builds this view is, which is not the
// same as where its name first appears.
func creates(stmts []source.Statement, name string) int {
	want := `VIEW "ikigai_it"."` + name + `"`
	for i, s := range stmts {
		if strings.Contains(s.SQL, want) {
			return i
		}
	}
	return -1
}

func mentionsAny(sql string, names []string) bool {
	for _, n := range names {
		if strings.Contains(sql, n) {
			return true
		}
	}
	return false
}
