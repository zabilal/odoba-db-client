//go:build conformance

package mysql

// Integration tests against real MySQL and MariaDB servers (REQ-DRV-1,
// T1.37). They expect the ikigai-mysql and ikigai-mariadb containers, on
// ports 53306 and 53307, and skip if either is not running;
// IKIGAI_REQUIRE_MYSQL=1 makes that a failure, as in CI.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
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

type server struct {
	name, portEnv string
	port          int
}

var servers = []server{{"mysql", "IKIGAI_MYSQL_PORT", 53306}, {"mariadb", "IKIGAI_MARIADB_PORT", 53307}}

func (s server) addr() int {
	if v := os.Getenv(s.portEnv); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			return p
		}
	}
	return s.port
}

func (s server) config(db string, guard source.Guard) source.ConnectionConfig {
	return source.ConnectionConfig{DriverID: driverID, Host: "127.0.0.1", Port: s.addr(), User: "root", Database: db,
		Secret: func(string) (string, error) { return env("IKIGAI_MYSQL_PASSWORD", "ikigai"), nil },
		TLS:    source.TLSConfig{Mode: "disable"}, Guard: guard}
}

const fixture = `DROP DATABASE IF EXISTS ikigai_it;
CREATE DATABASE ikigai_it;
CREATE TABLE ikigai_it.people (id INT PRIMARY KEY, name VARCHAR(50) NOT NULL, score DECIMAL(30,10),
  born DATE, seen TIMESTAMP NULL, meta JSON, pic BLOB);
INSERT INTO ikigai_it.people (id, name, score, born, meta)
  WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 100)
  SELECT i, CONCAT('person ', i), i * 1.5,
    IF(i % 3 = 0, DATE('2000-01-01') + INTERVAL (i % 2) DAY, NULL), JSON_OBJECT('i', i) FROM n;
INSERT INTO ikigai_it.people (id, name, score) VALUES (1000, 'exact', 12345678901234567890.1234567890);
CREATE TABLE ikigai_it.nokey (a INT, b VARCHAR(10));
INSERT INTO ikigai_it.nokey VALUES (2, 'y'), (1, 'x'), (1, 'x'), (3, 'z');
CREATE TABLE ikigai_it.orders (id INT PRIMARY KEY, person_id INT, total DECIMAL(10,2),
  KEY orders_total (total DESC),
  CONSTRAINT fk_person FOREIGN KEY (person_id) REFERENCES ikigai_it.people (id) ON DELETE CASCADE);
CREATE VIEW ikigai_it.adults AS SELECT id, name FROM ikigai_it.people WHERE id > 10;`

// each runs fn against every server that is up, with a fresh fixture.
func each(t *testing.T, fn func(t *testing.T, srv server)) {
	for _, srv := range servers {
		t.Run(srv.name, func(t *testing.T) {
			dsn := fmt.Sprintf("root:%s@tcp(127.0.0.1:%d)/?multiStatements=true",
				env("IKIGAI_MYSQL_PASSWORD", "ikigai"), srv.addr())
			db, err := sql.Open("mysql", dsn)
			if err == nil {
				err = db.Ping()
			}
			if err != nil {
				if os.Getenv("IKIGAI_REQUIRE_MYSQL") != "" {
					t.Fatalf("%s required but unavailable: %v", srv.name, err)
				}
				t.Skipf("no %s on port %d (docker start ikigai-%s): %v", srv.name, srv.addr(), srv.name, err)
			}
			defer db.Close()
			if _, err := db.Exec(fixture); err != nil {
				t.Fatalf("fixture: %v", err)
			}
			fn(t, srv)
		})
	}
}

func open(t *testing.T, srv server, guard source.Guard) *mysqlSource {
	t.Helper()
	src, err := Driver{}.Open(context.Background(), srv.config("ikigai_it", guard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	return src.(*mysqlSource)
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
	each(t, func(t *testing.T, srv server) {
		conformance.Run(t, conformance.Target{
			Name: srv.name,
			Open: func(ctx context.Context, t *testing.T) source.Source {
				src, err := Driver{}.Open(ctx, srv.config("ikigai_it", source.Guard{}))
				if err != nil {
					t.Fatal(err)
				}
				return src
			},
			Browsable: people,
		})
	})
}

func TestTheServerSaysWhichItIs(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		s := open(t, srv, source.Guard{})
		if lang := s.Capabilities().Query.Language; lang != srv.name {
			t.Errorf("language %q on %s", lang, srv.name)
		}
		info, err := s.Info(context.Background())
		if err != nil || (info.Product == "MariaDB") != (srv.name == "mariadb") {
			t.Errorf("info %+v, %v", info, err)
		}
	})
}

func TestExactValues(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		s := open(t, srv, source.Guard{})
		rs, err := s.Browse(context.Background(), people, source.BrowseOptions{Limit: 1,
			Filters: []source.Filter{{Column: "id", Op: source.OpEqual, Values: []any{int64(1000)}}}})
		if err != nil {
			t.Fatal(err)
		}
		row := drain(t, rs)[0]
		if row[2] != model.Decimal("12345678901234567890.1234567890") {
			t.Errorf("score = %#v; a DECIMAL must keep every digit", row[2])
		}
		rs, _ = s.Browse(context.Background(), people, source.BrowseOptions{Limit: 1})
		first := drain(t, rs)[0]
		if srv.name == "mysql" {
			if _, ok := first[5].(model.JSON); !ok {
				t.Errorf("meta is %T; MySQL's JSON type should arrive as JSON", first[5])
			}
		}
		// MariaDB's JSON is an alias for LONGTEXT and arrives as text:
		// a divergence recorded in ADR-0015, not a defect.
	})
}

func TestCancelKeepsTheSession(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		s := open(t, srv, source.Guard{})
		ss, err := s.Session(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer ss.Close()
		if _, err := ss.Query(context.Background(), source.Statement{SQL: "SET @kept = 5"}); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		start := time.Now()
		res, err := ss.Query(ctx, source.Statement{SQL: "SELECT BENCHMARK(2000000000, SHA2('x', 256))"})
		if err == nil {
			_, err = res.Rows.Next(context.Background())
			res.Rows.Close()
		}
		if err == nil || time.Since(start) > 5*time.Second {
			t.Fatalf("err %v after %v; cancelling must stop the statement", err, time.Since(start))
		}
		res, err = ss.Query(context.Background(), source.Statement{SQL: "SELECT @kept"})
		if err != nil {
			t.Fatalf("the session did not survive the cancel: %v", err)
		}
		if got := drain(t, res.Rows); len(got) != 1 || fmt.Sprint(got[0][0]) != "5" {
			t.Errorf("@kept = %v; the session lost its state", got)
		}
	})
}

func TestReadOnlyIsRefusedByTheServerToo(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		s := open(t, srv, source.Guard{ReadOnly: true})
		if _, err := s.Query(context.Background(), source.Statement{SQL: "DELETE FROM ikigai_it.people"}); !errors.Is(err, source.ErrReadOnly) {
			t.Errorf("guard: %v", err)
		}
		if _, err := s.db.Exec("DELETE FROM ikigai_it.people WHERE id = 1"); err == nil {
			t.Error("the server accepted a write on a read-only connection")
		}
	})
}

func TestTheTreeListsEachClassOfADatabase(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		s := open(t, srv, source.Guard{})
		ctx := context.Background()
		for _, q := range []string{
			`CREATE TRIGGER ikigai_it.people_seen BEFORE UPDATE ON ikigai_it.people FOR EACH ROW SET NEW.seen = NEW.seen`,
			`CREATE FUNCTION ikigai_it.twice(x INT) RETURNS INT DETERMINISTIC RETURN x * 2`,
			`CREATE INDEX name_score ON ikigai_it.people (name, score)`,
		} {
			if _, err := s.db.ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		db := model.NewRef(model.KindDatabase, "ikigai_it")
		classes, err := s.Children(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		count := map[model.ObjectKind]string{}
		for _, c := range classes {
			got = append(got, c.Label+" "+c.Badge.Text)
			k, _ := model.ClassOf(c.Ref)
			count[k] = c.Badge.Text
		}
		// Five indexes: each table's PRIMARY, orders_total, the one the
		// foreign key made, and name_score over two columns.
		if want := []string{"Tables 3", "Views 1", "Indexes 5", "Triggers 1", "Routines 1"}; fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
			t.Fatalf("classes %q, want %q", got, want)
		}
		list := func(k model.ObjectKind) map[string]model.Node {
			kids, err := s.Children(ctx, model.ClassRef(db, k))
			if err != nil {
				t.Fatalf("%s: %v", k, err)
			}
			if n := strconv.Itoa(len(kids)); count[k] != n {
				t.Errorf("%s counted %s, and lists %s", k, count[k], n)
			}
			out := map[string]model.Node{}
			for _, n := range kids {
				if _, twice := out[n.Label]; twice {
					t.Errorf("%s lists %q twice", k, n.Label)
				}
				out[n.Label] = n
			}
			return out
		}
		if v, ok := list(model.KindView)["adults"]; !ok || v.Badge != nil || !v.Browsable {
			t.Errorf("view %+v; it has rows to browse, and no estimate of them", v)
		}
		idx := list(model.KindIndex)
		pk, ok := idx["PRIMARY on people"]
		if _, total := idx["orders_total on orders"]; !ok || !total || len(idx) != 5 ||
			!pk.Ref.Equal(model.NewRef(model.KindIndex, "ikigai_it", "people", "PRIMARY")) {
			t.Errorf("indexes %+v; each is named, and addressed, with its table", idx)
		}
		if trig, ok := list(model.KindTrigger)["people_seen on people"]; !ok || trig.Attrs["table"] != "people" ||
			!trig.Ref.Equal(model.NewRef(model.KindTrigger, "ikigai_it", "people_seen")) {
			t.Errorf("trigger %+v", trig)
		}
		if fn, ok := list(model.KindRoutine)["twice"]; !ok || fn.Attrs["kind"] != "function" ||
			!fn.Ref.Equal(model.NewRef(model.KindRoutine, "ikigai_it", "function", "twice")) {
			t.Errorf("routine %+v; a function and a procedure may share a name", fn)
		}
	})
}

func TestRowCountAndDelimitedScripts(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		s := open(t, srv, source.Guard{})
		ss, _ := s.Session(context.Background())
		defer ss.Close()
		ch, err := ss.QueryMulti(context.Background(), `UPDATE ikigai_it.people SET score = 0 WHERE id <= 3;
DELIMITER //
CREATE PROCEDURE ikigai_it.bump() BEGIN
  UPDATE ikigai_it.people SET score = score + 1 WHERE id = 1;
  SELECT score FROM ikigai_it.people WHERE id = 1;
END //
DELIMITER ;
CALL ikigai_it.bump();`, false)
		if err != nil {
			t.Fatal(err)
		}
		var res []source.ScriptResult
		for r := range ch {
			res = append(res, r)
		}
		if len(res) != 3 {
			t.Fatalf("%d results; the procedure body must not be split", len(res))
		}
		for _, r := range res {
			if r.Err != nil {
				t.Fatalf("%q: %v", r.Statement, r.Err)
			}
		}
		if res[0].Result.Affected != 3 {
			t.Errorf("UPDATE affected %d, want 3", res[0].Result.Affected)
		}
		if got := drain(t, res[2].Result.Rows); len(got) != 1 || got[0][0] != model.Decimal("1.0000000000") {
			t.Errorf("procedure returned %v", got)
		}
	})
}

func TestDescribe(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		s := open(t, srv, source.Guard{})
		v, err := s.Describe(context.Background(), model.NewRef(model.KindTable, "ikigai_it", "orders"))
		if err != nil {
			t.Fatal(err)
		}
		tb := v.(*model.Table)
		if tb.PrimaryKey == nil || tb.PrimaryKey.Columns[0] != "id" {
			t.Errorf("pk %+v", tb.PrimaryKey)
		}
		if len(tb.ForeignKeys) != 1 || tb.ForeignKeys[0].RefTable != "people" || tb.ForeignKeys[0].OnDelete != "CASCADE" {
			t.Errorf("foreign keys %+v", tb.ForeignKeys)
		}
		var total *model.Index
		for i := range tb.Indexes {
			if tb.Indexes[i].Name == "orders_total" {
				total = &tb.Indexes[i]
			}
		}
		if total == nil || !total.Columns[0].Descending {
			t.Errorf("indexes %+v", tb.Indexes)
		}
		vw, err := s.Describe(context.Background(), model.NewRef(model.KindView, "ikigai_it", "adults"))
		if err != nil || vw.(*model.View).Definition == "" {
			t.Errorf("view %+v, %v", vw, err)
		}
	})
}

func TestATableWithoutAKeyIsPagedByEveryColumn(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		s := open(t, srv, source.Guard{})
		ref := model.NewRef(model.KindTable, "ikigai_it", "nokey")
		var all []string
		for off := int64(0); off < 4; off += 2 {
			rs, err := s.Browse(context.Background(), ref, source.BrowseOptions{Offset: off, Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range drain(t, rs) {
				all = append(all, fmt.Sprint(r...))
			}
		}
		want := []string{"1x", "1x", "2y", "3z"}
		if fmt.Sprint(all) != fmt.Sprint(want) {
			t.Errorf("pages %v, want %v", all, want)
		}
	})
}

func TestConnectErrorsSayWhatIsWrong(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		kind := func(cfg source.ConnectionConfig) source.ConnectKind {
			_, err := Driver{}.Open(context.Background(), cfg)
			var ce *source.ConnectError
			if !errors.As(err, &ce) {
				t.Fatalf("%v is not a ConnectError", err)
			}
			return ce.Kind
		}
		bad := srv.config("ikigai_it", source.Guard{})
		bad.Secret = func(string) (string, error) { return "wrong", nil }
		if k := kind(bad); k != source.ConnectAuth {
			t.Errorf("wrong password: %v", k)
		}
		if k := kind(srv.config("no_such_db", source.Guard{})); k != source.ConnectNoDatabase {
			t.Errorf("unknown database: %v", k)
		}
		closed := srv.config("", source.Guard{})
		closed.Port = 53399
		if k := kind(closed); k != source.ConnectRefused {
			t.Errorf("nothing listening: %v", k)
		}
		strict := srv.config("", source.Guard{})
		strict.TLS = source.TLSConfig{Mode: "verify-full"}
		if k := kind(strict); k != source.ConnectTLS {
			t.Errorf("unverifiable server: %v", k)
		}
	})
}
