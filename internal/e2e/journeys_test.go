package e2e

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor/view"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	explorerview "github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
	"github.com/ikigai-db/ikigai-db/internal/ui/shell"
	"github.com/ikigai-db/ikigai-db/internal/ui/tabbar"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// fixtureRows is how many rows every engine's people table holds. It is under
// one grid page, so a total the grid finds by reaching the end is exact.
const fixtureRows = 42

// journey is one engine's setup: a saved connection to a database holding a
// table people (id, name) of fixtureRows rows.
type journey struct {
	conn    store.SavedConnection
	secrets map[string]string
	path    []model.ObjectRef // the sidebar walk from the connection to the table
	table   string            // how a query names the table
}

// harness is the application over one engine, as a person would have it.
type harness struct {
	s    *shell.Shell
	q    *uithread.Queue
	w    fyne.Window
	tabs *tabbar.Tabs
	db   *localdb.DB
	conn store.SavedConnection
}

func start(t *testing.T, j journey) *harness {
	t.Helper()
	a := test.NewTempApp(t)
	dir := t.TempDir()
	db, err := localdb.Open(context.Background(), filepath.Join(dir, "ikigai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sf, _, err := store.OpenSettings(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	conns := app.NewConnections(sf, app.NewVault(secrets.NewMemory(), nil), nil)
	c, err := conns.Create(j.conn, j.secrets)
	if err != nil {
		t.Fatal(err)
	}
	ws := app.NewWorkspace(conns, app.MonitorConfig{Interval: time.Hour})
	t.Cleanup(func() { ws.CloseAll() })
	q := &uithread.Queue{}
	s := shell.New(a, shell.Deps{Conns: conns, WS: ws, History: db, Saved: db, Run: q.Run})
	w := s.Window()
	t.Cleanup(w.Close) // quit as a person does, before the workspace cleanup above
	w.Resize(fyne.NewSize(1280, 800))
	return &harness{s: s, q: q, w: w, tabs: findTabs(w.Content()), db: db, conn: c}
}

// runJ1 is journey J1: with a saved connection, find a table in the sidebar,
// open it, and see its rows.
func runJ1(t *testing.T, j journey) {
	h := start(t, j)
	ids := []string{explorerview.ConnectionID(h.conn.ID)}
	for _, ref := range j.path {
		ids = append(ids, explorerview.NodeID(h.conn.ID, ref))
	}
	parent := explorer.RootID
	for _, id := range ids {
		expand(t, h.q, h.s.Explorer.Model, parent, id)
		parent = id
	}
	h.s.Explorer.Tree.Select(parent)
	if err := h.s.Commands().Run("object.open"); err != nil {
		t.Fatalf("Open Data: %v", err)
	}
	table := j.path[len(j.path)-1].Name()
	if h.tabs.Selected() == nil || h.tabs.Selected().Text != table {
		t.Fatalf("no tab for %q opened", table)
	}
	want := fmt.Sprintf("%d rows", fixtureRows)
	waitFor(t, h.q, "the table's rows", func() bool {
		h.w.Canvas().Capture() // drawing the grid is what fetches its first page
		return hasLabel(h.tabs.Selected().Content, want)
	})
}

// runJ3 is journey J3: type SQL, run it, inspect the result, refine it, save
// it, and find it again through history and through saved queries.
func runJ3(t *testing.T, j journey) {
	h := start(t, j)
	tabs := h.tabs

	h.s.OpenQuery(h.conn.ID)
	ed := find[*view.Editor](tabs.Selected().Content)[0]
	test.Type(ed.Focusable(), "SELECT id, name FROM "+j.table+" WHERE id <= 10 ORDER BY id")
	runWhenReady(t, h.q, h.s, "query.run")
	waitFor(t, h.q, "the first result", func() bool { return hasLabel(tabs.Selected().Content, "10 rows") })

	ed.Document().SelectAll()
	test.Type(ed.Focusable(), "SELECT id, name FROM "+j.table+" WHERE id > 40 ORDER BY id")
	runWhenReady(t, h.q, h.s, "query.run")
	waitFor(t, h.q, "the refined result", func() bool { return hasLabel(tabs.Selected().Content, "2 rows") })
	refined := ed.Document().Text()

	if err := h.s.Commands().Run("query.save"); err != nil {
		t.Fatalf("Save Query: %v", err)
	}
	sheet := h.w.Canvas().Overlays().Top()
	find[*widget.Entry](sheet)[0].SetText("Tail of people")
	test.Tap(button(t, sheet, "Save"))
	waitFor(t, h.q, "the tab to take the saved name", func() bool { return tabs.Selected().Text == "Tail of people" })

	// Find it again through history, by words only the refined statement has.
	waitFor(t, h.q, "history to record the run", func() bool {
		got, _ := h.db.SearchHistory(context.Background(), localdb.HistoryQuery{Text: "people 40"})
		return len(got) == 1
	})
	if err := h.s.Commands().Run("query.history"); err != nil {
		t.Fatalf("Query History: %v", err)
	}
	panel := h.s.Panel()
	test.Type(find[textField](panel)[0], "people 40")
	list := find[*widget.List](panel)[0]
	waitFor(t, h.q, "the history search", func() bool { return list.Length() == 1 })
	list.Select(0)
	if len(tabs.Items) != 2 {
		t.Fatalf("%d tabs; the history entry should open in a new one", len(tabs.Items))
	}
	if got := find[*view.Editor](tabs.Selected().Content)[0].Document().Text(); got != refined {
		t.Errorf("history reopened %q, want %q", got, refined)
	}

	// And through saved queries: it is open already, so its tab comes forward.
	if err := h.s.Commands().Run("query.openSaved"); err != nil {
		t.Fatalf("Open Saved Query: %v", err)
	}
	saved := find[*widget.List](h.s.Panel())[0]
	waitFor(t, h.q, "the saved list", func() bool { return saved.Length() == 1 })
	saved.Select(0)
	if len(tabs.Items) != 2 || tabs.Selected().Text != "Tail of people" {
		t.Errorf("%d tabs, selected %q; the saved query's open tab should come forward", len(tabs.Items), tabs.Selected().Text)
	}
}

// textField is a field that takes focus and text. The side panels' search
// fields are the shell's own entry type, not a widget.Entry, so they are
// found by what they do. In a panel nothing else does both: labels take no
// focus, and a list takes no text.
type textField interface {
	fyne.CanvasObject
	fyne.Focusable
	SetText(string)
}

// expand loads parent's children, as expanding it in the tree does, and waits
// for want among them.
func expand(t *testing.T, q *uithread.Queue, m *explorer.Model, parent, want string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
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

// runWhenReady runs a command once it is enabled: a query tab can run only
// once its session has connected.
func runWhenReady(t *testing.T, q *uithread.Queue, s *shell.Shell, id string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		q.Flush()
		err := s.Commands().Run(id)
		if err == nil {
			return
		}
		if !errors.Is(err, commands.ErrDisabled) || time.Now().After(deadline) {
			t.Fatalf("%s: %v", id, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitFor(t *testing.T, q *uithread.Queue, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		if q.Flush() == 0 {
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func hasLabel(o fyne.CanvasObject, text string) bool {
	return slices.ContainsFunc(find[*widget.Label](o), func(l *widget.Label) bool { return l.Text == text })
}

func button(t *testing.T, o fyne.CanvasObject, text string) *widget.Button {
	t.Helper()
	for _, b := range find[*widget.Button](o) {
		if b.Text == text {
			return b
		}
	}
	t.Fatalf("no %q button", text)
	return nil
}

func findTabs(o fyne.CanvasObject) *tabbar.Tabs {
	if tabs := find[*tabbar.Tabs](o); len(tabs) > 0 {
		return tabs[0]
	}
	return nil
}

// find collects every object of type T under o, looking inside containers,
// tabs, splits, pop-ups and, through their renderers, any other widget.
func find[T fyne.CanvasObject](o fyne.CanvasObject) []T {
	var out []T
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		if o == nil {
			return
		}
		if v, ok := o.(T); ok {
			out = append(out, v)
		}
		switch v := o.(type) {
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
		case *container.AppTabs:
			for _, it := range v.Items {
				walk(it.Content)
			}
		case *tabbar.Tabs:
			for _, it := range v.Items {
				walk(it.Content)
			}
		case *container.Split:
			walk(v.Leading)
			walk(v.Trailing)
		case *widget.PopUp:
			walk(v.Content)
		case fyne.Widget:
			for _, c := range test.WidgetRenderer(v).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	return out
}
