//go:build conformance

package e2e

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
	"github.com/ikigai-db/ikigai-db/internal/ui/editor/view"
	"github.com/ikigai-db/ikigai-db/internal/ui/shell"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// TestJ3WriteRunSaveAndFindAgain is journey J3, the other Phase 1 exit
// criterion: type SQL, run it, inspect the result, refine it, save it, and
// find it again through history. It drives only what a user touches: text
// typed into the editor, commands run as the menus run them, and dialogs
// filled in through their widgets.
func TestJ3WriteRunSaveAndFindAgain(t *testing.T) {
	srv := target()
	fixture(t, srv)

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
	c, err := conns.Create(store.SavedConnection{Name: "it", Driver: "postgres", Host: srv.host, Port: srv.port,
		Database: srv.db, User: srv.user, TLS: store.TLS{Mode: "disable"}},
		map[string]string{"password": srv.pass})
	if err != nil {
		t.Fatal(err)
	}
	ws := app.NewWorkspace(conns, app.MonitorConfig{Interval: time.Hour})
	t.Cleanup(func() { ws.CloseAll() })
	q := &uithread.Queue{}
	s := shell.New(a, shell.Deps{Conns: conns, WS: ws, History: db, Saved: db, Run: q.Run})
	w := s.Window()
	t.Cleanup(w.Close) // quit as a user does, before the workspace cleanup above
	w.Resize(fyne.NewSize(1280, 800))
	tabs := findDocTabs(w.Content())

	// Type a query and run it.
	s.OpenQuery(c.ID)
	ed := find[*view.Editor](tabs.Selected().Content)[0]
	test.Type(ed.Focusable(), "SELECT id, name FROM e2e_j1.people WHERE id <= 10 ORDER BY id")
	runWhenReady(t, q, s, "query.run")
	waitFor(t, q, "the first result", func() bool { return hasLabel(tabs.Selected().Content, "10 rows") })

	// Refine it and run again.
	ed.Document().SelectAll()
	test.Type(ed.Focusable(), "SELECT id, name FROM e2e_j1.people WHERE id > 40 ORDER BY id")
	runWhenReady(t, q, s, "query.run")
	waitFor(t, q, "the refined result", func() bool { return hasLabel(tabs.Selected().Content, "2 rows") })
	refined := ed.Document().Text()

	// Save it.
	if err := s.Commands().Run("query.save"); err != nil {
		t.Fatalf("Save Query: %v", err)
	}
	sheet := w.Canvas().Overlays().Top()
	find[*widget.Entry](sheet)[0].SetText("Tail of people")
	test.Tap(button(t, sheet, "Save"))
	waitFor(t, q, "the tab to take the saved name", func() bool { return tabs.Selected().Text == "Tail of people" })

	// Find it again through history, by words only the refined statement has.
	waitFor(t, q, "history to record the run", func() bool {
		got, _ := db.SearchHistory(context.Background(), localdb.HistoryQuery{Text: "people 40"})
		return len(got) == 1
	})
	if err := s.Commands().Run("query.history"); err != nil {
		t.Fatalf("Query History: %v", err)
	}
	panel := w.Canvas().Overlays().Top()
	test.Type(find[*widget.Entry](panel)[0], "people 40")
	list := find[*widget.List](panel)[0]
	waitFor(t, q, "the history search", func() bool { return list.Length() == 1 })
	list.Select(0)
	if len(tabs.Items) != 2 {
		t.Fatalf("%d tabs; the history entry should open in a new one", len(tabs.Items))
	}
	if got := find[*view.Editor](tabs.Selected().Content)[0].Document().Text(); got != refined {
		t.Errorf("history reopened %q, want %q", got, refined)
	}

	// And through saved queries: it is open already, so its tab comes forward.
	if err := s.Commands().Run("query.openSaved"); err != nil {
		t.Fatalf("Open Saved Query: %v", err)
	}
	saved := find[*widget.List](w.Canvas().Overlays().Top())[0]
	waitFor(t, q, "the saved list", func() bool { return saved.Length() == 1 })
	saved.Select(0)
	if len(tabs.Items) != 2 || tabs.Selected().Text != "Tail of people" {
		t.Errorf("%d tabs, selected %q; the saved query's open tab should come forward", len(tabs.Items), tabs.Selected().Text)
	}
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
		case *container.DocTabs:
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
