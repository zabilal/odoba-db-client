package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/conformance"
)

// fixture writes a database file with a small table and a view.
func fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range []string{
		`CREATE TABLE people (id INTEGER PRIMARY KEY, name TEXT NOT NULL, score REAL, born DATE, meta JSON, pic BLOB)`,
		`CREATE TABLE tags (k TEXT PRIMARY KEY, v TEXT) WITHOUT ROWID`,
		`CREATE VIEW adults AS SELECT id, name FROM people WHERE id > 10`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 100)
		 INSERT INTO people (id, name, score, meta) SELECT i, 'person ' || i, i * 1.5, '{"i": ' || i || '}' FROM n`,
		`INSERT INTO tags VALUES ('b', '2'), ('a', '1')`,
		`CREATE TABLE orders (id INTEGER PRIMARY KEY, person_id INTEGER REFERENCES people(id) ON DELETE CASCADE,
		   total NUMERIC, UNIQUE (person_id, total))`,
		`CREATE INDEX orders_total ON orders (total DESC)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return path
}

func open(t *testing.T, path string, guard source.Guard) *sqliteSource {
	t.Helper()
	src, err := Driver{}.Open(context.Background(), source.ConnectionConfig{DriverID: driverID, Database: path, Guard: guard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	return src.(*sqliteSource)
}

var people = model.NewRef(model.KindTable, "main", "people")

func TestConformance(t *testing.T) {
	path := fixture(t)
	conformance.Run(t, conformance.Target{
		Name: "sqlite",
		Open: func(ctx context.Context, t *testing.T) source.Source {
			src, err := Driver{}.Open(ctx, source.ConnectionConfig{DriverID: driverID, Database: path})
			if err != nil {
				t.Fatal(err)
			}
			return src
		},
		Browsable: people,
	})
}

func TestOpenRefusesWhatIsNotADatabase(t *testing.T) {
	dir := t.TempDir()
	notDB := filepath.Join(dir, "notes.txt")
	os.WriteFile(notDB, []byte("hello, this is not a database file at all, not even close"), 0o600)
	for name, c := range map[string]struct {
		path string
		kind source.ConnectKind
	}{
		"missing": {filepath.Join(dir, "nope.db"), source.ConnectNoDatabase},
		"folder":  {dir, source.ConnectConfig},
		"text":    {notDB, source.ConnectConfig},
		"empty":   {"", source.ConnectConfig},
	} {
		_, err := Driver{}.Open(context.Background(), source.ConnectionConfig{Database: c.path})
		var ce *source.ConnectError
		if !errors.As(err, &ce) || ce.Kind != c.kind || ce.Hint == "" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "nope.db")); err == nil {
		t.Error("opening a missing file created it")
	}
}

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

func TestBrowsePagesDeterministicallyAndMapsTypes(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	rs, err := s.Browse(context.Background(), people, source.BrowseOptions{Offset: 10, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	cols := rs.Columns()
	rows := drain(t, rs)
	if len(rows) != 5 || rows[0][0] != int64(11) {
		t.Fatalf("page %v", rows)
	}
	want := map[string]model.TypeClass{"id": model.TypeInteger, "name": model.TypeString, "score": model.TypeFloat,
		"born": model.TypeDate, "meta": model.TypeJSON, "pic": model.TypeBytes}
	for _, c := range cols {
		if c.Type.Class != want[c.Name] {
			t.Errorf("%s: class %v, want %v", c.Name, c.Type.Class, want[c.Name])
		}
	}
	if _, ok := rows[0][4].(model.JSON); !ok {
		t.Errorf("meta is %T; a JSON column's text should be JSON", rows[0][4])
	}

	tags := model.NewRef(model.KindTable, "main", "tags")
	rs, err = s.Browse(context.Background(), tags, source.BrowseOptions{Limit: 10})
	if err != nil {
		t.Fatalf("a WITHOUT ROWID table has no rowid to page by: %v", err)
	}
	if got := drain(t, rs); len(got) != 2 || got[0][0] != "a" {
		t.Errorf("tags %v; paged by its primary key", got)
	}
}

func TestFilters(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	count := func(f ...source.Filter) int64 {
		n, err := s.Count(context.Background(), people, source.BrowseOptions{Filters: f})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(source.Filter{Column: "name", Op: source.OpContains, Values: []any{"PERSON 1"}}); n != 12 {
		t.Errorf("contains, ignoring case: %d, want 12 (1, 10-19, 100)", n)
	}
	if n := count(source.Filter{Column: "born", Op: source.OpEqual, Values: []any{nil}}); n != 100 {
		t.Errorf("= NULL means IS NULL: %d", n)
	}
	if n := count(source.Filter{Column: "id", Op: source.OpNotIn, Values: []any{int64(1), int64(2)}}); n != 98 {
		t.Errorf("not in: %d", n)
	}
	if _, err := s.Count(context.Background(), people, source.BrowseOptions{Filters: []source.Filter{
		{Column: "name", Op: source.OpRegex, Values: []any{"p.*"}}}}); err == nil {
		t.Error("a regex filter SQLite cannot evaluate must be refused, not ignored")
	}
}

func run(t *testing.T, ss source.Session, script string) []source.ScriptResult {
	t.Helper()
	ch, err := ss.QueryMulti(context.Background(), script, false)
	if err != nil {
		t.Fatal(err)
	}
	var out []source.ScriptResult
	for r := range ch {
		out = append(out, r)
	}
	return out
}

func TestScriptsWithTriggersChangesAndReturning(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	ss, err := s.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	res := run(t, ss, `CREATE TABLE log (msg TEXT);
CREATE TRIGGER note AFTER UPDATE ON people BEGIN
  INSERT INTO log VALUES ('changed ' || new.id);
END;
UPDATE people SET score = 0 WHERE id <= 3;
INSERT INTO people (name) VALUES ('new') RETURNING id, name;
SELECT count(*) FROM log`)
	if len(res) != 5 {
		t.Fatalf("%d results: the trigger body must not be split", len(res))
	}
	for _, r := range res {
		if r.Err != nil {
			t.Fatalf("%q: %v", r.Statement, r.Err)
		}
	}
	if res[2].Result.Affected != 3 {
		t.Errorf("UPDATE affected %d, want 3", res[2].Result.Affected)
	}
	if got := drain(t, res[3].Result.Rows); len(got) != 1 || got[0][1] != "new" {
		t.Errorf("RETURNING gave %v", got)
	}
	if got := drain(t, res[4].Result.Rows); got[0][0] != int64(3) {
		t.Errorf("the trigger ran %v times", got[0][0])
	}
}

func TestSessionsKeepTheirState(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	ss, _ := s.Session(context.Background())
	defer ss.Close()
	run(t, ss, "CREATE TEMP TABLE scratch (x); INSERT INTO scratch VALUES (1)")
	res := run(t, ss, "SELECT count(*) FROM scratch")
	if res[0].Err != nil {
		t.Fatalf("a temp table did not survive to the next run: %v", res[0].Err)
	}
}

func TestNamedParameters(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	res, err := s.Query(context.Background(), source.Statement{SQL: "SELECT name FROM people WHERE id = :id",
		Named: map[string]any{"id": int64(7)}})
	if err != nil {
		t.Fatal(err)
	}
	if got := drain(t, res.Rows); len(got) != 1 || got[0][0] != "person 7" {
		t.Errorf("%v", got)
	}
}

func TestReadOnlyIsRefusedTwice(t *testing.T) {
	path := fixture(t)
	s := open(t, path, source.Guard{ReadOnly: true})
	if _, err := s.Query(context.Background(), source.Statement{SQL: "DELETE FROM people"}); !errors.Is(err, source.ErrReadOnly) {
		t.Errorf("guard: %v", err)
	}
	// The engine refuses too, even when classification is fooled.
	var n int
	s.db.QueryRow(`PRAGMA query_only`).Scan(&n)
	if n != 1 {
		t.Error("a read-only connection should have query_only set")
	}
	if _, err := s.db.Exec(`DELETE FROM people`); err == nil {
		t.Error("the engine accepted a write on a read-only connection")
	}
}

const forever = `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT count(*) FROM c`

func TestCancellingInterruptsTheEngine(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	res, err := s.Query(ctx, source.Statement{SQL: forever})
	if err == nil {
		_, err = res.Rows.Next(context.Background())
		res.Rows.Close()
	}
	if err == nil || time.Since(start) > 3*time.Second {
		t.Errorf("err %v after %v; cancelling must stop an endless query", err, time.Since(start))
	}
}

func TestKillQueryStopsASession(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	ss, _ := s.Session(context.Background())
	defer ss.Close()
	done := make(chan error, 1)
	go func() {
		res, err := ss.Query(context.Background(), source.Statement{SQL: forever})
		if err == nil {
			_, err = res.Rows.Next(context.Background())
		}
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	if err := s.KillQuery(context.Background(), ss.Handle()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Error("the killed query returned a row")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("KillQuery did not stop the query")
	}
}

func TestClassify(t *testing.T) {
	for stmt, want := range map[string]source.Access{
		"SELECT * FROM t":                      source.AccessRead,
		"WITH x AS (SELECT 1) SELECT * FROM x": source.AccessRead,
		"WITH x AS (SELECT 1) DELETE FROM t":   source.AccessWrite,
		"EXPLAIN QUERY PLAN SELECT 1":          source.AccessRead,
		"PRAGMA table_info(t)":                 source.AccessRead,
		"PRAGMA user_version":                  source.AccessRead,
		"PRAGMA user_version = 3":              source.AccessAdmin,
		"PRAGMA writable_schema":               source.AccessAdmin,
		"INSERT INTO t VALUES (1)":             source.AccessWrite,
		"CREATE TABLE t (x)":                   source.AccessDDL,
		"ATTACH 'other.db' AS o":               source.AccessAdmin,
		"VACUUM":                               source.AccessAdmin,
		"SELECT load_extension('x')":           source.AccessAdmin,
		"SELECT 1; DELETE FROM t":              source.AccessWrite,
		"SELECT 'DELETE FROM t'":               source.AccessRead,
		"-- DROP TABLE t\nSELECT 1":            source.AccessRead,
		"BEGIN":                                source.AccessRead,
		"frobnicate everything":                source.AccessWrite,
	} {
		if got := classify(stmt); got != want {
			t.Errorf("%q: %v, want %v", stmt, got, want)
		}
	}
}

func TestQuoting(t *testing.T) {
	var d dialect
	if got := d.QuoteIdentifier(`we"ird`); got != `"we""ird"` {
		t.Errorf("%s", got)
	}
	if got := d.QualifyRef(people); got != `"main"."people"` {
		t.Errorf("%s", got)
	}
	if !strings.Contains(func() string {
		st, _ := d.BuildBrowse(people, source.BrowseOptions{Sorts: []source.Sort{{Column: "name", Descending: true}}})
		return st.SQL
	}(), `ORDER BY "name" DESC NULLS LAST LIMIT ?`) {
		t.Error("sort not rendered as expected")
	}
}

func TestDescribeReadsKeysIndexesAndForeignKeys(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	v, err := s.Describe(context.Background(), model.NewRef(model.KindTable, "main", "orders"))
	if err != nil {
		t.Fatal(err)
	}
	tb := v.(*model.Table)
	if len(tb.Columns) != 3 || tb.PrimaryKey == nil || tb.PrimaryKey.Columns[0] != "id" {
		t.Fatalf("columns %d, pk %+v", len(tb.Columns), tb.PrimaryKey)
	}
	if len(tb.ForeignKeys) != 1 {
		t.Fatalf("foreign keys %+v", tb.ForeignKeys)
	}
	fk := tb.ForeignKeys[0]
	if fk.RefTable != "people" || fk.Columns[0] != "person_id" || fk.RefColumns[0] != "id" || fk.OnDelete != model.ReferentialAction("CASCADE") {
		t.Errorf("foreign key %+v", fk)
	}
	if len(tb.Indexes) != 1 || tb.Indexes[0].Name != "orders_total" || !tb.Indexes[0].Columns[0].Descending {
		t.Errorf("indexes %+v", tb.Indexes)
	}
	if len(tb.Uniques) != 1 || len(tb.Uniques[0].Columns) != 2 {
		t.Errorf("uniques %+v", tb.Uniques)
	}
}

func TestDescribeAView(t *testing.T) {
	s := open(t, fixture(t), source.Guard{})
	v, err := s.Describe(context.Background(), model.NewRef(model.KindView, "main", "adults"))
	if err != nil {
		t.Fatal(err)
	}
	vw, ok := v.(*model.View)
	if !ok || len(vw.Columns) != 2 || !strings.Contains(vw.Definition, "WHERE id > 10") {
		t.Errorf("%#v", v)
	}
}
