//go:build conformance

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/jackc/pgx/v5"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
	"github.com/ikigai-db/ikigai-db/internal/ui/shell"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"

	_ "github.com/ikigai-db/ikigai-db/internal/source/drivers/postgres"
)

const (
	schema = "e2e_j1"
	table  = "people"
	// rows is under one grid page, and PostgreSQL reports no exact count, so
	// the footer's total can only come from the grid reaching the end.
	rows = 42
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// server is the PostgreSQL the journey runs against, configured the same way
// as the driver's integration tests.
type server struct {
	host           string
	port           int
	db, user, pass string
}

func target() server {
	port, _ := strconv.Atoi(env("IKIGAI_PG_PORT", "55432"))
	return server{env("IKIGAI_PG_HOST", "localhost"), port, env("IKIGAI_PG_DB", "ikigai_test"),
		env("IKIGAI_PG_USER", "postgres"), env("IKIGAI_PG_PASSWORD", "ikigai")}
}

func (s server) dsn() string {
	return fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=disable",
		s.host, s.port, s.db, s.user, s.pass)
}

// fixture creates the table the journey browses. With no server it skips,
// or fails under IKIGAI_REQUIRE_PG, as in CI.
func fixture(t *testing.T, srv server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, srv.dsn())
	if err != nil {
		if os.Getenv("IKIGAI_REQUIRE_PG") != "" {
			t.Fatalf("PostgreSQL required but unavailable: %v", err)
		}
		t.Skipf("no PostgreSQL at port %d (docker start ikigai-pg): %v", srv.port, err)
	}
	defer conn.Close(ctx)
	sql := fmt.Sprintf(`DROP SCHEMA IF EXISTS %[1]s CASCADE;
		CREATE SCHEMA %[1]s;
		CREATE TABLE %[1]s.%[2]s (id int PRIMARY KEY, name text NOT NULL);
		INSERT INTO %[1]s.%[2]s SELECT g, 'person ' || g FROM generate_series(1, %[3]d) g;`,
		schema, table, rows)
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if c, err := pgx.Connect(ctx, srv.dsn()); err == nil {
			c.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
			c.Close(ctx)
		}
	})
}

// TestJ1BrowseATable is journey J1, the Phase 1 exit criterion: with a saved
// connection, find a table in the sidebar, open it, and see its rows.
func TestJ1BrowseATable(t *testing.T) {
	srv := target()
	fixture(t, srv)

	a := test.NewTempApp(t)
	sf, _, err := store.OpenSettings(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	conns := app.NewConnections(sf, app.NewVault(secrets.NewMemory(), nil), nil)
	c, err := conns.Create(store.SavedConnection{Name: "it", Driver: "postgres", Host: srv.host, Port: srv.port,
		Database: srv.db, User: srv.user, TLS: store.TLS{Mode: "disable"}},
		map[string]string{"password": srv.pass})
	if err != nil {
		t.Fatal(err)
	}
	ws := app.NewWorkspace(conns, app.MonitorConfig{Interval: time.Hour})
	t.Cleanup(func() { ws.CloseAll() })
	q := &uithread.Queue{}
	s := shell.New(a, shell.Deps{Conns: conns, WS: ws, Run: q.Run})
	w := s.Window()
	w.Resize(fyne.NewSize(1280, 800))

	// Walk the sidebar the way a user expands it: connection, database,
	// schema, the Tables folder, the table.
	path := []string{
		view.ConnectionID(c.ID),
		view.NodeID(c.ID, model.NewRef(model.KindDatabase, srv.db)),
		view.NodeID(c.ID, model.NewRef(model.KindSchema, srv.db, schema)),
		view.NodeID(c.ID, model.NewRef(model.KindFolder, srv.db, schema, "tables")),
		view.NodeID(c.ID, model.NewRef(model.KindTable, srv.db, schema, table)),
	}
	parent := explorer.RootID
	for _, id := range path {
		expand(t, q, s.Explorer.Model, parent, id)
		parent = id
	}

	s.Explorer.Tree.Select(parent)
	if err := s.Commands().Run("object.open"); err != nil {
		t.Fatalf("Open Data: %v", err)
	}
	tabs := findDocTabs(w.Content())
	if tabs == nil || tabs.Selected() == nil || tabs.Selected().Text != table {
		t.Fatalf("no tab for %q opened", table)
	}

	want := fmt.Sprintf("%d rows", rows)
	deadline := time.Now().Add(15 * time.Second)
	for {
		q.Flush()
		w.Canvas().Capture() // drawing the grid is what fetches its first page
		got := labelsIn(tabs.Selected().Content)
		if slices.Contains(got, want) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the tab never showed %q; it shows %q", want, got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// expand loads parent's children, as expanding it in the tree does, and waits
// for want among them.
func expand(t *testing.T, q *uithread.Queue, m *explorer.Model, parent, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		q.Flush()
		kids := m.Children(parent)
		if slices.Contains(kids, want) {
			return
		}
		for _, k := range kids {
			if it, st, err := m.Item(k); st == explorer.Failed {
				t.Fatalf("expanding the sidebar failed: %s: %v", it.Label, err)
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%q never appeared in the sidebar; saw %q", want, m.Children(parent))
}

func findDocTabs(o fyne.CanvasObject) *container.DocTabs {
	switch v := o.(type) {
	case *container.DocTabs:
		return v
	case *container.Split:
		if d := findDocTabs(v.Leading); d != nil {
			return d
		}
		return findDocTabs(v.Trailing)
	case *fyne.Container:
		for _, c := range v.Objects {
			if d := findDocTabs(c); d != nil {
				return d
			}
		}
	}
	return nil
}

func labelsIn(o fyne.CanvasObject) []string {
	switch v := o.(type) {
	case *widget.Label:
		return []string{v.Text}
	case *fyne.Container:
		var out []string
		for _, c := range v.Objects {
			out = append(out, labelsIn(c)...)
		}
		return out
	}
	return nil
}
