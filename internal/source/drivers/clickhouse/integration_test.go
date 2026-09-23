//go:build conformance

package clickhouse

// Integration tests against a real ClickHouse (REQ-DRV-1, T3.31). They
// expect the ikigai-clickhouse container on port 59000 and skip if it is
// not running; IKIGAI_REQUIRE_CLICKHOUSE=1 makes that a failure, as in CI.

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
	"sync"
	"testing"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"

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

func port() int {
	if v := os.Getenv("IKIGAI_CLICKHOUSE_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 59000
}

func password() string { return env("IKIGAI_CLICKHOUSE_PASSWORD", "ikigai") }

func config(db string, guard source.Guard) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: port(), User: "default", Database: db,
		Secret: func(string) (string, error) { return password(), nil },
		TLS:    source.TLSConfig{Mode: "disable"}, Guard: guard}
}

// admin is a plain connection, for building the fixture.
func admin(t *testing.T, database string) *sql.DB {
	t.Helper()
	db := ch.OpenDB(&ch.Options{Protocol: ch.Native,
		Addr: []string{fmt.Sprintf("127.0.0.1:%d", port())},
		Auth: ch.Auth{Database: database, Username: "default", Password: password()}})
	if err := db.Ping(); err != nil {
		db.Close()
		if os.Getenv("IKIGAI_REQUIRE_CLICKHOUSE") != "" {
			t.Fatalf("ClickHouse required but unavailable: %v", err)
		}
		t.Skipf("no ClickHouse on port %d (docker start ikigai-clickhouse): %v", port(), err)
	}
	return db
}

var madeDatabase sync.Once

var fixture = []string{
	// The dictionary first: it holds the table it reads from.
	`DROP DICTIONARY IF EXISTS ikigai_it.by_id`,
	`DROP TABLE IF EXISTS ikigai_it.people`,
	`DROP TABLE IF EXISTS ikigai_it.orders`,
	`DROP TABLE IF EXISTS ikigai_it.nokey`,
	`DROP TABLE IF EXISTS ikigai_it.keyed`,
	`DROP TABLE IF EXISTS ikigai_it.writes`,
	`DROP VIEW IF EXISTS ikigai_it.adults`,
	`CREATE TABLE ikigai_it.people (
	   id UInt32,
	   name String COMMENT 'What they are called',
	   score Nullable(Decimal(30, 10)),
	   born Nullable(Date),
	   seen Nullable(DateTime64(3, 'UTC')),
	   meta Nullable(String),
	   tag UUID,
	   ok Bool,
	   tags Array(LowCardinality(String)),
	   kind Enum8('one' = 1, 'two' = 2),
	   where_ IPv4,
	   big Int128
	 ) ENGINE = MergeTree ORDER BY id COMMENT 'Everyone we know'`,
	`INSERT INTO ikigai_it.people
	 SELECT number + 1 AS id, concat('person ', toString(number + 1)) AS name,
	        toDecimal128((number + 1) * 1.5, 10) AS score,
	        if(number % 3 = 0, toDate('2000-01-01') + (number % 2), NULL) AS born,
	        NULL AS seen,
	        concat('{"i": ', toString(number + 1), '}') AS meta,
	        generateUUIDv4() AS tag, number % 2 = 0 AS ok,
	        ['a', 'b'] AS tags, if(number % 2 = 0, 'one', 'two') AS kind,
	        toIPv4('10.0.0.1') AS where_, toInt128(number + 1) AS big
	 FROM numbers(100)`,
	`INSERT INTO ikigai_it.people VALUES (1000, 'exact', 12345678901234567890.1234567890, NULL, NULL, NULL,
	   '6f9619ff-8b86-d011-b42d-00c04fc964ff', true, [], 'one', '10.0.0.2', 170141183460469231731687303715884105727)`,
	`CREATE TABLE ikigai_it.orders (id UInt32, person_id UInt32, total Decimal(10, 2))
	 ENGINE = MergeTree ORDER BY (person_id, id)`,
	// No sorting key at all: a Memory table is ordered by everything it can
	// be, which is the only total order there is for one.
	// notes is a column no ORDER BY may name, and a table with no sorting
	// key is ordered by every column it can be.
	// No sorting key at all, so a browse orders by every column it has.
	`CREATE TABLE ikigai_it.nokey (a Int32, b String) ENGINE = Memory`,
	`INSERT INTO ikigai_it.nokey VALUES (2, 'y'), (1, 'x'), (1, 'x'), (3, 'z')`,
	// name takes a value when a row does not give one; twice is worked out.
	`CREATE TABLE ikigai_it.writes (id UInt32, name String DEFAULT 'none', n Nullable(Int32),
	   twice UInt32 MATERIALIZED id * 2) ENGINE = MergeTree ORDER BY id`,
	// Ordered by its second column, so that a browse following the table's
	// own order reads differently from one following the columns' order.
	`CREATE TABLE ikigai_it.keyed (a Int32, b Int32) ENGINE = MergeTree ORDER BY b`,
	`INSERT INTO ikigai_it.keyed VALUES (1, 3), (2, 1), (3, 2)`,
	`CREATE VIEW ikigai_it.adults AS SELECT id, name FROM ikigai_it.people WHERE id > 10`,
	// A dictionary is a table to the catalogue and a routine to the tree:
	// it is a lookup, not rows somebody browses.
	`CREATE DICTIONARY ikigai_it.by_id (id UInt64, name String) PRIMARY KEY id
	 SOURCE(CLICKHOUSE(DB 'ikigai_it' TABLE 'people' USER 'default' PASSWORD 'ikigai'))
	 LAYOUT(FLAT()) LIFETIME(0)`,
}

// build makes the fixture. The database is made once per run and its tables
// per test, so that a test that writes cannot reach the next one.
func build(t *testing.T) {
	t.Helper()
	db := admin(t, "default")
	defer db.Close()
	madeDatabase.Do(func() {
		for _, stmt := range []string{`DROP DATABASE IF EXISTS ikigai_it`, `CREATE DATABASE ikigai_it`} {
			if _, err := db.Exec(stmt); err != nil {
				t.Fatalf("%s: %v", stmt, err)
			}
		}
	})
	for _, stmt := range fixture {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("fixture %.60q: %v", stmt, err)
		}
	}
}

func open(t *testing.T, guard source.Guard) *clickhouseSource {
	t.Helper()
	build(t)
	src, err := Driver{}.Open(context.Background(), config("ikigai_it", guard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	return src.(*clickhouseSource)
}

var people = model.NewRef(model.KindTable, "ikigai_it", "people")

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
		Name: "clickhouse",
		Open: func(ctx context.Context, t *testing.T) source.Source {
			src, err := Driver{}.Open(ctx, config("ikigai_it", source.Guard{}))
			if err != nil {
				t.Fatal(err)
			}
			return src
		},
		Browsable: people,
		OpenGuarded: func(ctx context.Context, t *testing.T, g source.Guard) source.Source {
			src, err := Driver{}.Open(ctx, config("ikigai_it", g))
			if err != nil {
				t.Fatal(err)
			}
			return src
		},
	})
}

// Every value comes back as one of the model's own, whatever shape the
// driver handed it over in (model.Row).
func TestLiveValuesArriveAsTheModelsOwn(t *testing.T) {
	src := open(t, source.Guard{})
	rs, err := src.Browse(context.Background(), people, source.BrowseOptions{Limit: 1,
		Filters: []source.Filter{{Column: "id", Op: source.OpEqual, Values: []any{int64(1000)}}}})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want the one row asked for", len(rows))
	}
	at := map[string]any{}
	for i, c := range rs.Columns() {
		at[c.Name] = rows[0][i]
	}
	cases := map[string]any{
		"id":     int64(1000),
		"name":   "exact",
		"ok":     true,
		"kind":   "one",
		"where_": "10.0.0.2",
		"tag":    "6f9619ff-8b86-d011-b42d-00c04fc964ff",
		// An exact number keeps the places its column has: a decimal that
		// dropped its last zero would be a different number to read.
		"score": model.Decimal("12345678901234567890.1234567890"),
		// Past what an int64 holds, so it travels as exact text.
		"big": model.Decimal("170141183460469231731687303715884105727"),
	}
	for name, want := range cases {
		if got := at[name]; got != want {
			t.Errorf("%s is %#v, want %#v", name, got, want)
		}
	}
	// An array is a list of the model's own values, not a driver's slice.
	if got, ok := at["tags"].([]any); !ok || len(got) != 0 {
		t.Errorf("tags is %#v, want an empty list", at["tags"])
	}
	if at["born"] != nil || at["meta"] != nil {
		t.Errorf("a column holding nothing came back as %#v and %#v", at["born"], at["meta"])
	}
}

// A date is a time and not the text of one, and an array holds what its
// element type says it holds.
func TestLiveATimeIsATimeAndAnArrayIsAList(t *testing.T) {
	src := open(t, source.Guard{})
	rs, err := src.Browse(context.Background(), people, source.BrowseOptions{Limit: 1,
		Filters: []source.Filter{{Column: "born", Op: source.OpIsNotNull}}})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	at := map[string]any{}
	for i, c := range rs.Columns() {
		at[c.Name] = rows[0][i]
	}
	born, ok := at["born"].(time.Time)
	if !ok || born.Year() != 2000 {
		t.Errorf("born is %#v, want a time", at["born"])
	}
	tags, ok := at["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("tags is %#v, want a list of two names", at["tags"])
	}
}

var _ = strings.TrimSpace
var _ = slices.Equal[[]string]

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

// The explorer walks a server, a database and a table (FR-2.1). There is no
// schema between the database and the table.
func TestLiveTheExplorerWalksTheServer(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()

	roots, err := src.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ours := find(roots, "ikigai_it")
	if ours == nil {
		t.Fatalf("the databases are %q", labels(roots))
	}
	if ours.Attrs["current"] != "true" {
		t.Errorf("the database this connection is on is not marked: %+v", ours.Attrs)
	}
	if sys := find(roots, "system"); sys == nil || sys.Attrs["system"] != "true" {
		t.Errorf("system is not marked as the server's own: %+v", sys)
	}

	folders, err := src.Children(ctx, ours.Ref)
	if err != nil {
		t.Fatal(err)
	}
	tables := find(folders, "Tables")
	if tables == nil {
		t.Fatalf("the classes are %q", labels(folders))
	}
	if tables.Badge == nil || tables.Badge.Text != "5" {
		t.Errorf("the tables folder counts %+v, want the five in the fixture", tables.Badge)
	}
	if v := find(folders, "Views"); v == nil || v.Badge == nil || v.Badge.Text != "1" {
		t.Errorf("the views folder is %+v", v)
	}

	objects, err := src.Children(ctx, tables.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := labels(objects); !slices.IsSorted(got) {
		t.Errorf("the tables are listed %q, want them in order", got)
	}
	p := find(objects, "people")
	if p == nil {
		t.Fatalf("the tables are %q", labels(objects))
	}
	if !p.Browsable || !p.HasChildren {
		t.Errorf("people is %+v, want it browsable and openable", p)
	}
	if p.Badge == nil || p.Badge.Text != "101" {
		t.Errorf("people's badge is %+v, want the hundred and one rows it holds", p.Badge)
	}
	if p.Attrs["engine"] != "MergeTree" {
		t.Errorf("people is stored by %q", p.Attrs["engine"])
	}

	cols, err := src.Children(ctx, p.Ref)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "name", "score", "born", "seen", "meta", "tag", "ok", "tags", "kind", "where_", "big"}
	if got := labels(cols); !slices.Equal(got, want) {
		t.Fatalf("the columns are %q, want %q", got, want)
	}
	// ClickHouse's primary key is the prefix of the sorting key it keeps an
	// index over. It addresses no row, so it is shown as what orders the
	// table rather than as a key.
	if cols[0].Attrs["key"] != "sorting" || cols[0].Attrs["type"] != "UInt32" {
		t.Errorf("id is %+v", cols[0].Attrs)
	}
	if cols[1].Attrs["type"] != "String" || cols[1].Attrs["nullable"] != "false" {
		t.Errorf("name is %+v", cols[1].Attrs)
	}
	if cols[2].Attrs["type"] != "Nullable(Decimal(30, 10))" || cols[2].Attrs["nullable"] != "true" {
		t.Errorf("score is %+v", cols[2].Attrs)
	}
	if cols[1].Attrs["key"] != "" {
		t.Errorf("name orders nothing and says %q", cols[1].Attrs["key"])
	}
}

// A view is listed as a view, and is browsable; a table is listed as a
// table. What tells them apart is the engine they are stored by.
func TestLiveAViewIsNotATable(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	views, err := src.Children(ctx, model.NewRef(model.KindFolder, "ikigai_it", "view"))
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Label != "adults" || !views[0].Browsable {
		t.Fatalf("the views are %+v", views)
	}
	if views[0].Attrs["engine"] != "View" {
		t.Errorf("adults is stored by %q", views[0].Attrs["engine"])
	}
	// A view holds no rows of its own, so it is given no count.
	if views[0].Badge != nil {
		t.Errorf("a view was counted: %+v", views[0].Badge)
	}
	tables, err := src.Children(ctx, model.NewRef(model.KindFolder, "ikigai_it", "table"))
	if err != nil {
		t.Fatal(err)
	}
	if find(tables, "adults") != nil {
		t.Errorf("a view is listed among the tables: %q", labels(tables))
	}
}

// A dictionary is a table to the catalogue and a routine to the tree: it
// is a lookup rather than rows somebody browses.
func TestLiveADictionaryIsNotATable(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	dicts, err := src.Children(ctx, model.NewRef(model.KindFolder, "ikigai_it", "routine"))
	if err != nil {
		t.Fatal(err)
	}
	if len(dicts) != 1 || dicts[0].Label != "by_id" {
		t.Fatalf("the dictionaries are %+v", dicts)
	}
	if dicts[0].Browsable || dicts[0].HasChildren {
		t.Errorf("a dictionary was offered as rows to browse: %+v", dicts[0])
	}
	if dicts[0].Attrs["engine"] != "Dictionary" {
		t.Errorf("it is stored by %q", dicts[0].Attrs["engine"])
	}
	// The explorer shows nothing it was not told this source holds
	// (REQ-DB-1), so the class has to be declared as well as listed.
	if !src.Capabilities().Supports(model.KindRoutine) {
		t.Error("dictionaries are listed and the explorer is not told they exist")
	}
	tables, err := src.Children(ctx, model.NewRef(model.KindFolder, "ikigai_it", "table"))
	if err != nil {
		t.Fatal(err)
	}
	if find(tables, "by_id") != nil {
		t.Errorf("a dictionary is listed among the tables: %q", labels(tables))
	}
	// It holds rows, and is still not given the badge a table has: what is
	// counted for the tree is what somebody can open.
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindRoutine, "ikigai_it", "by_id")); ok || err != nil {
		t.Errorf("a dictionary was counted: %v %v", ok, err)
	}
}

// A column takes a value where a row gives none, and one worked out from
// the others is not a value a row may give.
func TestLiveAColumnTheServerFillsInSaysSo(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), model.NewRef(model.KindTable, "ikigai_it", "writes"))
	if err != nil {
		t.Fatal(err)
	}
	at := map[string]model.Column{}
	for _, c := range got.(*model.Table).Columns {
		at[c.Name] = c
	}
	if c := at["name"]; !c.HasDefault || c.Default != "'none'" || c.Generated != "" {
		t.Errorf("name is %+v, want a column with a default", c)
	}
	if c := at["twice"]; c.Generated != "id * 2" || c.HasDefault {
		t.Errorf("twice is %+v, want a column worked out", c)
	}
	if c := at["n"]; c.HasDefault || c.Generated != "" {
		t.Errorf("n is %+v, want a column somebody fills in", c)
	}
}

// A reference naming no table names nothing that can be read.
func TestLiveAnIncompleteReferenceIsRefused(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	ref := model.NewRef(model.KindTable, "people")
	if _, err := src.Browse(ctx, ref, source.BrowseOptions{Limit: 1}); err == nil {
		t.Error("a reference naming no database was browsable")
	}
	if _, err := src.Describe(ctx, ref); err == nil {
		t.Error("a reference naming no database was described")
	}
}

// A table's structure is what a column store is arranged by: the engine,
// the sorting key, the partition key.
func TestLiveATableIsDescribed(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), model.NewRef(model.KindTable, "ikigai_it", "orders"))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := got.(*model.Table)
	if !ok {
		t.Fatalf("a table is described as %T", got)
	}
	if tbl.Attrs["engine"] != "MergeTree" {
		t.Errorf("it is stored by %q", tbl.Attrs["engine"])
	}
	if tbl.Attrs["sorting_key"] != "person_id, id" {
		t.Errorf("it is ordered by %q", tbl.Attrs["sorting_key"])
	}
	if tbl.RowsEstimate != 0 {
		t.Errorf("it holds %d rows", tbl.RowsEstimate)
	}
	if len(tbl.Columns) != 3 {
		t.Errorf("its columns are %+v", tbl.Columns)
	}
	// There are no keys here to describe: nothing addresses a row, and
	// nothing refers to one.
	if tbl.PrimaryKey != nil || len(tbl.ForeignKeys) != 0 {
		t.Errorf("a key was described: %+v %+v", tbl.PrimaryKey, tbl.ForeignKeys)
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
		if c.Name == "name" && c.Comment != "What they are called" {
			t.Errorf("the name column says %q", c.Comment)
		}
		if c.Name == "id" && c.Comment != "" {
			t.Errorf("a column nobody wrote about says %q", c.Comment)
		}
	}
}

// A view is described by what it selects.
func TestLiveAViewIsDescribed(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), model.NewRef(model.KindView, "ikigai_it", "adults"))
	if err != nil {
		t.Fatal(err)
	}
	v, ok := got.(*model.View)
	if !ok {
		t.Fatalf("a view is described as %T", got)
	}
	if !strings.Contains(v.Definition, "people") {
		t.Errorf("the view is defined as %q", v.Definition)
	}
	if len(v.Columns) != 2 {
		t.Errorf("the view has %d columns", len(v.Columns))
	}
	if v.Materialized {
		t.Error("an ordinary view was called a materialized one")
	}
}

// Paging orders by the table's sorting key, which is as stable as a column
// store makes it — and the rows are known by nothing, because no key here
// addresses one (FR-4.7).
func TestLivePagingOrdersByTheSortingKey(t *testing.T) {
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
		if id := model.IdentityOf(rs); id.Kind != model.IdentityNone {
			t.Errorf("a row was said to be addressable by %+v", id)
		}
		seen = append(seen, rows[0][0].(int64))
	}
	if !slices.Equal(seen, []int64{1, 2, 3, 4}) {
		t.Errorf("the pages held %v, want the rows in the key's order", seen)
	}
}

// A table with no sorting key at all is ordered by every column it can be,
// which is the only total order one has.
func TestLiveATableWithNoSortingKeyIsOrderedByEverything(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	nokey := model.NewRef(model.KindTable, "ikigai_it", "nokey")
	var seen []string
	for off := int64(0); off < 4; off++ {
		rs, err := src.Browse(ctx, nokey, source.BrowseOptions{Limit: 1, Offset: off})
		if err != nil {
			t.Fatal(err)
		}
		rows := drain(t, rs)
		if len(rows) != 1 {
			t.Fatalf("page %d holds %d rows", off, len(rows))
		}
		seen = append(seen, fmt.Sprint(rows[0]))
	}
	want := []string{"[1 x]", "[1 x]", "[2 y]", "[3 z]"}
	if !slices.Equal(seen, want) {
		t.Errorf("the pages held %q, want %q", seen, want)
	}
}

// Paging follows the table's own order, which is the order the rows are
// already in and the cheap one to read them in.
func TestLivePagingOrdersByItsOwnKey(t *testing.T) {
	src := open(t, source.Guard{})
	rs, err := src.Browse(context.Background(), model.NewRef(model.KindTable, "ikigai_it", "keyed"),
		source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, row := range drain(t, rs) {
		got = append(got, row[0].(int64))
	}
	// Ordered by b, which is 3, 1, 2 for a of 1, 2, 3.
	if !slices.Equal(got, []int64{2, 3, 1}) {
		t.Errorf("the rows came in the order %v, want the table's own", got)
	}
}

// A sort somebody asked for comes first, and the key only breaks its ties.
func TestLiveASortAskedForComesFirst(t *testing.T) {
	src := open(t, source.Guard{})
	rs, err := src.Browse(context.Background(), people, source.BrowseOptions{Limit: 3,
		Sorts: []source.Sort{{Column: "id", Descending: true}}})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	if len(rows) != 3 || rows[0][0] != int64(1000) {
		t.Errorf("the rows came in the order %v", rows)
	}
}

// A count for the tree, asked for one node at a time (FR-2.5).
func TestLiveABadgeCountsATablesRows(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	b, ok, err := src.Badge(ctx, people)
	if err != nil || !ok {
		t.Fatalf("badge: %v %v", ok, err)
	}
	if b.Text != "101" || !b.Exact {
		t.Errorf("the badge says %+v", b)
	}
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindView, "ikigai_it", "adults")); ok || err != nil {
		t.Errorf("a view was counted: %v %v", ok, err)
	}
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindTable, "ikigai_it", "not_there")); ok || err != nil {
		t.Errorf("a table that is not there was counted: %v %v", ok, err)
	}
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindTable, "people")); ok || err != nil {
		t.Errorf("a reference naming no database was counted: %v %v", ok, err)
	}
}

func pinned(t *testing.T, src *clickhouseSource) source.Session {
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

// A query tab is one connection, so what a statement leaves behind is there
// for the next one: a setting, a temporary table.
func TestLiveASessionKeepsWhatAStatementLeft(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	one(t, ss, `CREATE TEMPORARY TABLE scratch (a Int32)`)
	one(t, ss, `INSERT INTO scratch VALUES (7)`)
	rows := one(t, ss, `SELECT a FROM scratch`)
	if len(rows) != 1 || rows[0][0] != int64(7) {
		t.Errorf("the temporary table holds %v", rows)
	}
	if ss.Handle() == "" {
		t.Error("the session has no name to be stopped by")
	}
	other := pinned(t, src)
	if _, err := other.Query(context.Background(), source.Statement{SQL: `SELECT a FROM scratch`}); err == nil {
		t.Error("another session saw this one's temporary table")
	}
	if other.Handle() == ss.Handle() {
		t.Errorf("two sessions are both called %q", ss.Handle())
	}
}

// Named parameters are bound, never written in (FR-5.7, NFR-S6).
func TestLiveNamedParametersAreBound(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	res, err := ss.Query(context.Background(), source.Statement{
		SQL:   `SELECT name FROM ikigai_it.people WHERE id = :who OR id = :who + 1 ORDER BY id`,
		Named: map[string]any{"who": int64(5)}})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, res.Rows)
	if len(rows) != 2 || rows[0][0] != "person 5" || rows[1][0] != "person 6" {
		t.Errorf("the rows are %v", rows)
	}
}

// A script is run statement by statement, each result in its turn (FR-5.4).
func TestLiveAScriptRunsItsStatements(t *testing.T) {
	src := open(t, source.Guard{})
	ch, err := src.QueryMulti(context.Background(),
		"SELECT 'first' AS said; SELECT 'second' AS said", source.ScriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var said []string
	for r := range ch {
		if r.Err != nil {
			t.Fatalf("statement %d (%.40q): %v", r.Index, r.Statement, r.Err)
		}
		for _, row := range drain(t, r.Result.Rows) {
			said = append(said, fmt.Sprint(row[0]))
		}
	}
	if !slices.Equal(said, []string{"first", "second"}) {
		t.Errorf("the script said %q", said)
	}
}

// A result that is not the last is held, so the next statement can have the
// connection — and bounded, because holding a whole table would not fit
// (NFR-P11).
func TestLiveAnEarlierResultIsHeldWhileTheRestRuns(t *testing.T) {
	src := open(t, source.Guard{})
	ch, err := src.QueryMulti(context.Background(),
		"SELECT id FROM ikigai_it.people ORDER BY id; SELECT 'last' AS said", source.ScriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var results []source.ScriptResult
	for r := range ch {
		if r.Err != nil {
			t.Fatalf("statement %d: %v", r.Index, r.Err)
		}
		results = append(results, r)
	}
	if len(results) != 2 {
		t.Fatalf("the script gave %d results", len(results))
	}
	if got := len(drain(t, results[0].Result.Rows)); got != 101 {
		t.Errorf("the first result holds %d rows", got)
	}
	if got := drain(t, results[1].Result.Rows); len(got) != 1 || got[0][0] != "last" {
		t.Errorf("the last result is %v", got)
	}
}

// A server failure says what the server said, and which statement it was
// (FR-5.10).
func TestLiveAFailureSaysWhatTheServerSaid(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	_, err := ss.Query(context.Background(), source.Statement{SQL: "SELECT 1 FROM ikigai_it.nowhere_at_all"})
	var se *source.StatementError
	if !errors.As(err, &se) {
		t.Fatalf("the failure is %v", err)
	}
	if se.Message.Code != "60" {
		t.Errorf("the code is %q, want the server's own", se.Message.Code)
	}
	if !strings.Contains(se.Message.Text, "nowhere_at_all") {
		t.Errorf("the message is %q", se.Message.Text)
	}
	if strings.Contains(se.Message.Text, "Stack trace") || strings.Contains(se.Message.Text, "\n") {
		t.Errorf("the message carries more than it says: %q", se.Message.Text)
	}
}

// A statement is stopped at the server, and the tab goes on working
// (FR-5.5).
func TestLiveAStatementIsStoppedAtTheServer(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	res, err := ss.Query(ctx, source.Statement{SQL: `SELECT number FROM numbers(100000000000) WHERE sipHash64(number) = 1`})
	if err == nil {
		_, err = res.Rows.Next(ctx)
		res.Rows.Close()
	}
	if err == nil {
		t.Fatal("a statement over a hundred thousand million rows finished")
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("stopping it took %v", took)
	}
	// Whatever the driver made of it, what is reported is the stopping.
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("it failed with %v, want the stopping", err)
	}
	if rows := one(t, ss, `SELECT 1`); len(rows) != 1 {
		t.Errorf("the session answered %v after a statement was stopped", rows)
	}
}

// A statement can be stopped from another connection, by the name its
// session gave it (source.Killer).
func TestLiveAStatementIsStoppedByName(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	// Whatever happens below, the statement is stopped before the test
	// ends: a session cannot be closed while one is still running on it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		res, err := ss.Query(ctx, source.Statement{
			SQL: `SELECT number FROM numbers(100000000000) WHERE sipHash64(number) = 1`})
		if err == nil {
			_, err = res.Rows.Next(ctx)
			res.Rows.Close()
		}
		done <- err
	}()
	// Wait for the server to have it, then stop it by name.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var n uint64
		if err := src.db.QueryRow(`SELECT count() FROM system.processes WHERE startsWith(query_id, ?)`,
			ss.Handle()).Scan(&n); err == nil && n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the statement never reached the server")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// The window may only offer a stop button where there is something
	// behind it (FR-5.5).
	if !src.Capabilities().Query.Cancel {
		t.Error("a statement can be stopped and the window is not told so")
	}
	if err := src.KillQuery(context.Background(), ss.Handle()); err != nil {
		t.Fatalf("stopping it: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("a statement that was stopped finished")
		}
	case <-time.After(20 * time.Second):
		t.Error("the statement went on after it was stopped")
	}
}

// A read-only connection refuses a write here, before it reaches a server
// whose own read-only setting belongs to its user and not to this
// connection (NFR-S4).
func TestLiveReadOnlyRefusesAWriteHere(t *testing.T) {
	src := open(t, source.Guard{ReadOnly: true})
	ss := pinned(t, src)
	for _, sql := range []string{
		`INSERT INTO ikigai_it.writes VALUES (1, 'a', 1)`,
		`ALTER TABLE ikigai_it.writes UPDATE name = 'x' WHERE id = 1`,
		`DROP TABLE ikigai_it.nokey`,
		`OPTIMIZE TABLE ikigai_it.people FINAL`,
	} {
		if _, err := ss.Query(context.Background(), source.Statement{SQL: sql, Confirmed: true}); !errors.Is(err, source.ErrReadOnly) {
			t.Errorf("%q was refused with %v, want it refused as a write", sql, err)
		}
	}
	if rows := one(t, ss, `SELECT count() FROM ikigai_it.people`); len(rows) != 1 || rows[0][0] != int64(101) {
		t.Errorf("a read on a read-only connection gave %v", rows)
	}
}

// A script is refused whole, before any of it runs.
func TestLiveAScriptIsRefusedBeforeAnyOfItRuns(t *testing.T) {
	src := open(t, source.Guard{ReadOnly: true})
	_, err := src.QueryMulti(context.Background(),
		"SELECT 1; INSERT INTO ikigai_it.writes VALUES (1, 'a', 1)", source.ScriptOptions{Confirmed: true})
	if !errors.Is(err, source.ErrReadOnly) {
		t.Fatalf("the script was refused with %v", err)
	}
	rw := open(t, source.Guard{})
	ss := pinned(t, rw)
	if rows := one(t, ss, `SELECT count() FROM ikigai_it.writes`); len(rows) != 1 || rows[0][0] != int64(0) {
		t.Errorf("the refused script left %v behind", rows)
	}
}

// A production connection asks before it changes anything (FR-4.9).
func TestLiveProductionAsksFirst(t *testing.T) {
	src := open(t, source.Guard{Environment: source.EnvProduction})
	ss := pinned(t, src)
	ctx := context.Background()
	write := `INSERT INTO ikigai_it.writes VALUES (1, 'a', 1)`
	if _, err := ss.Query(ctx, source.Statement{SQL: write}); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("a write on a production connection: %v", err)
	}
	if _, err := ss.Query(ctx, source.Statement{SQL: write, Confirmed: true}); err != nil {
		t.Errorf("a write with consent: %v", err)
	}
	// A change over every row is worth asking about on its own, whatever
	// connection it is.
	plain := pinned(t, open(t, source.Guard{}))
	var unbounded *source.UnboundedError
	if _, err := plain.Query(ctx, source.Statement{SQL: `DELETE FROM ikigai_it.writes`}); !errors.As(err, &unbounded) {
		t.Errorf("a delete of every row: %v", err)
	}
}

// The picklist's values are the column's, most frequent first, and each one
// filters with exactly what it was given (FR-3.4).
func TestLiveDistinctValuesFilterWithThemselves(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	nokey := model.NewRef(model.KindTable, "ikigai_it", "nokey")
	vals, err := src.Distinct(ctx, nokey, "a", source.BrowseOptions{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 3 || vals[0].Value != int64(1) || vals[0].Count != 2 {
		t.Fatalf("the values are %+v", vals)
	}
	rs, err := src.Browse(ctx, nokey, source.BrowseOptions{Limit: 10,
		Filters: []source.Filter{{Column: "a", Op: source.OpIn, Values: []any{vals[0].Value}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(drain(t, rs)); got != 2 {
		t.Errorf("filtering by the first value found %d rows, want the 2 it was counted in", got)
	}
}

// A search finds what somebody typed, and a regular expression is a real
// one: ClickHouse has match().
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
	if got := count(source.Filter{Column: "name", Op: source.OpContains, Values: []any{"PERSON 1"}}); got != 12 {
		t.Errorf("a search for PERSON 1 found %d rows, want the 12 whose name holds it", got)
	}
	if got := count(source.Filter{Column: "name", Op: source.OpRegex, Values: []any{"^person 1[0-9]$"}}); got != 10 {
		t.Errorf("a pattern for person 10 to 19 found %d rows", got)
	}
	// The text of a search is not a pattern: nothing here holds a per cent.
	if got := count(source.Filter{Column: "name", Op: source.OpContains, Values: []any{"person%1"}}); got != 0 {
		t.Errorf("a search for a per cent found %d rows", got)
	}
}

// A statement with no result set is sent as one. Asking this driver for
// rows a statement does not have ends the connection, so what a tab would
// otherwise lose is everything it left behind.
func TestLiveAStatementWithNoResultKeepsTheConnection(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	one(t, ss, `CREATE TEMPORARY TABLE kept (a Int32)`)
	for _, sql := range []string{
		`SET max_threads = 4`,
		`INSERT INTO kept VALUES (1)`,
		`INSERT INTO ikigai_it.writes VALUES (5, 'five', 5)`,
		`ALTER TABLE ikigai_it.writes UPDATE name = 'FIVE' WHERE id = 5`,
		`OPTIMIZE TABLE ikigai_it.writes FINAL`,
	} {
		if _, err := ss.Query(context.Background(), source.Statement{SQL: sql, Confirmed: true}); err != nil {
			t.Fatalf("%q: %v", sql, err)
		}
	}
	// The temporary table is still there, so the connection never went.
	if rows := one(t, ss, `SELECT a FROM kept`); len(rows) != 1 || rows[0][0] != int64(1) {
		t.Errorf("the temporary table holds %v", rows)
	}
	// A statement that changes rows reports no count: ClickHouse says what
	// it wrote as progress, and a count of zero would read as a change that
	// did nothing.
	res, err := ss.Query(context.Background(), source.Statement{
		SQL: `INSERT INTO ikigai_it.writes VALUES (6, 'six', 6)`, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Affected != -1 || res.Rows != nil {
		t.Errorf("an insert reported %+v", res)
	}
	if res.Duration <= 0 {
		t.Errorf("it took %v", res.Duration)
	}
}

// Stopping a statement costs the connection it was running on, which is
// what this driver does and not a choice made here, so the session takes a
// new one — keeping its name, which is what anything stopping it knows it
// by. What the old connection held does not come back.
func TestLiveAStoppedStatementCostsTheConnectionAndNotTheTab(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	name := ss.Handle()
	one(t, ss, `CREATE TEMPORARY TABLE gone (a Int32)`)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := ss.Query(ctx, source.Statement{
		SQL: `SELECT number FROM numbers(100000000000) WHERE sipHash64(number) = 1`})
	if err == nil {
		_, err = res.Rows.Next(ctx)
		res.Rows.Close()
	}
	if err == nil {
		t.Fatal("a statement over a hundred thousand million rows finished")
	}

	if rows := one(t, ss, `SELECT 1`); len(rows) != 1 {
		t.Fatalf("the session answered %v after a statement was stopped", rows)
	}
	if ss.Handle() != name {
		t.Errorf("the session is now called %q and was %q", ss.Handle(), name)
	}
	if _, err := ss.Query(context.Background(), source.Statement{SQL: `SELECT a FROM gone`}); err == nil {
		t.Error("the temporary table outlived the connection it was made on")
	}
}

// A result read to its end leaves the connection as it was, so an ordinary
// query costs nothing.
func TestLiveAFinishedStatementKeepsTheConnection(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	one(t, ss, `CREATE TEMPORARY TABLE stays (a Int32)`)
	one(t, ss, `SELECT id FROM ikigai_it.people ORDER BY id LIMIT 5`)
	one(t, ss, `INSERT INTO stays VALUES (1)`)
	if rows := one(t, ss, `SELECT a FROM stays`); len(rows) != 1 {
		t.Errorf("the temporary table holds %v", rows)
	}
}
