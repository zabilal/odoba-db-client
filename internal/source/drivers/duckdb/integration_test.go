//go:build duckdb

package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// Tests against a real DuckDB (REQ-DRV-1, T3.34). Nothing has to be
// running: a database here is a file, and each test makes its own.

var fixture = []string{
	`CREATE TABLE people (
	   id INTEGER PRIMARY KEY,
	   name VARCHAR NOT NULL,
	   score DECIMAL(30,10),
	   born DATE,
	   seen TIMESTAMPTZ,
	   meta JSON,
	   tag UUID,
	   ok BOOLEAN,
	   blob BLOB,
	   dur INTERVAL,
	   tags VARCHAR[],
	   big HUGEINT,
	   thing STRUCT(a INTEGER, b VARCHAR))`,
	`COMMENT ON TABLE people IS 'Everyone we know'`,
	`COMMENT ON COLUMN people.name IS 'What they are called'`,
	`INSERT INTO people (id, name, score, born, meta, ok)
	 SELECT i, 'person ' || i::VARCHAR, i * 1.5,
	        CASE WHEN i % 3 = 0 THEN DATE '2000-01-01' + (i % 2)::INTEGER END,
	        ('{"i": ' || i::VARCHAR || '}')::JSON, i % 2 = 0
	 FROM range(1, 101) AS t(i)`,
	`INSERT INTO people (id, name, score, tag, blob, dur, tags, big, thing, seen, meta)
	 VALUES (1000, 'exact', 12345678901234567890.1234567890,
	         '6f9619ff-8b86-d011-b42d-00c04fc964ff', '\x01\x02'::BLOB,
	         INTERVAL 1 DAY, ['a','b'],
	         170141183460469231731687303715884105727,
	         {'a': 1, 'b': 'x'}, TIMESTAMPTZ '2024-03-01 12:00:00+00',
	         '{"z":1.0,"a":2}')`,
	`CREATE TABLE orders (
	   id INTEGER PRIMARY KEY,
	   person_id INTEGER REFERENCES people (id),
	   total DECIMAL(10,2),
	   CONSTRAINT ck_total CHECK (total >= 0))`,
	`CREATE INDEX orders_total ON orders (total)`,
	// A key of two columns, declared in the other order from the table's.
	`CREATE TABLE pair (a INTEGER, b INTEGER, c INTEGER, PRIMARY KEY (b, a))`,
	`INSERT INTO pair VALUES (1, 2, 3), (2, 1, 4)`,
	// Rows written out of their key's order, so that a browse which did
	// not order by the key would read them in the order they were written.
	`CREATE TABLE jumbled (id INTEGER PRIMARY KEY, v VARCHAR)`,
	`INSERT INTO jumbled VALUES (3, 'three'), (1, 'one'), (2, 'two')`,
	`CREATE SEQUENCE writes_id_seq`,
	`CREATE TABLE writes (
	   id INTEGER PRIMARY KEY DEFAULT nextval('writes_id_seq'),
	   name VARCHAR NOT NULL DEFAULT 'none',
	   n INTEGER)`,
	// No key of its own: rowid says where a row is, and that is what
	// addresses these.
	`CREATE TABLE nokey (a INTEGER, b VARCHAR)`,
	`INSERT INTO nokey VALUES (2, 'y'), (1, 'x'), (1, 'x'), (3, 'z')`,
	`CREATE SCHEMA app`,
	`CREATE SCHEMA bare`,
	`CREATE TABLE app.elsewhere (id INTEGER PRIMARY KEY)`,
	`CREATE VIEW adults AS SELECT id, name FROM people WHERE id > 10`,
}

// build makes a database file and returns its path.
func build(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ikigai.duckdb")
	db, err := sql.Open(driverID, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range fixture {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("fixture %.60q: %v", stmt, err)
		}
	}
	return path
}

func config(path string, guard source.Guard) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Database: path, Guard: guard}
}

func open(t *testing.T, guard source.Guard) (*duckSource, string) {
	t.Helper()
	path := build(t)
	return opened(t, path, guard), path
}

func opened(t *testing.T, path string, guard source.Guard) *duckSource {
	t.Helper()
	src, err := Driver{}.Open(context.Background(), config(path, guard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	return src.(*duckSource)
}

// copyOf makes a copy of a database file, for the one thing this engine
// will not do: hold the same file open two ways at once.
func copyOf(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	at := filepath.Join(t.TempDir(), "copy.duckdb")
	if err := os.WriteFile(at, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return at
}

func ref(kind model.ObjectKind, name string) model.ObjectRef {
	return model.NewRef(kind, "ikigai", "main", name)
}

func people() model.ObjectRef { return ref(model.KindTable, "people") }

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
	path := build(t)
	conformance.Run(t, conformance.Target{
		Name: "duckdb",
		Open: func(ctx context.Context, t *testing.T) source.Source {
			src, err := Driver{}.Open(ctx, config(path, source.Guard{}))
			if err != nil {
				t.Fatal(err)
			}
			return src
		},
		Browsable: people(),
		Writable:  ref(model.KindTable, "writes"),
		OpenGuarded: func(ctx context.Context, t *testing.T, g source.Guard) source.Source {
			// A read-only connection reads a copy. DuckDB holds a file one
			// way at a time in one process, and the checks that guard
			// against writing do not need to see what the others wrote —
			// they are about a write being refused (ADR-0146).
			at := path
			if g.ReadOnly {
				at = copyOf(t, path)
			}
			src, err := Driver{}.Open(ctx, config(at, g))
			if err != nil {
				t.Fatal(err)
			}
			return src
		},
	})
}

// What the driver hands over for each type, which is what the grid shows
// (FR-3.8).
func TestLiveWhatTheDriverHandsOver(t *testing.T) {
	src, _ := open(t, source.Guard{})
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
		t.Logf("%-6s native=%-22s class=%-10v value=%#v", c.Name, c.Type.Native, c.Type.Class, rows[0][i])
	}

	// An exact number keeps every digit and its column's places with them.
	if got := at["score"]; got != model.Decimal("12345678901234567890.1234567890") {
		t.Errorf("score reads %#v", got)
	}
	// A document is the document: its keys in the order they were written
	// and 1.0 still 1.0, which the library's own decoding loses.
	if got, ok := at["meta"].(model.JSON); !ok || string(got) != `{"z":1.0,"a":2}` {
		t.Errorf("meta reads %#v", at["meta"])
	}
	if got := at["tag"]; got != "6f9619ff-8b86-d011-b42d-00c04fc964ff" {
		t.Errorf("tag reads %#v", got)
	}
	// A HUGEINT holds more than an int64, so every digit of it is kept.
	if got := at["big"]; got != model.Decimal("170141183460469231731687303715884105727") {
		t.Errorf("big reads %#v", got)
	}
	if got, ok := at["blob"].([]byte); !ok || !slices.Equal(got, []byte{1, 2}) {
		t.Errorf("blob reads %#v", at["blob"])
	}
	if got, ok := at["tags"].([]any); !ok || !slices.Equal(got, []any{"a", "b"}) {
		t.Errorf("tags reads %#v", at["tags"])
	}
	if _, ok := at["thing"].(map[string]any); !ok {
		t.Errorf("thing reads %#v", at["thing"])
	}
	if got := at["dur"]; got != "0 mons 1 days 00:00:00" {
		t.Errorf("dur reads %#v", got)
	}
	if native["score"] != "DECIMAL(30,10)" {
		t.Errorf("score's type reads %q", native["score"])
	}
}

// Opening never makes a database. DuckDB would create a file it cannot
// find, so a mistyped path would open an empty database that looks
// exactly like data gone missing.
func TestLiveOpeningNeverMakesADatabase(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "not-there.duckdb")
	_, err := Driver{}.Open(context.Background(), config(missing, source.Guard{}))
	if err == nil {
		t.Fatal("a path that is not there opened")
	}
	var ce *source.ConnectError
	if !errors.As(err, &ce) || ce.Kind != source.ConnectNoDatabase {
		t.Errorf("the refusal reads %v", err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("the file was made anyway")
	}
	// And nothing at all is not a path to try.
	if _, err := (Driver{}).Open(context.Background(), config("", source.Guard{})); err == nil {
		t.Error("an empty path opened")
	} else {
		var ce *source.ConnectError
		if !errors.As(err, &ce) || ce.Kind != source.ConnectConfig {
			t.Errorf("an empty path reads %v", err)
		}
	}
	// And a folder is not a database file, and is refused as one rather
	// than as whatever the engine makes of being handed a folder.
	_, err = (Driver{}).Open(context.Background(), config(dir, source.Guard{}))
	if err == nil {
		t.Fatal("a folder opened as a database")
	}
	if !errors.As(err, &ce) || ce.Kind != source.ConnectConfig {
		t.Errorf("a folder reads %v", err)
	}
	if !strings.Contains(ce.Hint, "folder") {
		t.Errorf("the refusal says %q", ce.Hint)
	}
}

// The explorer walks the file: the databases this connection holds, their
// schemas, and what is in them (FR-2.1, FR-2.2).
func TestLiveTheExplorerWalksTheFile(t *testing.T) {
	src, _ := open(t, source.Guard{})
	ctx := context.Background()

	roots, err := src.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ours := find(roots, "ikigai")
	if ours == nil {
		t.Fatalf("the databases are %q", labels(roots))
	}
	if ours.Attrs["current"] != "true" {
		t.Error("the database this connection was opened on does not say so")
	}
	// The engine's own are not somebody's data: temp holds what a session
	// made for itself and system holds the catalogue being read to draw
	// this tree.
	for _, hidden := range []string{"system", "temp"} {
		if find(roots, hidden) != nil {
			t.Errorf("%s is listed: %q", hidden, labels(roots))
		}
	}

	schemas, err := src.Children(ctx, ours.Ref)
	if err != nil {
		t.Fatal(err)
	}
	// main first, because it is where most people's tables are, and the
	// rest by name after it. Both are here even though the catalogue
	// calls main internal.
	if got := labels(schemas); !slices.Equal(got, []string{"main", "app", "bare"}) {
		t.Fatalf("the schemas read %q", got)
	}

	folders, err := src.Children(ctx, schemas[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	if find(folders, "Tables") == nil {
		t.Fatalf("the folders are %q", labels(folders))
	}

	tables, err := src.Children(ctx, model.ClassRef(schemas[0].Ref, model.KindTable))
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
	if find(tables, "adults") != nil {
		t.Errorf("a view is listed among the tables: %q", labels(tables))
	}
	views, err := src.Children(ctx, model.ClassRef(schemas[0].Ref, model.KindView))
	if err != nil {
		t.Fatal(err)
	}
	if find(views, "adults") == nil || find(views, "people") != nil {
		t.Errorf("the views are %q", labels(views))
	}

	// A schema with nothing in it still shows its tables, empty: a schema
	// node says it has children, and one that then lists none draws an
	// expander that turns and never opens (FR-2.2).
	bare := find(schemas, "bare")
	if bare == nil {
		t.Fatalf("the schemas are %q", labels(schemas))
	}
	inBare, err := src.Children(ctx, bare.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(inBare) != 1 || find(inBare, "Tables") == nil {
		t.Errorf("an empty schema holds %q", labels(inBare))
	}

	cols, err := src.Children(ctx, pe.Ref)
	if err != nil {
		t.Fatal(err)
	}
	if got := find(cols, "score"); got == nil || got.Attrs["type"] != "DECIMAL(30,10)" {
		t.Errorf("score reads %+v", got)
	}
	if got := find(cols, "id"); got == nil || got.Attrs["key"] != "primary" {
		t.Errorf("id is the key and reads %+v", got)
	}
}

// A file somebody attached is read through the same connection, by
// naming it (ADR-0146).
func TestLiveAnAttachedFileIsReadTheSameWay(t *testing.T) {
	src, _ := open(t, source.Guard{})
	ctx := context.Background()

	other := filepath.Join(t.TempDir(), "other.duckdb")
	plain, err := sql.Open(driverID, other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Exec(`CREATE TABLE only_here (id INTEGER PRIMARY KEY, note VARCHAR);
		INSERT INTO only_here VALUES (1, 'over here')`); err != nil {
		t.Fatal(err)
	}
	plain.Close()

	if _, err := src.Query(ctx, source.Statement{
		SQL: `ATTACH '` + other + `' AS elsewhere (READ_ONLY)`, Confirmed: true}); err != nil {
		t.Fatalf("attaching: %v", err)
	}

	roots, err := src.Root(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if find(roots, "elsewhere") == nil {
		t.Fatalf("the databases are %q", labels(roots))
	}
	there := model.NewRef(model.KindTable, "elsewhere", "main", "only_here")
	rs, err := src.Browse(ctx, there, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatalf("browsing the attached file: %v", err)
	}
	rows := drain(t, rs)
	if len(rows) != 1 || rows[0][1] != "over here" {
		t.Fatalf("the attached file's rows read %v", rows)
	}
	// Its structure too, which is read through other statements again.
	got, err := src.Describe(ctx, there)
	if err != nil {
		t.Fatalf("describing a table in an attached file: %v", err)
	}
	if k := got.(*model.Table).PrimaryKey; k == nil || !slices.Equal(k.Columns, []string{"id"}) {
		t.Errorf("its key reads %+v", k)
	}
}

// A table with no key of its own is addressed by where its rows are.
// rowid is hidden from SELECT *, so it is asked for by name, and it is no
// part of the table's structure (ADR-0146).
func TestLiveATableWithNoKeyIsAddressedByItsRows(t *testing.T) {
	src, _ := open(t, source.Guard{})
	ctx := context.Background()
	nokey := ref(model.KindTable, "nokey")

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
	if id.Kind != model.IdentityRowID {
		t.Fatalf("rows are addressed as %v", id.Kind)
	}
	if !slices.Equal(id.Columns, []string{"rowid"}) {
		t.Fatalf("the address is %q", id.Columns)
	}
	if len(rows) != 4 {
		t.Fatalf("got %d rows", len(rows))
	}
	// The address is read first, before the columns somebody declared.
	var names []string
	for _, c := range rs.Columns() {
		names = append(names, c.Name)
	}
	if !slices.Equal(names, []string{"rowid", "a", "b"}) {
		t.Fatalf("the browse reads %q", names)
	}
	// Two of these rows are the same in every declared column, and the
	// address is what tells them apart.
	if rows[1][0] == rows[2][0] {
		t.Error("two rows have the same address")
	}
}

// A key of two columns is read in the order it was declared, which here
// is not the order its columns are in.
func TestLiveAKeyIsReadInTheOrderItWasDeclared(t *testing.T) {
	src, _ := open(t, source.Guard{})
	ctx := context.Background()
	pair := ref(model.KindTable, "pair")

	info, err := src.tableInfo(ctx, pair)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(info.key, []string{"b", "a"}) {
		t.Errorf("the key reads %q, not the order it was declared in", info.key)
	}
	if info.rowID {
		t.Error("a key somebody declared is called the row's address")
	}
	got, err := src.Describe(ctx, pair)
	if err != nil {
		t.Fatal(err)
	}
	if k := got.(*model.Table).PrimaryKey; k == nil || !slices.Equal(k.Columns, []string{"b", "a"}) {
		t.Errorf("the structure's key reads %+v", k)
	}
}

// Each table's key is its own, and they are remembered between pages.
func TestLiveEachTableKeepsItsOwnKey(t *testing.T) {
	src, _ := open(t, source.Guard{})
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
		info, err := src.tableInfo(ctx, ref(model.KindTable, c.table))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(info.key, c.key) {
			t.Errorf("%s is addressed by %q, want %q", c.table, info.key, c.key)
		}
	}
}

// What a table is made of (FR-2.4).
func TestLiveTheStructureOfATable(t *testing.T) {
	src, _ := open(t, source.Guard{})
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
	if fk := tbl.ForeignKeys[0]; !slices.Equal(fk.Columns, []string{"person_id"}) ||
		fk.RefTable != "people" || !slices.Equal(fk.RefColumns, []string{"id"}) {
		t.Errorf("the foreign key reads %+v", tbl.ForeignKeys[0])
	}
	// A NOT NULL is a column's own business and is not a rule over the
	// row, so it is not among the checks — and this table has one.
	if len(tbl.Checks) != 1 {
		t.Fatalf("there are %d checks: %+v", len(tbl.Checks), tbl.Checks)
	}
	if !strings.Contains(tbl.Checks[0].Expression, "total") {
		t.Errorf("the check reads %q", tbl.Checks[0].Expression)
	}
	if len(tbl.Indexes) != 1 || tbl.Indexes[0].Name != "orders_total" {
		t.Fatalf("the indexes read %+v", tbl.Indexes)
	}
	// What it is over, read out of the one piece of text the catalogue
	// writes it as.
	if got := tbl.Indexes[0].Columns; len(got) != 1 || got[0].Name != "total" {
		t.Errorf("the index is over %+v", got)
	}
	if !slices.Equal(names(tbl.Columns), []string{"id", "person_id", "total"}) {
		t.Errorf("the columns read %q", names(tbl.Columns))
	}
}

// A table's comment and a column's are what somebody wrote about them.
func TestLiveTheCommentsAreRead(t *testing.T) {
	src, _ := open(t, source.Guard{})
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
	src, _ := open(t, source.Guard{})
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

	// And its rows are read. A view has no rowid, so a driver that gave
	// it an address would not be able to read one at all.
	rs, err := src.Browse(context.Background(), ref(model.KindView, "adults"),
		source.BrowseOptions{Limit: 5})
	if err != nil {
		t.Fatalf("browsing a view: %v", err)
	}
	if rows := drain(t, rs); len(rows) != 5 {
		t.Errorf("a view read %d rows", len(rows))
	}
	if k := model.IdentityOf(rs).Kind; k != model.IdentityNone {
		t.Errorf("a view's rows are addressed as %v", k)
	}
}

// Paging is by the key, so a page is a page (NFR-P11).
func TestLivePagingIsOrderedByTheKey(t *testing.T) {
	src, _ := open(t, source.Guard{})
	ctx := context.Background()
	jumbled := ref(model.KindTable, "jumbled")

	var seen []any
	for offset := int64(0); offset < 3; offset++ {
		rs, err := src.Browse(ctx, jumbled, source.BrowseOptions{Limit: 1, Offset: offset})
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

// A filter means what it means on every engine (FR-3.6).
func TestLiveFiltersSelectWhatTheyMean(t *testing.T) {
	src, _ := open(t, source.Guard{})
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
	withNull := count(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "born", Op: source.OpIn, Values: []any{nil}}}})
	nulls := count(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "born", Op: source.OpIsNull}}})
	if withNull != nulls || nulls == 0 {
		t.Errorf("IN (NULL) found %d rows where IS NULL found %d", withNull, nulls)
	}
	if n := count(t, source.BrowseOptions{Filters: []source.Filter{
		{Column: "score", Op: source.OpEqual,
			Values: []any{model.Decimal("12345678901234567890.1234567890")}}}}); n != 1 {
		t.Errorf("filtering by an exact number found %d rows", n)
	}
}

// The picklist offers the values that are there, most frequent first, and
// each of them filters the column it came from (FR-3.7).
func TestLiveTheDistinctValuesAreOffered(t *testing.T) {
	src, _ := open(t, source.Guard{})
	ctx := context.Background()
	nokey := ref(model.KindTable, "nokey")

	got, err := src.Distinct(ctx, nokey, "a", source.BrowseOptions{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("the distinct values are %+v", got)
	}
	if got[0].Value != int64(1) || got[0].Count != 2 {
		t.Errorf("the most frequent value is %+v", got[0])
	}
	// A value the picklist offered has to select the rows it was counted
	// from.
	rs, err := src.Browse(ctx, nokey, source.BrowseOptions{Limit: 10,
		Filters: []source.Filter{{Column: "a", Op: source.OpIn, Values: []any{got[0].Value}}}})
	if err != nil {
		t.Fatal(err)
	}
	if rows := drain(t, rs); int64(len(rows)) != got[0].Count {
		t.Errorf("the picklist counted %d rows and the filter found %d", got[0].Count, len(rows))
	}
}

// A read-only connection refuses a write, and the file is held read-only
// as well (NFR-S4).
func TestLiveAReadOnlyConnectionRefusesAWrite(t *testing.T) {
	path := build(t)
	src := opened(t, path, source.Guard{ReadOnly: true})
	ctx := context.Background()

	// The first defence: refused before the engine hears of it.
	_, err := src.Query(ctx, source.Statement{SQL: `INSERT INTO writes (n) VALUES (1)`})
	if err == nil {
		t.Fatal("a read-only connection wrote a row")
	}
	if !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("the refusal came from somewhere else: %v", err)
	}

	// The second: the file itself, which no statement can undo. A routine
	// that writes is something the classifier reads as a plain SELECT.
	if _, err := src.Query(ctx, source.Statement{
		SQL: `CREATE OR REPLACE MACRO grow() AS (SELECT 1)`, Confirmed: true}); err == nil {
		t.Error("a read-only file accepted a definition")
	}
	// And the setting that made it read-only cannot be changed back.
	if _, err := src.Query(ctx, source.Statement{
		SQL: `SET access_mode = 'READ_WRITE'`, Confirmed: true}); err == nil {
		t.Error("read-only mode was turned off")
	}
}

// A statement is stopped by the session running it. There is no server to
// send a cancel to: a DuckDB database is this process (ADR-0146).
func TestLiveAStatementIsStoppedByItsSession(t *testing.T) {
	src, _ := open(t, source.Guard{})
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()

	ss, err := src.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	handle := ss.Handle()
	if handle == "" {
		t.Fatal("a session has no name to be stopped by")
	}
	// Nothing is running yet, so there is nothing to stop.
	if err := src.KillQuery(ctx, handle); err == nil {
		t.Error("stopping an idle session reported that it stopped something")
	}
	if err := src.KillQuery(ctx, "no-such-session"); err == nil {
		t.Error("stopping a session that does not exist reported success")
	}

	done := make(chan error, 1)
	go func() {
		// A sum over a range wide enough to outlast the test, and no
		// wider: what is being proved is that a statement stops when it
		// is told to.
		_, err := ss.Query(ctx, source.Statement{
			SQL: `SELECT sum(i) FROM range(1, 20000000000) AS t(i)`})
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := src.KillQuery(ctx, handle); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("nothing was running to stop")
		}
		time.Sleep(50 * time.Millisecond)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the statement was stopped and finished anyway")
		}
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
	path := build(t)
	src := opened(t, path, source.Guard{})
	ctx := context.Background()
	writes := ref(model.KindTable, "writes")
	id := model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: writes}

	plan, err := src.Plan(ctx, source.Changeset{Target: writes, Identity: id,
		Changes: []source.RowChange{
			{Kind: source.ChangeInsert, Values: map[string]any{"n": int64(10)}},
			{Kind: source.ChangeInsert, Values: map[string]any{"n": int64(20)}},
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

	rs, err := src.Browse(ctx, writes, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	rows := drain(t, rs)
	if len(rows) != 2 {
		t.Fatalf("the table holds %d rows", len(rows))
	}
	// A column nobody named takes its default.
	if rows[0][1] != "none" {
		t.Errorf("the default was not taken: %#v", rows[0][1])
	}

	// And a row of nothing but defaults, which this engine spells the
	// way PostgreSQL does.
	plan, err = src.Plan(ctx, source.Changeset{Target: writes, Identity: id,
		Changes: []source.RowChange{{Kind: source.ChangeInsert}}})
	if err != nil {
		t.Fatal(err)
	}
	if out, err := src.Apply(ctx, plan); err != nil || out.Err != nil {
		t.Errorf("a row of nothing but defaults: %v %v", err, out.Err)
	}
}

// A read-only connection writes nothing through the grid either, and the
// refusal is the guard's own (NFR-S4).
func TestLiveAReadOnlyConnectionAppliesNothing(t *testing.T) {
	path := build(t)
	src := opened(t, path, source.Guard{ReadOnly: true})
	ctx := context.Background()
	writes := ref(model.KindTable, "writes")
	id := model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: []string{"id"}, Target: writes}

	plan, err := src.Plan(ctx, source.Changeset{Target: writes, Identity: id,
		Changes: []source.RowChange{{Kind: source.ChangeInsert, Values: map[string]any{"n": int64(1)}}}})
	if err != nil {
		t.Fatalf("a change could not even be rendered: %v", err)
	}
	if _, err := src.Apply(ctx, plan); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("the refusal reads %v", err)
	}
}

// A change says how many rows it changed. DuckDB answers one with a
// single column called Count, which is a number to report rather than a
// row to show (FR-5.2).
func TestLiveAChangeSaysHowManyRows(t *testing.T) {
	path := build(t)
	src := opened(t, path, source.Guard{})
	ctx := context.Background()

	res, err := src.Query(ctx, source.Statement{
		SQL: `INSERT INTO writes (n) VALUES (1), (2), (3)`, Confirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows != nil {
		t.Error("a change handed back rows")
	}
	if res.Affected != 3 {
		t.Errorf("the change says it reached %d rows", res.Affected)
	}
	// And a read still reads.
	res, err = src.Query(ctx, source.Statement{SQL: `SELECT n FROM writes ORDER BY n`})
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows == nil {
		t.Fatal("a read handed back no rows")
	}
	if got := drain(t, res.Rows); len(got) != 3 {
		t.Errorf("the read found %d rows", len(got))
	}
}

// A script is weighed whole before any of it runs, so a write at the end
// stops the read at the start (NFR-S4).
func TestLiveAScriptIsWeighedWhole(t *testing.T) {
	path := build(t)
	src := opened(t, path, source.Guard{ReadOnly: true})
	ctx := context.Background()

	out, err := src.QueryMulti(ctx, "SELECT 1;\nINSERT INTO writes (n) VALUES (1)",
		source.ScriptOptions{})
	if err == nil {
		for r := range out {
			t.Logf("statement %d: err=%v", r.Index, r.Err)
		}
		t.Fatal("a script holding a write was accepted on a read-only connection")
	}
	if !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("the refusal came from somewhere else: %v", err)
	}
}

// A row in a table with no key of its own is written by where it is.
func TestLiveARowWithNoKeyOfItsOwnIsWritten(t *testing.T) {
	path := build(t)
	src := opened(t, path, source.Guard{})
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
	address := rows[0][0] // the row's address, read first

	plan, err := src.Plan(ctx, source.Changeset{Target: nokey,
		Identity: model.RowIdentity{Kind: model.IdentityRowID,
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
		t.Fatalf("writing by a row's address: %v", out.Err)
	}
	if out.Affected != 1 {
		t.Errorf("the change reached %d rows", out.Affected)
	}
}

// A badge is the estimate the engine keeps. Unlike the engines that say
// zero for a table nobody measured, this one says nothing at all — so a
// table with no rows is badged 0 and means it (FR-2.5).
func TestLiveABadgeIsAnEstimate(t *testing.T) {
	src, _ := open(t, source.Guard{})
	ctx := context.Background()

	b, ok, err := src.Badge(ctx, people())
	if err != nil {
		t.Fatal(err)
	}
	if !ok || b.Exact {
		t.Fatalf("people's badge is %+v (present %v)", b, ok)
	}
	if b.Text != "101" {
		t.Errorf("people's badge reads %q", b.Text)
	}
	empty, ok, err := src.Badge(ctx, ref(model.KindTable, "writes"))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || empty.Text != "0" {
		t.Errorf("an empty table's badge is %+v (present %v)", empty, ok)
	}
	// A kind with no rows to count has no badge at all.
	if _, ok, err := src.Badge(ctx, ref(model.KindSequence, "writes_id_seq")); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("a sequence was given a row count")
	}
}
