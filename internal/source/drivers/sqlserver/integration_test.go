//go:build conformance

package sqlserver

// Integration tests against a real SQL Server (REQ-DRV-1, T3.30). They
// expect the ikigai-mssql container on port 51433 and skip if it is not
// running; IKIGAI_REQUIRE_MSSQL=1 makes that a failure, as in CI.

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
	if v := os.Getenv("IKIGAI_MSSQL_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return 51433
}

func password() string { return env("IKIGAI_MSSQL_PASSWORD", "Ikigai!2026") }

func config(db string, guard source.Guard) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: port(), User: "sa", Database: db,
		Secret: func(string) (string, error) { return password(), nil },
		TLS:    source.TLSConfig{Mode: "disable"}, Guard: guard}
}

// admin is a plain connection, for building the fixture.
func admin(t *testing.T, database string) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("sqlserver://sa:%s@127.0.0.1:%d?database=%s&encrypt=disable&connection+timeout=10",
		password(), port(), database)
	db, err := sql.Open("sqlserver", dsn)
	if err == nil {
		err = db.Ping()
	}
	if err != nil {
		if db != nil {
			db.Close()
		}
		if os.Getenv("IKIGAI_REQUIRE_MSSQL") != "" {
			t.Fatalf("SQL Server required but unavailable: %v", err)
		}
		t.Skipf("no SQL Server on port %d (docker start ikigai-mssql): %v", port(), err)
	}
	return db
}

var madeDatabase sync.Once

// batches are run one at a time: T-SQL has no multi-statement DDL batch that
// creates a database and then uses it.
var fixture = []string{
	`DROP TABLE IF EXISTS dbo.orders`,
	`DROP VIEW IF EXISTS dbo.adults`,
	`DROP TABLE IF EXISTS dbo.people`,
	`DROP TABLE IF EXISTS dbo.writes`,
	`DROP SEQUENCE IF EXISTS dbo.writes_id`,
	`DROP TABLE IF EXISTS dbo.nokey`,
	`DROP TABLE IF EXISTS dbo.pairref`,
	`DROP TABLE IF EXISTS dbo.pair`,
	`DROP TABLE IF EXISTS dbo.keyed`,
	`DROP TABLE IF EXISTS dbo.uniq`,
	`DROP TABLE IF EXISTS dbo.autoid`,
	`DROP PROCEDURE IF EXISTS dbo.count_people`,
	`DROP FUNCTION IF EXISTS dbo.twice`,
	`DROP TYPE IF EXISTS dbo.short_name`,
	`DROP SCHEMA IF EXISTS empty`,
	`DROP TABLE IF EXISTS aaa.only_here`,
	`DROP SCHEMA IF EXISTS aaa`,
	`CREATE SCHEMA empty`,
	// A schema sorting before dbo, so that dbo coming first is the order
	// this driver asks for rather than the alphabet's.
	`CREATE SCHEMA aaa`,
	// A table outside dbo, so that a schema's classes are counted for that
	// schema rather than for the database.
	`CREATE TABLE aaa.only_here (a int)`,
	`CREATE TABLE dbo.people (id int PRIMARY KEY, name nvarchar(50) NOT NULL,
	   score decimal(30,10), born date, seen datetime2, meta nvarchar(max), pic varbinary(max),
	   tag uniqueidentifier, ok bit)`,
	`INSERT INTO dbo.people (id, name, score, born, meta, ok)
	 SELECT i, CONCAT('person ', i), i * 1.5,
	        CASE WHEN i % 3 = 0 THEN DATEADD(day, i % 2, '2000-01-01') END,
	        CONCAT('{"i": ', i, '}'), CASE WHEN i % 2 = 0 THEN 1 ELSE 0 END
	 FROM (SELECT TOP (100) ROW_NUMBER() OVER (ORDER BY (SELECT NULL)) AS i
	       FROM sys.all_objects) AS n`,
	`INSERT INTO dbo.people (id, name, score, tag)
	 VALUES (1000, 'exact', 12345678901234567890.1234567890, '6F9619FF-8B86-D011-B42D-00C04FC964FF')`,
	// A sequence rather than IDENTITY: the conformance contract's writable
	// table is numbered by the server when no id is given and takes one when
	// it is, and SQL Server refuses an explicit value for an identity column.
	`CREATE SEQUENCE dbo.writes_id AS int START WITH 1 INCREMENT BY 1`,
	`CREATE TABLE dbo.writes (id int NOT NULL PRIMARY KEY
	     CONSTRAINT df_writes_id DEFAULT (NEXT VALUE FOR dbo.writes_id),
	   name nvarchar(40) NOT NULL CONSTRAINT df_writes_name DEFAULT 'none', n int)`,
	// notes is a column no ORDER BY may name, and a table with no key is
	// ordered by every column it can be.
	`CREATE TABLE dbo.nokey (a int, b nvarchar(10), notes xml)`,
	`INSERT INTO dbo.nokey (a, b) VALUES (2, 'y'), (1, 'x'), (1, 'x'), (3, 'z')`,
	// A column the server numbers, and one it works out.
	`CREATE TABLE dbo.autoid (id int IDENTITY(1,1) PRIMARY KEY, a int, twice AS (a * 2))`,
	`INSERT INTO dbo.autoid (a) VALUES (21)`,
	`CREATE TABLE dbo.orders (id int PRIMARY KEY, person_id int, total decimal(10,2),
	   CONSTRAINT ck_total CHECK (total >= 0),
	   CONSTRAINT fk_person FOREIGN KEY (person_id) REFERENCES dbo.people (id) ON DELETE CASCADE)`,
	// A table with a key and two unique indexes, one of them over a column
	// that may be NULL: paging must take the key.
	`CREATE TABLE dbo.keyed (id int PRIMARY KEY, code nvarchar(10) NOT NULL CONSTRAINT uq_code UNIQUE,
	   opt int NULL CONSTRAINT uq_opt UNIQUE)`,
	`INSERT INTO dbo.keyed (id, code, opt) VALUES (2, 'b', NULL), (1, 'a', 1)`,
	// A table with no key at all, whose only unique index over columns that
	// cannot be NULL is what paging has to find.
	`CREATE TABLE dbo.uniq (opt int NULL, code nvarchar(10) NOT NULL, extra int NOT NULL)`,
	// Made first, so that an index taken in the catalogue's order without
	// asking whether it is unique would be this one.
	`CREATE INDEX ix_extra ON dbo.uniq (extra)`,
	`CREATE UNIQUE INDEX ux_opt ON dbo.uniq (opt)`,
	`CREATE UNIQUE INDEX ux_code ON dbo.uniq (code) INCLUDE (extra)`,
	`INSERT INTO dbo.uniq (opt, code, extra) VALUES (2, 'b', 20), (1, 'a', 10)`,
	`CREATE INDEX orders_total ON dbo.orders (total DESC)`,
	// A key of two columns, declared in the other order from the table's, so
	// that a key read in the catalogue's order would read it wrongly.
	`CREATE TABLE dbo.pair (a int NOT NULL, b int NOT NULL, c int,
	   CONSTRAINT pk_pair PRIMARY KEY (b, a))`,
	`INSERT INTO dbo.pair (a, b, c) VALUES (1, 2, 3), (2, 1, 4)`,
	`CREATE TABLE dbo.pairref (x int NOT NULL, y int NOT NULL,
	   CONSTRAINT fk_pair FOREIGN KEY (y, x) REFERENCES dbo.pair (b, a))`,
	`CREATE VIEW adults AS SELECT id, name FROM dbo.people WHERE id > 10`,
	`CREATE TYPE dbo.short_name FROM nvarchar(10) NOT NULL`,
	`CREATE PROCEDURE dbo.count_people AS SELECT count(*) FROM dbo.people`,
	`CREATE FUNCTION dbo.twice (@n int) RETURNS int AS BEGIN RETURN @n * 2; END`,
	`CREATE TRIGGER trg_writes ON dbo.writes AFTER INSERT AS SET NOCOUNT ON`,
	`EXEC sys.sp_addextendedproperty @name = N'MS_Description', @value = N'Everyone we know',
	   @level0type = N'SCHEMA', @level0name = N'dbo', @level1type = N'TABLE', @level1name = N'people'`,
	`EXEC sys.sp_addextendedproperty @name = N'MS_Description', @value = N'What they are called',
	   @level0type = N'SCHEMA', @level0name = N'dbo', @level1type = N'TABLE', @level1name = N'people',
	   @level2type = N'COLUMN', @level2name = N'name'`,
}

// build makes the fixture. The database is made once per run and its tables
// per test, so that a test that writes cannot reach the next one.
func build(t *testing.T) {
	t.Helper()
	madeDatabase.Do(func() {
		master := admin(t, "master")
		defer master.Close()
		for _, db := range []string{"ikigai_it", "ikigai_it2"} {
			if _, err := master.Exec(`IF DB_ID('` + db + `') IS NOT NULL
				BEGIN
					ALTER DATABASE ` + db + ` SET SINGLE_USER WITH ROLLBACK IMMEDIATE;
					DROP DATABASE ` + db + `;
				END`); err != nil {
				t.Fatalf("dropping %s: %v", db, err)
			}
			if _, err := master.Exec(`CREATE DATABASE ` + db); err != nil {
				t.Fatalf("creating %s: %v", db, err)
			}
		}
	})
	// Each batch on its own: CREATE VIEW and its kin must be the only
	// statement in one, which is the rule the splitter is written around.
	db := admin(t, "ikigai_it")
	defer db.Close()
	for _, stmt := range fixture {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("fixture %.60q: %v", stmt, err)
		}
	}
}

func open(t *testing.T, guard source.Guard) *sqlServerSource {
	t.Helper()
	build(t)
	src, err := Driver{}.Open(context.Background(), config("ikigai_it", guard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	return src.(*sqlServerSource)
}

var people = model.NewRef(model.KindTable, "ikigai_it", "dbo", "people")

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
		Name: "sqlserver",
		Open: func(ctx context.Context, t *testing.T) source.Source {
			src, err := Driver{}.Open(ctx, config("ikigai_it", source.Guard{}))
			if err != nil {
				t.Fatal(err)
			}
			return src
		},
		Browsable: people,
		Writable:  model.NewRef(model.KindTable, "ikigai_it", "dbo", "writes"),
		OpenGuarded: func(ctx context.Context, t *testing.T, g source.Guard) source.Source {
			src, err := Driver{}.Open(ctx, config("ikigai_it", g))
			if err != nil {
				t.Fatal(err)
			}
			return src
		},
	})
}

// A value keeps the shape the model gives it: an exact number is not a
// float, and a uniqueidentifier is not sixteen bytes.
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
	at := map[string]int{}
	for i, c := range rs.Columns() {
		at[c.Name] = i
	}
	if got, want := rows[0][at["score"]], model.Decimal("12345678901234567890.1234567890"); got != want {
		t.Errorf("score is %#v, want %#v", got, want)
	}
	if got, want := rows[0][at["tag"]], "6F9619FF-8B86-D011-B42D-00C04FC964FF"; got != want {
		t.Errorf("tag is %#v, want %#v", got, want)
	}
	if got, ok := rows[0][at["id"]].(int64); !ok || got != 1000 {
		t.Errorf("id is %#v, want an int64", rows[0][at["id"]])
	}
}

// nodes is the labels of a set of nodes, for comparing a listing.
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

// The explorer walks a server, a database, a schema and a table (FR-2.1).
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
	if m := find(roots, "master"); m == nil || m.Attrs["system"] != "true" {
		t.Errorf("master is not marked as the server's own: %+v", m)
	}
	if find(roots, "tempdb") == nil {
		t.Errorf("the databases are %q", labels(roots))
	}

	schemas, err := src.Children(ctx, ours.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) == 0 || schemas[0].Label != "dbo" {
		t.Fatalf("the schemas are %q, want dbo first", labels(schemas))
	}
	for _, hidden := range []string{"sys", "INFORMATION_SCHEMA", "guest", "db_owner", "db_datareader"} {
		if find(schemas, hidden) != nil {
			t.Errorf("%s is the server's own and is listed: %q", hidden, labels(schemas))
		}
	}

	folders, err := src.Children(ctx, schemas[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	tables := find(folders, "Tables")
	if tables == nil {
		t.Fatalf("the classes are %q", labels(folders))
	}
	// The nine in dbo, and not the one in aaa: a folder counts its schema's.
	if tables.Badge == nil || tables.Badge.Text != "9" {
		t.Errorf("the tables folder counts %+v, want the nine dbo holds", tables.Badge)
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

	cols, err := src.Children(ctx, p.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := labels(cols), []string{"id", "name", "score", "born", "seen", "meta", "pic", "tag", "ok"}; !slices.Equal(got, want) {
		t.Fatalf("the columns are %q, want %q", got, want)
	}
	if cols[0].Attrs["key"] != "primary" || cols[0].Attrs["type"] != "int" {
		t.Errorf("id is %+v", cols[0].Attrs)
	}
	if cols[1].Attrs["type"] != "nvarchar(50)" || cols[1].Attrs["nullable"] != "false" {
		t.Errorf("name is %+v", cols[1].Attrs)
	}
	if cols[1].Attrs["key"] != "" {
		t.Errorf("name is no part of the key and says %q", cols[1].Attrs["key"])
	}
	if cols[2].Attrs["type"] != "decimal(30,10)" || cols[2].Attrs["nullable"] != "true" {
		t.Errorf("score is %+v", cols[2].Attrs)
	}
}

// An index and a trigger are named with the table they are on, because two
// tables may each have a PK_id.
func TestLiveAnIndexIsNamedWithItsTable(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	folder := model.NewRef(model.KindFolder, "ikigai_it", "dbo", "index")
	ixs, err := src.Children(ctx, folder)
	if err != nil {
		t.Fatal(err)
	}
	var found *model.Node
	for i := range ixs {
		if ixs[i].Attrs["table"] == "orders" && strings.HasPrefix(ixs[i].Label, "orders_total") {
			found = &ixs[i]
		}
	}
	if found == nil {
		t.Fatalf("the indexes are %q", labels(ixs))
	}
	if n := len(found.Ref.Path); n != 4 || found.Ref.Path[2] != "orders" {
		t.Errorf("the index is at %v, want its table in its path", found.Ref.Path)
	}
}

// A table's structure is what the structure tab and the diff engine need:
// more than the tree shows (FR-2.3).
func TestLiveATableIsDescribed(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	got, err := src.Describe(ctx, model.NewRef(model.KindTable, "ikigai_it", "dbo", "orders"))
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := got.(*model.Table)
	if !ok {
		t.Fatalf("a table is described as %T", got)
	}
	if tbl.PrimaryKey == nil || !slices.Equal(tbl.PrimaryKey.Columns, []string{"id"}) {
		t.Errorf("the primary key is %+v", tbl.PrimaryKey)
	}
	if len(tbl.ForeignKeys) != 1 {
		t.Fatalf("the foreign keys are %+v", tbl.ForeignKeys)
	}
	fk := tbl.ForeignKeys[0]
	if fk.Name != "fk_person" || fk.RefSchema != "dbo" || fk.RefTable != "people" ||
		!slices.Equal(fk.Columns, []string{"person_id"}) || !slices.Equal(fk.RefColumns, []string{"id"}) {
		t.Errorf("the foreign key is %+v", fk)
	}
	if fk.OnDelete != model.ActionCascade || fk.OnUpdate != model.ActionNoAction {
		t.Errorf("the foreign key's behaviour is %q on delete and %q on update", fk.OnDelete, fk.OnUpdate)
	}
	var ix *model.Index
	for i := range tbl.Indexes {
		if tbl.Indexes[i].Name == "orders_total" {
			ix = &tbl.Indexes[i]
		}
	}
	if ix == nil || len(ix.Columns) != 1 || ix.Columns[0].Name != "total" || !ix.Columns[0].Descending {
		t.Errorf("the indexes are %+v", tbl.Indexes)
	}
	if ix != nil && ix.Method != "nonclustered" {
		t.Errorf("the index's method is %q", ix.Method)
	}
	if tbl.RowsEstimate != 0 {
		t.Errorf("orders holds %d rows", tbl.RowsEstimate)
	}
}

// A column's default is shown as it was written, not wrapped in the
// brackets the server stored it in.
func TestLiveADefaultIsShownAsItWasWritten(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), model.NewRef(model.KindTable, "ikigai_it", "dbo", "writes"))
	if err != nil {
		t.Fatal(err)
	}
	tbl := got.(*model.Table)
	at := map[string]model.Column{}
	for _, c := range tbl.Columns {
		at[c.Name] = c
	}
	if c := at["name"]; !c.HasDefault || c.Default != "'none'" {
		t.Errorf("name's default is %q (has one: %v)", c.Default, c.HasDefault)
	}
	if c := at["id"]; !c.HasDefault || !strings.Contains(c.Default, "NEXT VALUE FOR") {
		t.Errorf("id's default is %q", c.Default)
	}
	if c := at["n"]; c.HasDefault {
		t.Errorf("n has a default it was never given: %q", c.Default)
	}
}

// A view is described by what it selects.
func TestLiveAViewIsDescribed(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), model.NewRef(model.KindView, "ikigai_it", "dbo", "adults"))
	if err != nil {
		t.Fatal(err)
	}
	v, ok := got.(*model.View)
	if !ok {
		t.Fatalf("a view is described as %T", got)
	}
	if !strings.Contains(v.Definition, "FROM dbo.people") {
		t.Errorf("the view is defined as %q", v.Definition)
	}
	if got, want := len(v.Columns), 2; got != want {
		t.Errorf("the view has %d columns, want %d", got, want)
	}
}

// A table with no key of its own still pages without repeating or losing a
// row: SQL Server has no row address to fall back on.
func TestLivePagingWithoutAKeyIsStillStable(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	nokey := model.NewRef(model.KindTable, "ikigai_it", "dbo", "nokey")
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
		if model.IdentityOf(rs).Kind != model.IdentityNone {
			t.Errorf("a table with no key was said to have one")
		}
	}
	want := []string{"[1 x <nil>]", "[1 x <nil>]", "[2 y <nil>]", "[3 z <nil>]"}
	if !slices.Equal(seen, want) {
		t.Errorf("the pages held %q, want %q", seen, want)
	}
}

func pinned(t *testing.T, src *sqlServerSource) source.Session {
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
	res, err := ss.Query(context.Background(), source.Statement{SQL: sql})
	if err != nil {
		t.Fatalf("%q: %v", sql, err)
	}
	if res.Rows == nil {
		return nil
	}
	return drain(t, res.Rows)
}

// A query tab is one connection, so what a statement leaves behind is there
// for the next one: a variable, a temporary table, a setting.
func TestLiveASessionKeepsWhatAStatementLeft(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	one(t, ss, `CREATE TABLE #scratch (a int)`)
	one(t, ss, `INSERT INTO #scratch VALUES (7)`)
	rows := one(t, ss, `SELECT a FROM #scratch`)
	if len(rows) != 1 || rows[0][0] != int64(7) {
		t.Errorf("the temporary table holds %v", rows)
	}
	if ss.Handle() == "" || ss.Handle() == "0" {
		t.Errorf("the session is called %q", ss.Handle())
	}
	// Another session cannot see it: they are different connections.
	other := pinned(t, src)
	if _, err := other.Query(context.Background(), source.Statement{SQL: `SELECT a FROM #scratch`}); err == nil {
		t.Error("another session saw this one's temporary table")
	}
}

// A statement that changes rows says how many (FR-5.2).
func TestLiveAWriteSaysHowManyRowsItChanged(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	res, err := ss.Query(context.Background(), source.Statement{
		SQL: `INSERT INTO dbo.writes (name, n) VALUES ('a', 1), ('b', 2)`, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Affected != 2 {
		t.Errorf("it says %d rows, want 2", res.Affected)
	}
	if res.Duration <= 0 {
		t.Errorf("it took %v", res.Duration)
	}
	res, err = ss.Query(context.Background(), source.Statement{
		SQL: `UPDATE dbo.writes SET n = n + 1 WHERE name = 'a'`, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Affected != 1 {
		t.Errorf("the update says %d rows, want 1", res.Affected)
	}
	// A read reports no count, because a count of rows read is the rows.
	res, err = ss.Query(context.Background(), source.Statement{SQL: `SELECT 1`})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Rows.Close()
	if res.Affected != -1 {
		t.Errorf("a read says %d rows changed", res.Affected)
	}
	// Nor does a statement that returns nothing and changes nothing: a
	// count of nothing would read as a change that did nothing.
	res, err = ss.Query(context.Background(), source.Statement{SQL: `DECLARE @x int`})
	if err != nil {
		t.Fatal(err)
	}
	if res.Affected != -1 {
		t.Errorf("declaring a variable says %d rows changed", res.Affected)
	}
}

// Named parameters are bound, never written in (FR-5.7, NFR-S6).
func TestLiveNamedParametersAreBound(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	res, err := ss.Query(context.Background(), source.Statement{
		SQL:   `SELECT name FROM dbo.people WHERE id = :who OR id = :who + 1 ORDER BY id`,
		Named: map[string]any{"who": int64(5)}})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, res.Rows)
	if len(rows) != 2 || rows[0][0] != "person 5" || rows[1][0] != "person 6" {
		t.Errorf("the rows are %v", rows)
	}
	// A name used twice is one parameter given once, which is what binding
	// by name means.
	if _, err := ss.Query(context.Background(), source.Statement{
		SQL: `SELECT :who`, Named: map[string]any{"who": 1}, Args: []any{1}}); err == nil {
		t.Error("a statement mixing named and positional parameters was accepted")
	}
}

// A script is run batch by batch, and GO is never sent (FR-5.4).
func TestLiveAScriptRunsItsBatches(t *testing.T) {
	src := open(t, source.Guard{})
	script := "CREATE PROCEDURE dbo.two AS BEGIN SELECT 1; SELECT 2; END\nGO\n" +
		"SELECT 'after' AS said\nGO\nDROP PROCEDURE dbo.two"
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
				said = append(said, fmt.Sprint(row[0]))
			}
		}
	}
	if !slices.Equal(said, []string{"after"}) {
		t.Errorf("the script said %q", said)
	}
}

// Where the server says an error was is where the editor points (FR-5.10).
func TestLiveAnErrorSaysWhereItWas(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	_, err := ss.Query(context.Background(), source.Statement{SQL: "SELECT 1,\nFROM x"})
	var se *source.StatementError
	if !errors.As(err, &se) {
		t.Fatalf("the failure is %v", err)
	}
	if se.Message.Code != "156" {
		t.Errorf("the code is %q, want the server's own", se.Message.Code)
	}
	if se.Message.Position != 11 {
		t.Errorf("it points at character %d, want the start of the second line", se.Message.Position)
	}
	// A name the server could not resolve is reported against the first
	// line, because that is where it says the error was; what is claimed
	// here is only that where it says is where the editor points.
	_, err = ss.Query(context.Background(), source.Statement{SQL: "SELECT 1\nFROM nowhere_at_all"})
	if !errors.As(err, &se) || se.Message.Code != "208" || se.Message.Position != 0 {
		t.Errorf("an unresolved name is reported as %+v", err)
	}
}

// A statement is stopped at the server, and the tab goes on working.
//
// Stopping one costs the connection it was running on, which is SQL
// Server's own behaviour and not a choice made here, so the session takes a
// new one. What the old connection held does not come back, and this says
// so rather than leaving somebody to find out.
func TestLiveAStoppedStatementCostsTheConnectionAndNotTheTab(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	before := ss.Handle()
	one(t, ss, `CREATE TABLE #gone (a int)`)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	if _, err := ss.Query(ctx, source.Statement{SQL: `WAITFOR DELAY '00:00:30'`}); err == nil {
		t.Fatal("a statement that waits half a minute finished")
	}
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("stopping it took %v", took)
	}

	rows := one(t, ss, `SELECT @@SPID`)
	if len(rows) != 1 {
		t.Fatalf("the session answered %v after a statement was stopped", rows)
	}
	if got := fmt.Sprint(rows[0][0]); got == before {
		t.Errorf("the session is still %s, and stopping a statement ends one", got)
	} else if got != ss.Handle() {
		t.Errorf("the session says it is %s and the server says %s", ss.Handle(), got)
	}
	if _, err := ss.Query(context.Background(), source.Statement{SQL: `SELECT a FROM #gone`}); err == nil {
		t.Error("the temporary table outlived the connection it was made on")
	}
}

// A result read to its end leaves the connection as it was, so an ordinary
// query costs nothing.
func TestLiveAFinishedStatementKeepsTheConnection(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	before := ss.Handle()
	one(t, ss, `CREATE TABLE #kept (a int)`)
	one(t, ss, `SELECT TOP (5) id FROM dbo.people ORDER BY id`)
	one(t, ss, `INSERT INTO #kept VALUES (1)`)
	if rows := one(t, ss, `SELECT a FROM #kept`); len(rows) != 1 {
		t.Errorf("the temporary table holds %v", rows)
	}
	if ss.Handle() != before {
		t.Errorf("the session changed from %s to %s with nothing stopped", before, ss.Handle())
	}
}

// The rows a grid can read, written as INSERT statements, read back as the
// same rows (FR-3.7).
func TestLiveCopyAsInsertReadsBack(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	rs, err := src.Browse(ctx, people, source.BrowseOptions{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	cols := rs.Columns()
	rows := drain(t, rs)
	copyInto := model.NewRef(model.KindTable, "ikigai_it", "dbo", "people_copy")
	script, err := src.InsertRows(copyInto, cols, rows)
	if err != nil {
		t.Fatal(err)
	}
	ss := pinned(t, src)
	one(t, ss, `SELECT * INTO dbo.people_copy FROM dbo.people WHERE 1 = 0`)
	defer func() { one(t, ss, `DROP TABLE dbo.people_copy`) }()
	for _, st := range src.SplitScript(script) {
		if _, err := ss.Query(ctx, source.Statement{SQL: st.Text, Confirmed: true}); err != nil {
			t.Fatalf("%.80q: %v", st.Text, err)
		}
	}
	back, err := src.Browse(ctx, copyInto, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := drain(t, back)
	if len(got) != len(rows) {
		t.Fatalf("%d rows were written back, want %d", len(got), len(rows))
	}
	for i := range got {
		if fmt.Sprint(got[i]) != fmt.Sprint(rows[i]) {
			t.Errorf("row %d came back as %v, want %v", i, got[i], rows[i])
		}
	}
}

// A read-only connection refuses a write here, before it reaches a server
// that has no read-only session of its own to fall back on (NFR-S4).
func TestLiveReadOnlyRefusesAWriteHere(t *testing.T) {
	src := open(t, source.Guard{ReadOnly: true})
	ss := pinned(t, src)
	for _, sql := range []string{
		`DELETE FROM dbo.writes`,
		`UPDATE dbo.people SET name = 'x' WHERE id = 1`,
		`DROP TABLE dbo.nokey`,
		`EXEC dbo.anything`,
	} {
		if _, err := ss.Query(context.Background(), source.Statement{SQL: sql, Confirmed: true}); !errors.Is(err, source.ErrReadOnly) {
			t.Errorf("%q was refused with %v, want it refused as a write", sql, err)
		}
	}
	if rows := one(t, ss, `SELECT count(*) FROM dbo.people`); len(rows) != 1 || rows[0][0] != int64(101) {
		t.Errorf("a read on a read-only connection gave %v", rows)
	}
}

// The picklist's values are the column's, most frequent first, and each one
// filters with exactly what it was given (FR-3.4).
func TestLiveDistinctValuesFilterWithThemselves(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	nokey := model.NewRef(model.KindTable, "ikigai_it", "dbo", "nokey")
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

// A schema holding nothing still shows a folder, because a node that says
// it has children and then lists none draws an expander onto nothing
// (FR-2.2).
func TestLiveAnEmptySchemaStillShowsAFolder(t *testing.T) {
	src := open(t, source.Guard{})
	folders, err := src.Children(context.Background(), model.NewRef(model.KindSchema, "ikigai_it", "empty"))
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 || folders[0].Label != "Tables" {
		t.Fatalf("an empty schema holds %q", labels(folders))
	}
	if folders[0].HasChildren {
		t.Errorf("the empty folder says it has children: %+v", folders[0])
	}
}

// Paging takes the primary key where there is one, whatever else is unique.
func TestLivePagingTakesTheKey(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	rs, err := src.Browse(ctx, model.NewRef(model.KindTable, "ikigai_it", "dbo", "keyed"),
		source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	id := model.IdentityOf(rs)
	if id.Kind != model.IdentityPrimaryKey || !slices.Equal(id.Columns, []string{"id"}) {
		t.Errorf("the rows are known by %+v, want the primary key", id)
	}
	if len(rows) != 2 || rows[0][0] != int64(1) {
		t.Errorf("the rows came in the order %v, want the key's", rows)
	}
}

// Without a key, a unique index over columns that cannot be NULL is the
// next best thing — and one over a column that can be is not, because SQL
// Server holds one NULL row in it and no more.
func TestLivePagingTakesAUniqueIndexThatCannotBeNull(t *testing.T) {
	src := open(t, source.Guard{})
	rs, err := src.Browse(context.Background(), model.NewRef(model.KindTable, "ikigai_it", "dbo", "uniq"),
		source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	id := model.IdentityOf(rs)
	if id.Kind != model.IdentityUniqueIndex || !slices.Equal(id.Columns, []string{"code"}) {
		t.Errorf("the rows are known by %+v, want the unique index over code", id)
	}
	if len(rows) != 2 || rows[0][1] != "a" {
		t.Errorf("the rows came in the order %v, want the index's", rows)
	}
}

// What the catalogue keeps as one thing is three: a primary key, a unique
// constraint and an index.
func TestLiveAKeyAConstraintAndAnIndexAreToldApart(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), model.NewRef(model.KindTable, "ikigai_it", "dbo", "keyed"))
	if err != nil {
		t.Fatal(err)
	}
	tbl := got.(*model.Table)
	if tbl.PrimaryKey == nil || !slices.Equal(tbl.PrimaryKey.Columns, []string{"id"}) {
		t.Errorf("the primary key is %+v", tbl.PrimaryKey)
	}
	names := func() []string {
		var out []string
		for _, u := range tbl.Uniques {
			out = append(out, u.Name)
		}
		return out
	}()
	slices.Sort(names)
	if !slices.Equal(names, []string{"uq_code", "uq_opt"}) {
		t.Errorf("the unique constraints are %q", names)
	}
	if len(tbl.Indexes) != 0 {
		t.Errorf("a key and two constraints were also listed as indexes: %+v", tbl.Indexes)
	}
}

// An index's payload columns are not its key, and are shown as what they
// are.
func TestLiveAnIndexesPayloadIsNotItsKey(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), model.NewRef(model.KindTable, "ikigai_it", "dbo", "uniq"))
	if err != nil {
		t.Fatal(err)
	}
	tbl := got.(*model.Table)
	var ix *model.Index
	for i := range tbl.Indexes {
		if tbl.Indexes[i].Name == "ux_code" {
			ix = &tbl.Indexes[i]
		}
	}
	if ix == nil {
		t.Fatalf("the indexes are %+v", tbl.Indexes)
	}
	if len(ix.Columns) != 1 || ix.Columns[0].Name != "code" {
		t.Errorf("the index's columns are %+v", ix.Columns)
	}
	if !slices.Equal(ix.Include, []string{"extra"}) {
		t.Errorf("the index's payload is %q", ix.Include)
	}
	if !ix.Unique {
		t.Error("a unique index was not said to be unique")
	}
}

// A check constraint is read, and shown as it was written.
func TestLiveACheckConstraintIsRead(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), model.NewRef(model.KindTable, "ikigai_it", "dbo", "orders"))
	if err != nil {
		t.Fatal(err)
	}
	tbl := got.(*model.Table)
	if len(tbl.Checks) != 1 || tbl.Checks[0].Name != "ck_total" {
		t.Fatalf("the checks are %+v", tbl.Checks)
	}
	if got := tbl.Checks[0].Expression; got != "[total]>=(0)" {
		t.Errorf("the check reads %q", got)
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

// A count for the tree, asked for one node at a time (FR-2.5).
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
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindView, "ikigai_it", "dbo", "adults")); ok || err != nil {
		t.Errorf("a view was counted: %v %v", ok, err)
	}
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindTable, "ikigai_it", "dbo", "not_there")); ok || err != nil {
		t.Errorf("a table that is not there was counted: %v %v", ok, err)
	}
	if _, ok, err := src.Badge(ctx, model.NewRef(model.KindTable, "ikigai_it", "people")); ok || err != nil {
		t.Errorf("a reference naming no schema was counted: %v %v", ok, err)
	}
}

// A reference names the database it is in, and the statement runs there: a
// connection is to one database, and reaching another is another connection.
func TestLiveAnotherDatabaseIsAnotherConnection(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	got, err := src.Describe(ctx, model.NewRef(model.KindView, "master", "dbo", "spt_values"))
	if err != nil {
		t.Fatalf("a table in another database: %v", err)
	}
	if len(got.(*model.View).Columns) == 0 {
		t.Errorf("it was described as %+v", got)
	}
	if _, err := src.Describe(ctx, model.NewRef(model.KindView, "ikigai_it", "dbo", "spt_values")); err == nil {
		t.Error("a table of master was found in the database this connection is on")
	}
}

// A script is refused whole, before any of it runs: refusing the fourth
// statement after three have run would leave somebody where they did not
// choose to be.
func TestLiveAScriptIsRefusedBeforeAnyOfItRuns(t *testing.T) {
	src := open(t, source.Guard{ReadOnly: true})
	ctx := context.Background()
	_, err := src.QueryMulti(ctx, "SELECT 1\nGO\nDELETE FROM dbo.writes", source.ScriptOptions{Confirmed: true})
	if !errors.Is(err, source.ErrReadOnly) {
		t.Fatalf("the script was refused with %v", err)
	}
	rw := open(t, source.Guard{})
	ss := pinned(t, rw)
	if rows := one(t, ss, `SELECT count(*) FROM dbo.writes`); len(rows) != 1 || rows[0][0] != int64(0) {
		t.Errorf("the refused script left %v behind", rows)
	}
}

// A result that is not the last is held, so the next statement can have the
// connection — and bounded, because holding a whole table would not fit
// (NFR-P11).
func TestLiveAnEarlierResultIsHeldWhileTheRestRuns(t *testing.T) {
	src := open(t, source.Guard{})
	ch, err := src.QueryMulti(context.Background(),
		"SELECT id FROM dbo.people ORDER BY id; SELECT 'last' AS said", source.ScriptOptions{})
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
	// The first is read after the second has run, which only a held result
	// allows: one connection holds one open result at a time.
	first := drain(t, results[0].Result.Rows)
	if len(first) != 101 {
		t.Errorf("the first result holds %d rows", len(first))
	}
	if got := drain(t, results[1].Result.Rows); len(got) != 1 || got[0][0] != "last" {
		t.Errorf("the last result is %v", got)
	}
}

// Every class the explorer shows lists what it holds, named as what it is.
func TestLiveEachClassListsItsOwn(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	at := func(kind model.ObjectKind) []model.Node {
		t.Helper()
		ns, err := src.Children(ctx, model.NewRef(model.KindFolder, "ikigai_it", "dbo", string(kind)))
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		return ns
	}
	views := at(model.KindView)
	if len(views) != 1 || views[0].Label != "adults" || !views[0].Browsable {
		t.Errorf("the views are %+v", views)
	}
	routines := at(model.KindRoutine)
	proc, fn := find(routines, "count_people"), find(routines, "twice")
	if proc == nil || proc.Attrs["kind"] != "procedure" {
		t.Errorf("the routines are %q: %+v", labels(routines), proc)
	}
	if fn == nil || fn.Attrs["kind"] != "function" {
		t.Errorf("the routines are %q: %+v", labels(routines), fn)
	}
	triggers := at(model.KindTrigger)
	if len(triggers) != 1 || triggers[0].Attrs["table"] != "writes" ||
		!strings.Contains(triggers[0].Label, "trg_writes") {
		t.Errorf("the triggers are %+v", triggers)
	}
	if n := len(triggers[0].Ref.Path); n != 4 || triggers[0].Ref.Path[2] != "writes" {
		t.Errorf("the trigger is at %v, want its table in its path", triggers[0].Ref.Path)
	}
	seqs := at(model.KindSequence)
	if len(seqs) != 1 || seqs[0].Label != "writes_id" {
		t.Errorf("the sequences are %q", labels(seqs))
	}
	types := at(model.KindUserType)
	if len(types) != 1 || types[0].Label != "short_name" || types[0].Attrs["kind"] != "alias" {
		t.Errorf("the types are %+v", types)
	}
}

// A column the server numbers and a column it works out are shown as what
// they are.
func TestLiveAColumnTheServerFillsInSaysSo(t *testing.T) {
	src := open(t, source.Guard{})
	got, err := src.Describe(context.Background(), model.NewRef(model.KindTable, "ikigai_it", "dbo", "autoid"))
	if err != nil {
		t.Fatal(err)
	}
	at := map[string]model.Column{}
	for _, c := range got.(*model.Table).Columns {
		at[c.Name] = c
	}
	if c := at["id"]; !c.Identity || !c.AutoIncrement {
		t.Errorf("id is %+v, want a column the server numbers", c)
	}
	if c := at["a"]; c.Identity {
		t.Errorf("a is %+v, want a column somebody fills in", c)
	}
	if c := at["twice"]; c.Generated != "[a]*(2)" {
		t.Errorf("twice is worked out as %q", c.Generated)
	}
	if c := at["a"]; c.Generated != "" {
		t.Errorf("a is worked out as %q", c.Generated)
	}
}

// A reference that names no schema names nothing that can be read.
func TestLiveAnIncompleteReferenceIsRefused(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	for _, ref := range []model.ObjectRef{
		model.NewRef(model.KindTable, "people"),
		// A database that is there, and no schema: the reference is one part
		// short and every name in it means something else.
		model.NewRef(model.KindTable, "ikigai_it", "people"),
	} {
		if _, err := src.Browse(ctx, ref, source.BrowseOptions{Limit: 1}); err == nil {
			t.Errorf("%s was browsable", ref)
		}
		if _, err := src.Describe(ctx, ref); err == nil {
			t.Errorf("%s was described", ref)
		}
	}
}

// A production connection asks before it changes anything (FR-4.9).
func TestLiveProductionAsksFirst(t *testing.T) {
	src := open(t, source.Guard{Environment: source.EnvProduction})
	ss := pinned(t, src)
	ctx := context.Background()
	if _, err := ss.Query(ctx, source.Statement{SQL: `DELETE FROM dbo.writes WHERE id = 1`}); !errors.Is(err, source.ErrConfirmationRequired) {
		t.Errorf("a write on a production connection: %v", err)
	}
	if _, err := ss.Query(ctx, source.Statement{SQL: `DELETE FROM dbo.writes WHERE id = 1`, Confirmed: true}); err != nil {
		t.Errorf("a write with consent: %v", err)
	}
	// A change over every row is worth asking about on its own, whatever
	// connection it is (FR-4.9).
	src = open(t, source.Guard{})
	ss = pinned(t, src)
	var unbounded *source.UnboundedError
	if _, err := ss.Query(ctx, source.Statement{SQL: `DELETE FROM dbo.writes`}); !errors.As(err, &unbounded) {
		t.Errorf("a delete of every row: %v", err)
	}
}

// otherDatabase is a table in the second fixture database, for the writes
// that prove a reference's database is where they go.
var elsewhere = model.NewRef(model.KindTable, "ikigai_it2", "dbo", "other")

func buildElsewhere(t *testing.T) {
	t.Helper()
	build(t)
	db := admin(t, "ikigai_it2")
	defer db.Close()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS dbo.other`,
		`CREATE TABLE dbo.other (id int NOT NULL PRIMARY KEY, name nvarchar(40) NOT NULL)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%.40q: %v", stmt, err)
		}
	}
}

// A row is written to the database its reference names, not to the one the
// connection was opened on.
func TestLiveAWriteGoesToTheDatabaseItNames(t *testing.T) {
	buildElsewhere(t)
	src, err := Driver{}.Open(context.Background(), config("ikigai_it", source.Guard{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	w := src.(source.Writer)
	ctx := context.Background()
	plan, err := w.Plan(ctx, source.Changeset{Target: elsewhere,
		Identity: model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: elsewhere},
		Changes: []source.RowChange{{Kind: source.ChangeInsert,
			Values: map[string]any{"id": int64(1), "name": "over there"}}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := w.Apply(ctx, plan)
	if err != nil || out.Err != nil || out.Applied != 1 {
		t.Fatalf("writing to another database: %v %+v", err, out)
	}
	rs, err := src.Browse(ctx, elsewhere, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if rows := drain(t, rs); len(rows) != 1 || rows[0][1] != "over there" {
		t.Errorf("the other database holds %v", rows)
	}
}

// A key of several columns is read in the order it was declared in, which is
// the order rows are addressed and paged by.
func TestLiveAKeyOfSeveralColumnsKeepsItsOrder(t *testing.T) {
	src := open(t, source.Guard{})
	ctx := context.Background()
	pair := model.NewRef(model.KindTable, "ikigai_it", "dbo", "pair")
	got, err := src.Describe(ctx, pair)
	if err != nil {
		t.Fatal(err)
	}
	tbl := got.(*model.Table)
	if tbl.PrimaryKey == nil || !slices.Equal(tbl.PrimaryKey.Columns, []string{"b", "a"}) {
		t.Errorf("the primary key is %+v, want the order it was declared in", tbl.PrimaryKey)
	}
	rs, err := src.Browse(ctx, pair, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	if id := model.IdentityOf(rs); !slices.Equal(id.Columns, []string{"b", "a"}) {
		t.Errorf("the rows are known by %+v", id)
	}
	// Ordered by b and then a, so the row with the smaller b comes first.
	if len(rows) != 2 || rows[0][0] != int64(2) {
		t.Errorf("the rows came in the order %v", rows)
	}
	ref, err := src.Describe(ctx, model.NewRef(model.KindTable, "ikigai_it", "dbo", "pairref"))
	if err != nil {
		t.Fatal(err)
	}
	fks := ref.(*model.Table).ForeignKeys
	if len(fks) != 1 || !slices.Equal(fks[0].Columns, []string{"y", "x"}) ||
		!slices.Equal(fks[0].RefColumns, []string{"b", "a"}) {
		t.Errorf("the foreign key is %+v, want the order it was declared in", fks)
	}
}

// A result cut short by a stop costs its connection too, and the tab still
// works afterwards.
func TestLiveStoppingHalfWayThroughAResult(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	before := ss.Handle()
	ctx, cancel := context.WithCancel(context.Background())
	res, err := ss.Query(ctx, source.Statement{SQL: `SELECT id FROM dbo.people ORDER BY id`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := res.Rows.Next(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	res.Rows.Close()

	rows := one(t, ss, `SELECT @@SPID`)
	if len(rows) != 1 {
		t.Fatalf("the session answered %v after a result was cut short", rows)
	}
	if got := fmt.Sprint(rows[0][0]); got == before {
		t.Errorf("the session is still %s, and cutting a result short ends one", got)
	}
}

// A session's name can be read while a statement is running on it, which is
// the only moment anything wants it: what asks is whatever is about to stop
// that statement.
func TestLiveTheSessionsNameIsReadableWhileItIsBusy(t *testing.T) {
	src := open(t, source.Guard{})
	ss := pinned(t, src)
	name := ss.Handle()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ss.Query(ctx, source.Statement{SQL: `WAITFOR DELAY '00:00:10'`})
	}()
	// Long enough for the statement to be under way, and far short of the
	// ten seconds it waits.
	time.Sleep(300 * time.Millisecond)
	asked := make(chan string, 1)
	go func() { asked <- ss.Handle() }()
	select {
	case got := <-asked:
		if got != name {
			t.Errorf("the session is called %q while it is busy and %q while it is not", got, name)
		}
	case <-time.After(2 * time.Second):
		t.Error("the session's name could not be read while a statement was running on it")
	}
	<-done
}
