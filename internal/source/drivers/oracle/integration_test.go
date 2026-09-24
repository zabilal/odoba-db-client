//go:build conformance

package oracle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/sijms/go-ora/v2"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// Integration tests against a real Oracle (REQ-DRV-1, T3.32). They expect
// the ikigai-oracle container on port 51521 and skip if it is not running;
// IKIGAI_REQUIRE_ORACLE=1 makes that a failure, as in CI.

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func port() int {
	if v := os.Getenv("IKIGAI_ORACLE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 51521
}

func service() string  { return env("IKIGAI_ORACLE_SERVICE", "FREEPDB1") }
func user() string     { return env("IKIGAI_ORACLE_USER", "IKIGAI") }
func password() string { return env("IKIGAI_ORACLE_PASSWORD", "ikigai") }

func config(guard source.Guard) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: port(),
		Database: service(), User: user(),
		Secret: func(string) (string, error) { return password(), nil },
		TLS:    source.TLSConfig{Mode: "disable"}, Guard: guard}
}

// admin is a plain connection, for building the fixture.
func admin(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("oracle", fmt.Sprintf("oracle://%s:%s@127.0.0.1:%d/%s",
		user(), password(), port(), service()))
	if err == nil {
		err = db.Ping()
	}
	if err != nil {
		if db != nil {
			db.Close()
		}
		if os.Getenv("IKIGAI_REQUIRE_ORACLE") != "" {
			t.Fatalf("Oracle required but unavailable: %v", err)
		}
		t.Skipf("no Oracle on port %d (docker start ikigai-oracle): %v", port(), err)
	}
	return db
}

// clear empties the schema. Oracle has no DROP … IF EXISTS, and a table
// dropped without PURGE lingers in the recycle bin where the catalogue can
// still see it — so what the fixture counts would depend on what ran before
// it.
func clear(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, q := range []string{
		`SELECT 'DROP VIEW "' || view_name || '"' FROM user_views`,
		`SELECT 'DROP TABLE "' || table_name || '" CASCADE CONSTRAINTS PURGE' FROM user_tables`,
		`SELECT 'DROP SEQUENCE "' || sequence_name || '"' FROM user_sequences`,
	} {
		rows, err := db.Query(q)
		if err != nil {
			t.Fatalf("listing what to drop: %v", err)
		}
		var ddl []string
		for rows.Next() {
			var stmt string
			if err := rows.Scan(&stmt); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			ddl = append(ddl, stmt)
		}
		rows.Close()
		for _, stmt := range ddl {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
	}
	if _, err := db.Exec(`PURGE RECYCLEBIN`); err != nil {
		t.Fatalf("emptying the recycle bin: %v", err)
	}
}

var fixture = []string{
	`CREATE TABLE people (
	   id NUMBER(10) CONSTRAINT people_pk PRIMARY KEY,
	   name VARCHAR2(50) NOT NULL,
	   score NUMBER(30,10),
	   born DATE,
	   seen TIMESTAMP(6) WITH TIME ZONE,
	   meta VARCHAR2(200),
	   tag RAW(16),
	   ok NUMBER(1))`,
	`COMMENT ON TABLE people IS 'Everyone we know'`,
	`COMMENT ON COLUMN people.name IS 'What they are called'`,
	`INSERT INTO people (id, name, score, born, meta, ok)
	 SELECT LEVEL, 'person ' || LEVEL, LEVEL * 1.5,
	        CASE WHEN MOD(LEVEL, 3) = 0 THEN DATE '2000-01-01' + MOD(LEVEL, 2) END,
	        '{"i": ' || LEVEL || '}', MOD(LEVEL, 2)
	 FROM dual CONNECT BY LEVEL <= 100`,
	`INSERT INTO people (id, name, score, tag)
	 VALUES (1000, 'exact', 12345678901234567890.1234567890, HEXTORAW('6F9619FF8B86D011B42D00C04FC964FF'))`,
	`CREATE TABLE orders (
	   id NUMBER(10) CONSTRAINT orders_pk PRIMARY KEY,
	   person_id NUMBER(10),
	   total NUMBER(10,2),
	   CONSTRAINT ck_total CHECK (total >= 0),
	   CONSTRAINT fk_person FOREIGN KEY (person_id) REFERENCES people (id) ON DELETE CASCADE)`,
	`CREATE INDEX orders_total ON orders (total DESC)`,
	// An index of two columns, so that reading one in the catalogue's order
	// rather than the index's would read it wrongly.
	`CREATE INDEX orders_pair ON orders (total, person_id)`,
	// A key of two columns, declared in the other order from the table's,
	// and a key that refers to it.
	`CREATE TABLE pair (a NUMBER(10), b NUMBER(10), c NUMBER(10),
	   CONSTRAINT pair_pk PRIMARY KEY (b, a))`,
	`INSERT INTO pair VALUES (1, 2, 3)`,
	`INSERT INTO pair VALUES (2, 1, 4)`,
	`CREATE TABLE pairref (x NUMBER(10), y NUMBER(10),
	   CONSTRAINT fk_pair FOREIGN KEY (y, x) REFERENCES pair (b, a))`,
	// Rows written out of their key's order, so that a browse which did not
	// order by the key would read them in the order they were written.
	`CREATE TABLE jumbled (id NUMBER(10) CONSTRAINT jumbled_pk PRIMARY KEY, v VARCHAR2(10))`,
	`INSERT INTO jumbled VALUES (3, 'three')`,
	`INSERT INTO jumbled VALUES (1, 'one')`,
	`INSERT INTO jumbled VALUES (2, 'two')`,
	`CREATE TABLE writes (id NUMBER(10), name VARCHAR2(40) DEFAULT 'none' NOT NULL, n NUMBER(10))`,
	// A large object lives in a table of its own: Oracle will not group or
	// order by one, so it is not a column the picklist can offer.
	`CREATE TABLE docs (id NUMBER(10) CONSTRAINT docs_pk PRIMARY KEY, body CLOB)`,
	`INSERT INTO docs VALUES (1, 'the first document')`,
	`INSERT INTO docs VALUES (2, 'the second document')`,
	`CREATE TABLE nokey (a NUMBER(10), b VARCHAR2(10))`,
	`INSERT INTO nokey VALUES (2, 'y')`,
	`INSERT INTO nokey VALUES (1, 'x')`,
	`INSERT INTO nokey VALUES (1, 'x')`,
	`INSERT INTO nokey VALUES (3, 'z')`,
	`CREATE SEQUENCE people_seq START WITH 2000`,
	`CREATE VIEW adults AS SELECT id, name FROM people WHERE id > 10`,
	`COMMIT`,
}

// build makes the fixture, fresh, before each test that reads it.
func build(t *testing.T) {
	t.Helper()
	db := admin(t)
	defer db.Close()
	clear(t, db)
	for _, stmt := range fixture {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("fixture %.60q: %v", stmt, err)
		}
	}
	// The optimiser's row counts are what a badge shows, and nothing has
	// gathered them for a table made a moment ago.
	if _, err := db.Exec(`BEGIN DBMS_STATS.GATHER_SCHEMA_STATS(USER); END;`); err != nil {
		t.Fatalf("gathering statistics: %v", err)
	}
}

func open(t *testing.T, guard source.Guard) *oracleSource {
	t.Helper()
	build(t)
	return opened(t, guard)
}

// opened is a connection on the fixture as it stands, without making it
// again: a test that writes needs its other connections to see the rows it
// has already written.
func opened(t *testing.T, guard source.Guard) *oracleSource {
	t.Helper()
	src, err := Driver{}.Open(context.Background(), config(guard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	return src.(*oracleSource)
}

var people = model.NewRef(model.KindTable, "IKIGAI", "PEOPLE")

func drain(t *testing.T, rs model.RowStream) []model.Row {
	t.Helper()
	defer rs.Close()
	var out []model.Row
	for {
		r, err := rs.Next(context.Background())
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
}

func TestConformance(t *testing.T) {
	build(t)
	conformance.Run(t, conformance.Target{
		Name: "oracle",
		Open: func(ctx context.Context, t *testing.T) source.Source {
			src, err := Driver{}.Open(ctx, config(source.Guard{}))
			if err != nil {
				t.Fatal(err)
			}
			return src
		},
		Browsable: people,
		OpenGuarded: func(ctx context.Context, t *testing.T, g source.Guard) source.Source {
			src, err := Driver{}.Open(ctx, config(g))
			if err != nil {
				t.Fatal(err)
			}
			return src
		},
	})
}

// A first look at what the driver hands over, so the narrowing is written
// against it rather than against its documentation.
func TestLiveWhatTheDriverHandsOver(t *testing.T) {
	src := open(t, source.Guard{})
	rs, err := src.Browse(context.Background(), people, source.BrowseOptions{Limit: 1,
		Filters: []source.Filter{{Column: "ID", Op: source.OpEqual, Values: []any{int64(1000)}}}})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	for i, c := range rs.Columns() {
		t.Logf("%-6s native=%-16s class=%-10v value=%#v", c.Name, c.Type.Native, c.Type.Class, rows[0][i])
	}
	_ = time.Second
	_ = strings.TrimSpace
	_ = slices.Equal[[]string]
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

// The explorer walks a service, a schema and a table (FR-2.1). A schema
// here is a user: Oracle has no separate idea of one.
func TestLiveTheExplorerWalksTheService(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()

	roots, err := src.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ours := find(roots, "IKIGAI")
	if ours == nil {
		t.Fatalf("the schemas are %q", labels(roots))
	}
	if ours.Attrs["current"] != "true" {
		t.Errorf("the schema this connection is on is not marked: %+v", ours.Attrs)
	}
	if sys := find(roots, "SYS"); sys == nil || sys.Attrs["system"] != "true" {
		t.Errorf("SYS is not marked as the database's own: %+v", sys)
	}

	folders, err := src.Children(ctx, ours.Ref)
	if err != nil {
		t.Fatal(err)
	}
	tables := find(folders, "Tables")
	if tables == nil {
		t.Fatalf("the classes are %q", labels(folders))
	}
	if tables.Badge == nil || tables.Badge.Text != "8" {
		t.Errorf("the tables folder counts %+v, want the eight in the fixture", tables.Badge)
	}
	if v := find(folders, "Views"); v == nil || v.Badge == nil || v.Badge.Text != "1" {
		t.Errorf("the views folder is %+v", v)
	}
	if find(folders, "Sequences") == nil {
		t.Errorf("the sequence in the fixture is nowhere: %q", labels(folders))
	}

	objects, err := src.Children(ctx, tables.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := labels(objects); !slices.IsSorted(got) {
		t.Errorf("the tables are listed %q, want them in order", got)
	}
	p := find(objects, "PEOPLE")
	if p == nil {
		t.Fatalf("the tables are %q", labels(objects))
	}
	if !p.Browsable || !p.HasChildren {
		t.Errorf("PEOPLE is %+v, want it browsable and openable", p)
	}

	cols, err := src.Children(ctx, p.Ref)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ID", "NAME", "SCORE", "BORN", "SEEN", "META", "TAG", "OK"}
	if got := labels(cols); !slices.Equal(got, want) {
		t.Fatalf("the columns are %q, want %q", got, want)
	}
	if cols[0].Attrs["key"] != "primary" || cols[0].Attrs["type"] != "NUMBER(10)" {
		t.Errorf("ID is %+v", cols[0].Attrs)
	}
	if cols[1].Attrs["type"] != "VARCHAR2(50)" || cols[1].Attrs["nullable"] != "false" {
		t.Errorf("NAME is %+v", cols[1].Attrs)
	}
	if cols[2].Attrs["type"] != "NUMBER(30,10)" {
		t.Errorf("SCORE is %+v", cols[2].Attrs)
	}
	if cols[1].Attrs["key"] != "" {
		t.Errorf("NAME is no part of the key and says %q", cols[1].Attrs["key"])
	}
}

// A table's structure is what the structure tab and the diff engine need.
func TestLiveATableIsDescribed(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), model.NewRef(model.KindTable, "IKIGAI", "ORDERS"))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := got.(*model.Table)
	if !ok {
		t.Fatalf("a table is described as %T", got)
	}
	if tbl.PrimaryKey == nil || !slices.Equal(tbl.PrimaryKey.Columns, []string{"ID"}) {
		t.Errorf("the primary key is %+v", tbl.PrimaryKey)
	}
	if len(tbl.ForeignKeys) != 1 {
		t.Fatalf("the foreign keys are %+v", tbl.ForeignKeys)
	}
	fk := tbl.ForeignKeys[0]
	if fk.Name != "FK_PERSON" || fk.RefTable != "PEOPLE" ||
		!slices.Equal(fk.Columns, []string{"PERSON_ID"}) || !slices.Equal(fk.RefColumns, []string{"ID"}) {
		t.Errorf("the foreign key is %+v", fk)
	}
	if fk.OnDelete != model.ActionCascade {
		t.Errorf("the foreign key deletes %q", fk.OnDelete)
	}
	// Oracle has no ON UPDATE at all: a key's parent cannot change under
	// it, so there is nothing to report but no action.
	if fk.OnUpdate != model.ActionNoAction {
		t.Errorf("the foreign key updates %q", fk.OnUpdate)
	}
	var ix *model.Index
	for i := range tbl.Indexes {
		if tbl.Indexes[i].Name == "ORDERS_TOTAL" {
			ix = &tbl.Indexes[i]
		}
	}
	if ix == nil || len(ix.Columns) != 1 {
		t.Errorf("the indexes are %+v", tbl.Indexes)
	}
	// Exactly the one somebody wrote: Oracle makes a check of its own for
	// every NOT NULL column, and those are the server's rather than
	// anybody's to read.
	if len(tbl.Checks) != 1 || tbl.Checks[0].Name != "CK_TOTAL" {
		t.Errorf("the checks are %+v, want the one that was written", tbl.Checks)
	}
	// Oracle makes a check of its own for every NOT NULL column, and those
	// are the server's rather than anybody's to read: people has one on
	// its name and no constraint somebody wrote.
	got, err = src.Describe(context.Background(), people)
	if err != nil {
		t.Fatal(err)
	}
	if checks := got.(*model.Table).Checks; len(checks) != 0 {
		t.Errorf("a table nobody wrote a check on has %+v", checks)
	}
	if ix != nil && len(ix.Columns) != 1 {
		t.Errorf("a one-column index has %d columns", len(ix.Columns))
	}
	var pair *model.Index
	for i := range tbl.Indexes {
		if tbl.Indexes[i].Name == "ORDERS_PAIR" {
			pair = &tbl.Indexes[i]
		}
	}
	if pair == nil || len(pair.Columns) != 2 ||
		pair.Columns[0].Name != "TOTAL" || pair.Columns[1].Name != "PERSON_ID" {
		t.Errorf("the index of two columns is %+v, want them in the index's order", pair)
	}
	// A key's index is not listed as an index of its own.
	for _, i := range tbl.Indexes {
		if i.Name == "ORDERS_PK" {
			t.Errorf("a key's index is listed as an index: %+v", tbl.Indexes)
		}
	}
}

// What somebody wrote about a table and its columns is shown with them.
func TestLiveWhatWasWrittenAboutATableIsShown(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), people)
	if err != nil {
		t.Fatal(err)
	}
	tbl := got.(*model.Table)
	if tbl.Comment != "Everyone we know" {
		t.Errorf("the table says %q", tbl.Comment)
	}
	if tbl.RowsEstimate != 101 {
		t.Errorf("the table is said to hold %d rows", tbl.RowsEstimate)
	}
	for _, c := range tbl.Columns {
		if c.Name == "NAME" && c.Comment != "What they are called" {
			t.Errorf("the NAME column says %q", c.Comment)
		}
		if c.Name == "ID" && c.Comment != "" {
			t.Errorf("a column nobody wrote about says %q", c.Comment)
		}
	}
}

func TestLiveAViewIsDescribed(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), model.NewRef(model.KindView, "IKIGAI", "ADULTS"))
	if err != nil {
		t.Fatal(err)
	}
	v, ok := got.(*model.View)
	if !ok {
		t.Fatalf("a view is described as %T", got)
	}
	if !strings.Contains(strings.ToUpper(v.Definition), "PEOPLE") {
		t.Errorf("the view is defined as %q", v.Definition)
	}
	if len(v.Columns) != 2 {
		t.Errorf("the view has %d columns", len(v.Columns))
	}
	if _, err := src.Describe(context.Background(), model.NewRef(model.KindTable, "IKIGAI", "NOT_THERE")); err == nil {
		t.Error("a table that is not there was described")
	}
}

// A key of several columns is read in the order it was declared in, which
// is the order rows are addressed and paged by.
func TestLiveAKeyOfSeveralColumnsKeepsItsOrder(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	pair := model.NewRef(model.KindTable, "IKIGAI", "PAIR")
	got, err := src.Describe(ctx, pair)
	if err != nil {
		t.Fatal(err)
	}
	tbl := got.(*model.Table)
	if tbl.PrimaryKey == nil || !slices.Equal(tbl.PrimaryKey.Columns, []string{"B", "A"}) {
		t.Errorf("the primary key is %+v, want the order it was declared in", tbl.PrimaryKey)
	}
	rs, err := src.Browse(ctx, pair, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	if id := model.IdentityOf(rs); !slices.Equal(id.Columns, []string{"B", "A"}) {
		t.Errorf("the rows are known by %+v", id)
	}
	// Ordered by b and then a, so the row with the smaller b comes first.
	if len(rows) != 2 || rows[0][0] != int64(2) {
		t.Errorf("the rows came in the order %v", rows)
	}
	ref, err := src.Describe(ctx, model.NewRef(model.KindTable, "IKIGAI", "PAIRREF"))
	if err != nil {
		t.Fatal(err)
	}
	fks := ref.(*model.Table).ForeignKeys
	if len(fks) != 1 || !slices.Equal(fks[0].Columns, []string{"Y", "X"}) ||
		!slices.Equal(fks[0].RefColumns, []string{"B", "A"}) {
		t.Errorf("the foreign key is %+v, want the order it was declared in", fks)
	}
}

// A browse reads rows in the key's order, not in the order they were
// written.
func TestLivePagingOrdersByTheKeyAndNotByTheDisk(t *testing.T) {
	src := open(t, source.Guard{})
	rs, err := src.Browse(context.Background(), model.NewRef(model.KindTable, "IKIGAI", "JUMBLED"),
		source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, row := range drain(t, rs) {
		got = append(got, row[0].(int64))
	}
	if !slices.Equal(got, []int64{1, 2, 3}) {
		t.Errorf("the rows came in the order %v, want the key's", got)
	}
}

// A reference naming no schema names nothing that can be read.
func TestLiveAnIncompleteReferenceIsRefused(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	ref := model.NewRef(model.KindTable, "PEOPLE")
	if _, err := src.Browse(ctx, ref, source.BrowseOptions{Limit: 1}); err == nil {
		t.Error("a reference naming no schema was browsable")
	}
	if _, err := src.Describe(ctx, ref); err == nil {
		t.Error("a reference naming no schema was described")
	}
}

// Paging follows the key where there is one.
func TestLivePagingFollowsTheKey(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	var seen []int64
	for off := int64(0); off < 4; off++ {
		rs, err := src.Browse(ctx, people, source.BrowseOptions{Limit: 1, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		rows := drain(t, rs)
		if len(rows) != 1 {
			t.Fatalf("page %d holds %d rows", off, len(rows))
		}
		if id := model.IdentityOf(rs); id.Kind != model.IdentityPrimaryKey {
			t.Errorf("the rows are known by %+v, want the key", id)
		}
		seen = append(seen, rows[0][0].(int64))
	}
	if !slices.Equal(seen, []int64{1, 2, 3, 4}) {
		t.Errorf("the pages held %v", seen)
	}
}

// A table with no key of its own is addressed by ROWID, which is selected
// first so a grid can write by it (ADR-0144).
func TestLiveATableWithNoKeyIsAddressedByItsRowAddress(t *testing.T) {
	src := open(t, source.Guard{})
	nokey := model.NewRef(model.KindTable, "IKIGAI", "NOKEY")
	rs, err := src.Browse(context.Background(), nokey, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	id := model.IdentityOf(rs)
	if id.Kind != model.IdentityRowID || !slices.Equal(id.Columns, []string{"ROWID"}) {
		t.Fatalf("the rows are known by %+v", id)
	}
	cols := rs.Columns()
	if len(cols) != 3 || cols[0].Name != "ROWID" {
		t.Fatalf("the columns are %q, want the row's address first", labels(nil))
	}
	if len(rows) != 4 {
		t.Fatalf("it read %d rows", len(rows))
	}
	// Every row has an address, and no two share one.
	seen := map[string]bool{}
	for _, r := range rows {
		addr, ok := r[0].(string)
		if !ok || addr == "" {
			t.Fatalf("a row's address is %#v", r[0])
		}
		if seen[addr] {
			t.Errorf("two rows share the address %q", addr)
		}
		seen[addr] = true
	}
}

// A count for the tree (FR-2.5). It is the optimiser's, so it is as old as
// the last time anybody gathered statistics, and is marked as an estimate.
func TestLiveABadgeCountsATablesRows(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	b, ok, err := src.Badge(ctx, people)
	if err != nil || !ok {
		t.Fatalf("badge: %v %v", ok, err)
	}
	if b.Text != "101" {
		t.Errorf("the badge says %q", b.Text)
	}
	if b.Exact {
		t.Error("the optimiser's count was said to be exact")
	}
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindView, "IKIGAI", "ADULTS")); ok || err != nil {
		t.Errorf("a view was counted: %v %v", ok, err)
	}
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindTable, "IKIGAI", "NOT_THERE")); ok || err != nil {
		t.Errorf("a table that is not there was counted: %v %v", ok, err)
	}
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindTable, "PEOPLE")); ok || err != nil {
		t.Errorf("a reference naming no schema was counted: %v %v", ok, err)
	}
}

// A large object is read as text, and is not a value the picklist can
// offer: Oracle will not group or order by one.
func TestLiveALargeObjectIsReadButNotGrouped(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	docs := model.NewRef(model.KindTable, "IKIGAI", "DOCS")
	rs, err := src.Browse(ctx, docs, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	if len(rows) != 2 {
		t.Fatalf("it read %d rows", len(rows))
	}
	if body, ok := rows[0][1].(string); !ok || !strings.Contains(body, "document") {
		t.Errorf("the document came back as %#v", rows[0][1])
	}
	if _, err := src.Distinct(ctx, docs, "BODY", source.BrowseOptions{}, 10); err == nil {
		t.Error("a large object was offered as a picklist, and Oracle will not group one")
	}
}

func pinned(t *testing.T, src *oracleSource) source.Session {
	t.Helper()
	ss, err := src.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	return ss
}

func one(t *testing.T, ss source.Session, sql string) []model.Row {
	t.Helper()
	res, err := ss.Query(context.Background(), source.Statement{SQL: sql, Confirmed: true})
	if err != nil {
		t.Fatalf("%q: %v", sql, err)
	}
	if res.Rows == nil {
		return nil
	}
	return drain(t, res.Rows)
}

// A query tab is one connection, so what a statement leaves behind is
// there for the next one.
func TestLiveASessionKeepsWhatAStatementLeft(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	one(t, ss, `ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY'`)
	if rows := one(t, ss, `SELECT value FROM nls_session_parameters WHERE parameter = 'NLS_DATE_FORMAT'`); len(rows) != 1 || rows[0][0] != "YYYY" {
		t.Errorf("the session setting is %v", rows)
	}
	// Another session has its own, and is told nothing of this one's.
	other := pinned(t, src)
	if rows := one(t, other, `SELECT value FROM nls_session_parameters WHERE parameter = 'NLS_DATE_FORMAT'`); len(rows) != 1 || rows[0][0] == "YYYY" {
		t.Errorf("another session was given this one's setting: %v", rows)
	}
	if ss.Handle() == "" || ss.Handle() == other.Handle() {
		t.Errorf("the sessions are called %q and %q", ss.Handle(), other.Handle())
	}
}

// Every connection is put on UTC, so that a time means the same on the way
// out and on the way back (ADR-0144).
func TestLiveEveryConnectionIsOnUTC(t *testing.T) {
	src := open(t, source.Guard{})
	for _, ss := range []source.Session{pinned(t, src), pinned(t, src)} {
		rows := one(t, ss, `SELECT sessiontimezone FROM dual`)
		if len(rows) != 1 || rows[0][0] != "+00:00" {
			t.Errorf("a session is on %v", rows)
		}
	}
}

// A statement commits itself, as it does on every other engine here: an
// explicit transaction is the Transactor interface's business, and this
// driver does not implement it yet.
func TestLiveAStatementCommitsItself(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	one(t, ss, `INSERT INTO writes (id, name) VALUES (1, 'one')`)
	other := pinned(t, src)
	if rows := one(t, other, `SELECT COUNT(*) FROM writes`); len(rows) != 1 || countOf(rows[0][0]) != 1 {
		t.Errorf("another session cannot see what was written: %v", rows)
	}
}

// A statement that changes rows says how many (FR-5.2).
func TestLiveAWriteSaysHowManyRowsItChanged(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	res, err := ss.Query(context.Background(), source.Statement{
		SQL: `UPDATE people SET name = name WHERE id <= 5`, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Affected != 5 {
		t.Errorf("it says %d rows, want 5", res.Affected)
	}
	if res.Duration <= 0 {
		t.Errorf("it took %v", res.Duration)
	}
	// A read reports no count, because a count of rows read is the rows.
	res, err = ss.Query(context.Background(), source.Statement{SQL: `SELECT 1 FROM dual`})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Rows.Close()
	if res.Affected != -1 {
		t.Errorf("a read says %d rows changed", res.Affected)
	}
	// Nor does a statement that returns nothing and changes nothing.
	res, err = ss.Query(context.Background(), source.Statement{SQL: `ALTER SESSION SET NLS_DATE_FORMAT = 'YYYY'`})
	if err != nil {
		t.Fatal(err)
	}
	if res.Affected != -1 {
		t.Errorf("a session setting says %d rows changed", res.Affected)
	}
}

// Named parameters are bound, never written in (FR-5.7, NFR-S6).
func TestLiveNamedParametersAreBound(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	res, err := ss.Query(context.Background(), source.Statement{
		SQL:   `SELECT name FROM people WHERE id = :who OR id = :who + 1 ORDER BY id`,
		Named: map[string]any{"who": int64(5)}})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, res.Rows)
	if len(rows) != 2 || rows[0][0] != "person 5" || rows[1][0] != "person 6" {
		t.Errorf("the rows are %v", rows)
	}
}

// A script is run statement by statement, and a PL/SQL block is one
// statement however many semicolons it holds (FR-5.4).
func TestLiveAScriptRunsItsStatements(t *testing.T) {
	src := open(t, source.Guard{})
	script := "BEGIN INSERT INTO writes (id, name) VALUES (1, 'from a block'); " +
		"INSERT INTO writes (id, name) VALUES (2, 'and another'); END;\n/\n" +
		"SELECT COUNT(*) AS n FROM writes"
	ch, err := src.QueryMulti(context.Background(), script, source.ScriptOptions{Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	var said []string
	for r := range ch {
		if r.Err != nil {
			t.Fatalf("statement %d (%.40q): %v", r.Index, r.Statement, r.Err)
		}
		if r.Result.Rows != nil {
			for _, row := range drain(t, r.Result.Rows) {
				said = append(said, fmt.Sprint(countOf(row[0])))
			}
		}
	}
	if !slices.Equal(said, []string{"2"}) {
		t.Errorf("the script said %q, want the two rows the block wrote", said)
	}
}

// A server failure says what the server said, and which code it was
// (FR-5.10).
func TestLiveAFailureSaysWhatTheServerSaid(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	_, err := ss.Query(context.Background(), source.Statement{SQL: "SELECT 1 FROM nowhere_at_all"})
	var se *source.StatementError
	if !errors.As(err, &se) {
		t.Fatalf("the failure is %v", err)
	}
	if se.Message.Code != "ORA-00942" {
		t.Errorf("the code is %q, want the server's own", se.Message.Code)
	}
	if !strings.Contains(se.Message.Text, "does not exist") {
		t.Errorf("the message is %q", se.Message.Text)
	}
	if strings.Contains(se.Message.Text, "\n") {
		t.Errorf("the message carries more than it says: %q", se.Message.Text)
	}
}

// A read-only connection refuses a write here, before it reaches the server
// (NFR-S4).
func TestLiveReadOnlyRefusesAWriteHere(t *testing.T) {
	src := open(t, source.Guard{ReadOnly: true})
	ss := pinned(t, src)
	for _, sql := range []string{
		`INSERT INTO writes (id, name) VALUES (1, 'a')`,
		`UPDATE people SET name = 'x' WHERE id = 1`,
		`DROP TABLE nokey`,
		`BEGIN NULL; END;`,
	} {
		if _, err := ss.Query(context.Background(), source.Statement{SQL: sql, Confirmed: true}); !errors.Is(err, source.ErrReadOnly) {
			t.Errorf("%q was refused with %v, want it refused as a write", sql, err)
		}
	}
	if rows := one(t, ss, `SELECT COUNT(*) FROM people`); len(rows) != 1 || countOf(rows[0][0]) != 101 {
		t.Errorf("a read on a read-only connection gave %v", rows)
	}
}

// A script is refused whole, before any of it runs.
func TestLiveAScriptIsRefusedBeforeAnyOfItRuns(t *testing.T) {
	src := open(t, source.Guard{ReadOnly: true})
	_, err := src.QueryMulti(context.Background(),
		"SELECT 1 FROM dual; INSERT INTO writes (id, name) VALUES (1, 'a')", source.ScriptOptions{Confirmed: true})
	if !errors.Is(err, source.ErrReadOnly) {
		t.Fatalf("the script was refused with %v", err)
	}
	rw := opened(t, source.Guard{})
	ss := pinned(t, rw)
	if rows := one(t, ss, `SELECT COUNT(*) FROM writes`); len(rows) != 1 || countOf(rows[0][0]) != 0 {
		t.Errorf("the refused script left %v behind", rows)
	}
}

// The picklist's values are the column's, most frequent first, and each one
// filters with exactly what it was given (FR-3.4).
func TestLiveDistinctValuesFilterWithThemselves(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	nokey := model.NewRef(model.KindTable, "IKIGAI", "NOKEY")
	vals, err := src.Distinct(ctx, nokey, "A", source.BrowseOptions{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 3 || countOf(vals[0].Value) != 1 || vals[0].Count != 2 {
		t.Fatalf("the values are %+v", vals)
	}
	rs, err := src.Browse(ctx, nokey, source.BrowseOptions{Limit: 10,
		Filters: []source.Filter{{Column: "A", Op: source.OpIn, Values: []any{vals[0].Value}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(drain(t, rs)); got != 2 {
		t.Errorf("filtering by the first value found %d rows, want the 2 it was counted in", got)
	}
}

// A search finds what somebody typed whatever its case, and a regular
// expression is a real one.
func TestLiveASearchAndAPattern(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	count := func(f source.Filter) int {
		t.Helper()
		rs, err := src.Browse(ctx, people, source.BrowseOptions{Limit: 200, Filters: []source.Filter{f}})
		if err != nil {
			t.Fatalf("%s: %v", f.Op, err)
		}
		return len(drain(t, rs))
	}
	if got := count(source.Filter{Column: "NAME", Op: source.OpContains, Values: []any{"PERSON 1"}}); got != 12 {
		t.Errorf("a search for PERSON 1 found %d rows, want the 12 whose name holds it", got)
	}
	if got := count(source.Filter{Column: "NAME", Op: source.OpRegex, Values: []any{"^person 1[0-9]$"}}); got != 10 {
		t.Errorf("a pattern for person 10 to 19 found %d rows", got)
	}
	// The text of a search is not a pattern: nothing here holds a per cent.
	if got := count(source.Filter{Column: "NAME", Op: source.OpContains, Values: []any{"person%1"}}); got != 0 {
		t.Errorf("a search for a per cent found %d rows", got)
	}
}

// A time read from the grid finds its own row again: a value that carries a
// zone and a column that does not are compared as themselves (ADR-0144).
func TestLiveATimeFindsItsOwnRow(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	rs, err := src.Browse(ctx, people, source.BrowseOptions{Limit: 1,
		Filters: []source.Filter{{Column: "BORN", Op: source.OpIsNotNull}}})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	if len(rows) != 1 {
		t.Fatalf("it read %d rows", len(rows))
	}
	born, ok := rows[0][3].(time.Time)
	if !ok {
		t.Fatalf("a date came back as %#v", rows[0][3])
	}
	rs, err = src.Browse(ctx, people, source.BrowseOptions{Limit: 100,
		Filters: []source.Filter{{Column: "BORN", Op: source.OpEqual, Values: []any{born}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(drain(t, rs)); got == 0 {
		t.Errorf("filtering on the date it was read as found no rows")
	}
}

// Rows written back from the grid (FR-4.4, FR-4.5).

var writesRef = model.NewRef(model.KindTable, "IKIGAI", "WRITES")

func applyTo(t *testing.T, s *oracleSource, id model.RowIdentity, changes ...source.RowChange) *source.WriteOutcome {
	t.Helper()
	ctx := context.Background()
	plan, err := s.Plan(ctx, source.Changeset{Target: writesRef, Identity: id, Changes: changes, Confirmed: true})
	if err != nil {
		t.Fatalf("planning: %v", err)
	}
	out, err := s.Apply(ctx, plan)
	if err != nil {
		t.Fatalf("applying: %v", err)
	}
	return out
}

func rowsNow(t *testing.T, s *oracleSource) string {
	t.Helper()
	rs, err := s.Browse(context.Background(), writesRef, source.BrowseOptions{Limit: 50,
		Sorts: []source.Sort{{Column: "ID"}}})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, row := range drain(t, rs) {
		out = append(out, fmt.Sprint(row[1:]))
	}
	return strings.Join(out, " ")
}

func byID() model.RowIdentity {
	return model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"ID"}, Target: writesRef}
}

func added(id int64, name string, n any) source.RowChange {
	v := map[string]any{"ID": id, "NAME": name}
	if n != nil {
		v["N"] = n
	}
	return source.RowChange{Kind: source.ChangeInsert, Values: v}
}

// A row is added, changed and deleted by its key.
func TestLiveRowsAreWrittenByTheirKey(t *testing.T) {
	src := open(t, source.Guard{})
	out := applyTo(t, src, byID(), added(1, "one", int64(1)), added(2, "two", int64(2)), added(3, "three", nil))
	if out.Err != nil || out.Applied != 3 || out.Affected != 3 || out.FailedAt != -1 {
		t.Fatalf("adding rows: %+v", out)
	}
	if got := rowsNow(t, src); got != "[1 one 1] [2 two 2] [3 three <nil>]" {
		t.Fatalf("after adding: %s", got)
	}
	out = applyTo(t, src, byID(),
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(1)}, Values: map[string]any{"NAME": "uno"}},
		source.RowChange{Kind: source.ChangeDelete, Key: []any{int64(2)}},
		added(4, "four", nil))
	if out.Err != nil || out.Applied != 3 {
		t.Fatalf("an update, a delete and an insert: %+v", out)
	}
	if got := rowsNow(t, src); got != "[1 uno 1] [3 three <nil>] [4 four <nil>]" {
		t.Errorf("after them: %s", got)
	}
}

// A plan is written whole or not at all: Oracle has a transaction to write
// one in (FR-4.5).
func TestLiveAFailedPlanIsUndone(t *testing.T) {
	src := open(t, source.Guard{})
	applyTo(t, src, byID(), added(1, "one", nil))
	out := applyTo(t, src, byID(),
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(1)}, Values: map[string]any{"NAME": "changed"}},
		source.RowChange{Kind: source.ChangeUpdate, Key: []any{int64(99)}, Values: map[string]any{"NAME": "gone"}})
	if out.Err == nil || out.FailedAt != 1 {
		t.Fatalf("a plan whose second change fails: %+v", out)
	}
	if !out.RolledBack {
		t.Error("the plan was not undone, and Oracle has a transaction to undo it in")
	}
	if got := rowsNow(t, src); got != "[1 one <nil>]" {
		t.Errorf("the change that ran was kept: %s", got)
	}
}

// A row with no key of its own is written by its address (ADR-0144).
func TestLiveARowIsWrittenByItsAddress(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	nokey := model.NewRef(model.KindTable, "IKIGAI", "NOKEY")
	rs, err := src.Browse(ctx, nokey, source.BrowseOptions{Limit: 10,
		Filters: []source.Filter{{Column: "B", Op: source.OpEqual, Values: []any{"z"}}}})
	if err != nil {
		t.Fatal(err)
	}
	id := model.IdentityOf(rs)
	rows := drain(t, rs)
	if len(rows) != 1 {
		t.Fatalf("it read %d rows", len(rows))
	}
	addr := rows[0][0]

	plan, err := src.Plan(ctx, source.Changeset{Target: nokey, Identity: id, Confirmed: true,
		Changes: []source.RowChange{{Kind: source.ChangeUpdate, Key: []any{addr},
			Values: map[string]any{"B": "Z"}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := src.Apply(ctx, plan)
	if err != nil || out.Err != nil || out.Applied != 1 {
		t.Fatalf("writing by a row's address: %v %+v", err, out)
	}
	// One row changed, and the others are as they were.
	back, err := src.Browse(ctx, nokey, source.BrowseOptions{Limit: 10,
		Sorts: []source.Sort{{Column: "A"}, {Column: "B"}}})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range drain(t, back) {
		got = append(got, fmt.Sprint(r[1:]))
	}
	if strings.Join(got, " ") != "[1 x] [1 x] [2 y] [3 Z]" {
		t.Errorf("the rows are %q", got)
	}
}

// A read-only connection writes nothing, and a production one asks first
// (FR-1.8, FR-4.9).
func TestLiveWritingIsGuarded(t *testing.T) {
	src := open(t, source.Guard{})
	applyTo(t, src, byID(), added(1, "one", nil))
	ctx := context.Background()

	ro := opened(t, source.Guard{ReadOnly: true})
	plan, err := ro.Plan(ctx, source.Changeset{Target: writesRef, Identity: byID(),
		Changes: []source.RowChange{added(2, "two", nil)}, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ro.Apply(ctx, plan); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("a read-only connection wrote: %v", err)
	}

	prod := opened(t, source.Guard{Environment: source.EnvProduction})
	plan, err = prod.Plan(ctx, source.Changeset{Target: writesRef, Identity: byID(),
		Changes: []source.RowChange{{Kind: source.ChangeDelete, Key: []any{int64(1)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Guarded {
		t.Error("a production plan is guarded")
	}
	if _, err := prod.Apply(ctx, plan); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("production without consent: %v", err)
	}
	if got := rowsNow(t, src); got != "[1 one <nil>]" {
		t.Errorf("the row nobody was allowed to write is as it was: %s", got)
	}
}

// A change meant for a row that is not there is refused (ADR-0034).
func TestLiveAChangeIsForOneRowOrForNone(t *testing.T) {
	src := open(t, source.Guard{})
	applyTo(t, src, byID(), added(1, "one", nil))
	out := applyTo(t, src, byID(), source.RowChange{Kind: source.ChangeUpdate,
		Key: []any{int64(99)}, Values: map[string]any{"NAME": "gone"}})
	if out.Err == nil {
		t.Errorf("a change to a row that is not there: %+v", out)
	}
	if got := rowsNow(t, src); got != "[1 one <nil>]" {
		t.Errorf("a row was made by a change: %s", got)
	}
}
