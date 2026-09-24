package localdb

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var ctx = context.Background()

func open(t *testing.T) (*DB, string) {
	t.Helper()
	// A space in the path, because macOS keeps this under "Application Support".
	dir := filepath.Join(t.TempDir(), "Application Support")
	os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, "ikigai.db")
	d, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d, path
}

func TestOpenMigratesAndIsPrivate(t *testing.T) {
	d, path := open(t)
	if v, _ := d.SchemaVersion(ctx); v != len(migrations) {
		t.Errorf("schema version %d, want %d", v, len(migrations))
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Errorf("database mode %v, want 0600", st.Mode().Perm())
		}
	}
	d.Close()
	d2, err := Open(ctx, path) // reopening is a no-op migration
	if err != nil {
		t.Fatal(err)
	}
	d2.Close()
}

func TestFullTextSearchIsCompiledIn(t *testing.T) {
	d, _ := open(t)
	if _, err := d.db.ExecContext(ctx, "CREATE VIRTUAL TABLE probe USING fts5(x)"); err != nil {
		t.Fatalf("FTS5 is missing from this SQLite build; history search depends on it: %v", err)
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	d, path := open(t)
	d.db.ExecContext(ctx, "PRAGMA user_version = 99")
	d.Close()
	if _, err := Open(ctx, path); !errors.Is(err, ErrNewerSchema) {
		t.Errorf("want ErrNewerSchema, got %v", err)
	}
}

func TestFailedMigrationLeavesSchemaUntouched(t *testing.T) {
	d, path := open(t)
	d.Close()

	orig := migrations
	migrations = append(append([]string(nil), orig...),
		`CREATE TABLE half_done (a int); THIS IS NOT SQL;`)
	_, err := Open(ctx, path)
	migrations = orig
	if err == nil {
		t.Fatal("a broken migration was accepted")
	}

	d2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopening after a failed migration: %v", err)
	}
	defer d2.Close()
	if v, _ := d2.SchemaVersion(ctx); v != len(orig) {
		t.Errorf("schema version %d after a failed migration, want %d", v, len(orig))
	}
	var n int
	d2.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name = 'half_done'").Scan(&n)
	if n != 0 {
		t.Error("a failed migration left a table behind; migrations are not atomic")
	}
}

func add(t *testing.T, d *DB, conn, stmt, errText string, at time.Time) int64 {
	t.Helper()
	id, err := d.AddHistory(ctx, HistoryEntry{ConnectionID: conn, Statement: stmt,
		StartedAt: at, Duration: 12 * time.Millisecond, Rows: 3, Error: errText})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func statements(es []HistoryEntry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Statement)
	}
	return out
}

func TestHistorySearch(t *testing.T) {
	d, _ := open(t)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	add(t, d, "pg", "SELECT customer_id FROM orders", "", base)
	add(t, d, "pg", "SELECT * FROM customers WHERE region = 'emea'", "", base.Add(time.Minute))
	add(t, d, "my", "DELETE FROM orders_archive", "", base.Add(2*time.Minute))
	add(t, d, "pg", "SELEC broken", "syntax error", base.Add(3*time.Minute))

	cases := []struct {
		q    HistoryQuery
		want int
	}{
		{HistoryQuery{}, 4},
		{HistoryQuery{Text: "orders"}, 2},      // prefix: orders, orders_archive
		{HistoryQuery{Text: "customer_id"}, 1}, // identifiers stay whole
		{HistoryQuery{Text: "cust"}, 2},        // prefix of customer_id and customers
		{HistoryQuery{Text: "ORDERS"}, 2},      // case-insensitive
		{HistoryQuery{Text: "cust ord"}, 1},    // every word required
		{HistoryQuery{ConnectionID: "my"}, 1},
		{HistoryQuery{FailedOnly: true}, 1},
		{HistoryQuery{Text: "orders", ConnectionID: "pg"}, 1},
		{HistoryQuery{Limit: 2}, 2},
	}
	for _, c := range cases {
		got, err := d.SearchHistory(ctx, c.q)
		if err != nil {
			t.Errorf("%+v: %v", c.q, err)
			continue
		}
		if len(got) != c.want {
			t.Errorf("%+v: %d results %q, want %d", c.q, len(got), statements(got), c.want)
		}
	}

	all, _ := d.SearchHistory(ctx, HistoryQuery{})
	if all[0].Statement != "SELEC broken" {
		t.Errorf("history is not newest first: %q", statements(all))
	}
	if all[0].Duration != 12*time.Millisecond || all[0].Rows != 3 || !all[0].StartedAt.Equal(base.Add(3*time.Minute)) {
		t.Errorf("fields not round-tripped: %+v", all[0])
	}
}

func TestHostileSearchTextNeverBreaksTheQuery(t *testing.T) {
	d, _ := open(t)
	add(t, d, "pg", "SELECT 1 FROM order_by_test", "", time.Now())
	for _, q := range []string{`"`, `" OR 1`, `NEAR(a b)`, `*`, `-`, `order-by`, `a:b`, `^x`, `NOT`, `(`, `'`, "   "} {
		if _, err := d.SearchHistory(ctx, HistoryQuery{Text: q}); err != nil {
			t.Errorf("search %q failed: %v", q, err)
		}
	}
}

func TestHistoryIsRedacted(t *testing.T) {
	d, _ := open(t)
	add(t, d, "pg", "CREATE USER bob WITH PASSWORD 'hunter2xyz'", "", time.Now())
	add(t, d, "pg", "SELECT 1", "connect failed: password=hunter2xyz", time.Now())

	all, _ := d.SearchHistory(ctx, HistoryQuery{})
	for _, e := range all {
		if strings.Contains(e.Statement, "hunter2xyz") || strings.Contains(e.Error, "hunter2xyz") {
			t.Errorf("secret stored in history: %+v", e)
		}
	}
	// The search index must not hold it either.
	if hits, _ := d.SearchHistory(ctx, HistoryQuery{Text: "hunter2xyz"}); len(hits) != 0 {
		t.Error("the secret is findable through the full-text index")
	}
}

func TestPruneKeepsNewestAndTheIndexInStep(t *testing.T) {
	d, _ := open(t)
	base := time.Now()
	for i := 0; i < 30; i++ {
		add(t, d, "pg", fmt.Sprintf("SELECT marker_%d", i), "", base.Add(time.Duration(i)*time.Second))
	}
	if n, err := d.PruneHistory(ctx, 10); err != nil || n != 20 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	all, _ := d.SearchHistory(ctx, HistoryQuery{})
	if len(all) != 10 || all[len(all)-1].Statement != "SELECT marker_20" {
		t.Errorf("kept the wrong rows: %q", statements(all))
	}
	if hits, _ := d.SearchHistory(ctx, HistoryQuery{Text: "marker_5"}); len(hits) != 0 {
		t.Error("a pruned statement is still in the search index")
	}
}

func TestConcurrentWritesDoNotLock(t *testing.T) {
	d, _ := open(t)
	var wg sync.WaitGroup
	errs := make(chan error, 400)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := d.AddHistory(ctx, HistoryEntry{ConnectionID: "pg",
					Statement: fmt.Sprintf("SELECT %d", g*100+i), StartedAt: time.Now()}); err != nil {
					errs <- err
				}
				d.SearchHistory(ctx, HistoryQuery{Text: "select", Limit: 5})
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent write: %v", err)
	}
	all, _ := d.SearchHistory(ctx, HistoryQuery{Limit: 5000})
	if len(all) != 400 {
		t.Errorf("%d rows, want 400", len(all))
	}
}

func TestRedactStatement(t *testing.T) {
	cases := []struct {
		lang, in, mustNot, mustKeep string
	}{
		{"", "CREATE USER bob WITH PASSWORD 'hunter2'", "hunter2", "CREATE USER bob WITH PASSWORD"},
		{"", "ALTER ROLE bob ENCRYPTED PASSWORD 'hunter2' VALID UNTIL 'infinity'", "hunter2", "'infinity'"},
		{"", "CREATE ROLE bob LOGIN PASSWORD $$hunter2$$", "hunter2", "CREATE ROLE bob LOGIN PASSWORD"},
		{"mysql", "CREATE USER 'bob'@'%' IDENTIFIED BY 'hunter2'", "hunter2", "'bob'@'%'"},
		{"mysql", "ALTER USER bob IDENTIFIED WITH caching_sha2_password BY 'hunter2'", "hunter2", "caching_sha2_password"},
		{"mysql", "SET PASSWORD FOR bob = 'hunter2'", "hunter2", "SET PASSWORD FOR bob"},
		{"", "CREATE USER bob IDENTIFIED BY hunter2", "hunter2", "CREATE USER bob IDENTIFIED BY"}, // Oracle, bare
		{"", "SELECT dblink_connect('host=db user=u password=hunter2')", "hunter2", "dblink_connect"},
		{"", "SELECT * FROM t WHERE x = 'postgres://u:hunter2@h/d'", "hunter2", "SELECT * FROM t"},
		{"", "CREATE USER bob PASSWORD 'hunter2\nsecond line'", "second line", "CREATE USER bob PASSWORD"},
		// Nothing to redact: must come back unchanged.
		{"", "SELECT * FROM users WHERE password_hash = 'abc'", "", "password_hash = 'abc'"},
		{"", "SELECT 'plain text', 42 FROM t -- password 'in a comment'", "", "'plain text', 42"},
	}
	for _, c := range cases {
		got := RedactStatement(c.lang, c.in)
		if c.mustNot != "" && strings.Contains(got, c.mustNot) {
			t.Errorf("not redacted:\n  in  %q\n  out %q", c.in, got)
		}
		if !strings.Contains(got, c.mustKeep) {
			t.Errorf("over-redacted:\n  in  %q\n  out %q\n  lost %q", c.in, got, c.mustKeep)
		}
		if c.mustNot == "" && got != c.in {
			t.Errorf("changed a statement with nothing to redact:\n  in  %q\n  out %q", c.in, got)
		}
	}
	// Arming stops at the statement's end: the next statement's literal is kept.
	if got := RedactStatement("", "ALTER ROLE r PASSWORD 'x'; SELECT 'keep me'"); !strings.Contains(got, "'keep me'") {
		t.Errorf("redaction leaked across a statement boundary: %q", got)
	}
}

func TestSavedQueriesAreVerbatim(t *testing.T) {
	d, _ := open(t)
	body := "-- my script\nCREATE USER bob PASSWORD 'deliberately-saved';\nSELECT 1;"
	q, err := d.SaveQuery(ctx, SavedQuery{Folder: "admin", Name: "make bob", Body: body})
	if err != nil {
		t.Fatal(err)
	}
	if q.ID == "" || q.Body != body || q.CreatedAt.IsZero() {
		t.Errorf("saved query not stored verbatim: %+v", q)
	}

	q.Body = "SELECT 2"
	time.Sleep(2 * time.Millisecond)
	q2, err := d.SaveQuery(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if q2.Body != "SELECT 2" || !q2.CreatedAt.Equal(q.CreatedAt) || !q2.UpdatedAt.After(q.CreatedAt) {
		t.Errorf("update wrong: %+v", q2)
	}

	if _, err := d.SaveQuery(ctx, SavedQuery{Folder: "admin", Name: "make bob", Body: "x"}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("duplicate name: want ErrNameTaken, got %v", err)
	}
	if _, err := d.SaveQuery(ctx, SavedQuery{Folder: "other", Name: "make bob", Body: "x"}); err != nil {
		t.Errorf("the same name in another folder should be allowed: %v", err)
	}

	list, _ := d.SavedQueries(ctx)
	if len(list) != 2 || list[0].Folder != "admin" {
		t.Errorf("list = %+v", list)
	}
	d.DeleteQuery(ctx, q.ID)
	if list, _ := d.SavedQueries(ctx); len(list) != 1 {
		t.Errorf("delete failed: %+v", list)
	}
}

func TestSessionState(t *testing.T) {
	d, _ := open(t)
	if _, ok, err := d.Get(ctx, "tabs"); ok || err != nil {
		t.Errorf("missing key: ok=%v err=%v", ok, err)
	}
	d.Put(ctx, "tabs", []byte(`["a"]`))
	d.Put(ctx, "tabs", []byte(`["a","b"]`))
	if v, ok, _ := d.Get(ctx, "tabs"); !ok || string(v) != `["a","b"]` {
		t.Errorf("got %q", v)
	}
	d.Delete(ctx, "tabs")
	if _, ok, _ := d.Get(ctx, "tabs"); ok {
		t.Error("deleted key still present")
	}
}

// A copy of the database can be taken while it is open and in use, which
// is what a backup of a running application needs (FR-17.5).
func TestTheDatabaseIsCopiedWhileOpen(t *testing.T) {
	db, _ := open(t)
	ctx := context.Background()
	if _, err := db.SaveQuery(ctx, SavedQuery{Name: "totals", Body: "select 1"}); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(t.TempDir(), "copy.db")
	if err := db.BackupTo(ctx, to); err != nil {
		t.Fatal(err)
	}
	// Whole on its own: no -wal beside it to carry, and the row is there.
	copied, err := Open(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	defer copied.Close()
	if got, err := copied.SavedQueries(ctx); err != nil || len(got) != 1 || got[0].Name != "totals" {
		t.Errorf("the copy holds %+v (%v)", got, err)
	}
	// And the one it was copied from is still open and still writes.
	if _, err := db.SaveQuery(ctx, SavedQuery{Name: "after", Body: "select 2"}); err != nil {
		t.Errorf("the database it copied is no longer usable: %v", err)
	}
}
