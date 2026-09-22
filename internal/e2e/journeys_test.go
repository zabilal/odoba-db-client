package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
	"github.com/ikigai-db/ikigai-db/internal/ui/filedlg"
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
	s     *shell.Shell
	q     *uithread.Queue
	w     fyne.Window
	tabs  *tabbar.Tabs
	db    *localdb.DB
	conn  store.SavedConnection
	files *chooser
}

// chooser stands in for the file dialog. A person picks a file; a journey
// says which, and the rest of the path is the application's own.
type chooser struct {
	answer func(string, error)
	asked  string // the message the dialog would have shown
}

func (c *chooser) Save(_ fyne.Window, o filedlg.Options, done filedlg.Done) {
	c.asked = o.Message
	c.answer = done
}
func (c *chooser) Open(_ fyne.Window, o filedlg.Options, done filedlg.Done) {
	c.asked = o.Message
	c.answer = done
}

// picks answers the dialog now waiting, as choosing that file would.
func (c *chooser) picks(t *testing.T, q *uithread.Queue, path string) {
	t.Helper()
	waitFor(t, q, "the file dialog", func() bool { return c.answer != nil })
	done := c.answer
	c.answer = nil
	q.Run(func() { done(path, nil) })
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
	files := &chooser{}
	s := shell.New(a, shell.Deps{Conns: conns, WS: ws, History: db, Saved: db, Run: q.Run, Files: files})
	w := s.Window()
	t.Cleanup(w.Close) // quit as a person does, before the workspace cleanup above
	w.Resize(fyne.NewSize(1280, 800))
	return &harness{s: s, q: q, w: w, tabs: findTabs(w.Content()), db: db, conn: c, files: files}
}

// openTable walks the sidebar to the fixture table and opens its rows, which
// is J1 and the first step of every journey that works on a table.
func openTable(t *testing.T, h *harness, j journey) {
	t.Helper()
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

// runJ1 is journey J1: with a saved connection, find a table in the sidebar,
// open it, and see its rows.
func runJ1(t *testing.T, j journey) {
	openTable(t, start(t, j), j)
}

// runJ2 is journey J2: filter by a value, edit a cell, review the SQL that
// would run, and commit it.
//
// The point of the journey is that the review is not decoration. What it
// shows is what runs: the statement is rendered before anything is sent, and
// committing runs that and nothing else.
func runJ2(t *testing.T, j journey) {
	h := start(t, j)
	openTable(t, h, j)
	g := h.s.ActiveGrid()
	if g == nil {
		t.Fatal("the table's tab shows no grid")
	}
	ctx := context.Background()

	// Filter the name column to one person.
	const who = "person 7"
	if !g.Filterable() {
		t.Fatal("a table's rows have no filter row")
	}
	g.SetFilterText(1, who)
	g.ApplyFilters()
	waitFor(t, h.q, "the filtered row", func() bool {
		h.w.Canvas().Capture()
		n, final := g.Model().Extent()
		return final && n == 1
	})

	row, ok := g.Model().Row(ctx, 0)
	if !ok {
		t.Fatal("the matching row never loaded")
	}
	if got := fmt.Sprint(row[1]); got != who {
		t.Fatalf("the row shown is %q, want %q", got, who)
	}

	// Fix it. OnEdit is where a typed edit arrives, whether it was typed
	// into the cell or set from the form.
	const fixed = "person 7, corrected"
	if err := g.OnEdit(row, 1, fixed); err != nil {
		t.Fatalf("editing the cell: %v", err)
	}

	// Review what would run, before anything has.
	if err := h.s.Commands().Run("data.reviewChanges"); err != nil {
		t.Fatalf("Review Changes: %v", err)
	}
	sheet := h.w.Canvas().Overlays().Top()
	if sheet == nil {
		t.Fatal("Review Changes showed nothing")
	}
	said := strings.Join(labelsIn(sheet), "\n")
	// The table by its own name, not by the way a query would qualify it:
	// the statement quotes each part of a qualified name separately.
	table := j.path[len(j.path)-1].Name()
	for _, want := range []string{"UPDATE", table, "name"} {
		if !strings.Contains(said, want) {
			t.Errorf("the review does not mention %q:\n%s", want, said)
		}
	}
	if strings.Contains(said, "DELETE") || strings.Contains(said, "INSERT") {
		t.Errorf("the review offers more than the one edit:\n%s", said)
	}

	// Commit it, and see the value it wrote come back from the server.
	test.Tap(button(t, sheet, "Commit"))
	waitFor(t, h.q, "the change to be written", func() bool {
		h.w.Canvas().Capture()
		r, ok := g.Model().Row(ctx, 0)
		return ok && r != nil && fmt.Sprint(r[1]) == fixed
	})

	// And it is the row it was, not a new one: clearing the filter finds the
	// table the same size as before.
	g.SetFilterText(1, "")
	g.ApplyFilters()
	waitFor(t, h.q, "every row again", func() bool {
		h.w.Canvas().Capture()
		n, final := g.Model().Extent()
		return final && n == fixtureRows
	})
}

// runJ5 is journey J5: move data. Export a filtered result to a file, then
// import a CSV into a table, with its columns paired and a dry run first.
func runJ5(t *testing.T, j journey) {
	h := start(t, j)
	openTable(t, h, j)
	g := h.s.ActiveGrid()
	if g == nil {
		t.Fatal("the table's tab shows no grid")
	}
	dir := t.TempDir()

	// Export only what is filtered, not the whole table.
	g.SetFilterText(1, "person 1")
	g.ApplyFilters()
	waitFor(t, h.q, "the filtered rows", func() bool {
		h.w.Canvas().Capture()
		n, final := g.Model().Extent()
		return final && n > 0 && n < fixtureRows
	})
	filtered, _ := g.Model().Extent()

	if err := h.s.Commands().Run("data.export"); err != nil {
		t.Fatalf("Export: %v", err)
	}
	sheet := h.w.Canvas().Overlays().Top()
	if sheet == nil {
		t.Fatal("Export showed nothing")
	}
	test.Tap(button(t, sheet, "Choose File…"))
	out := filepath.Join(dir, "people.csv")
	h.files.picks(t, h.q, out)

	var exported []byte
	waitFor(t, h.q, "the file to be written", func() bool {
		b, err := os.ReadFile(out)
		exported = b
		return err == nil && bytes.Count(b, []byte("\n")) == int(filtered)+1
	})
	if !bytes.HasPrefix(exported, []byte("id,name\n")) {
		t.Errorf("the export starts %q, and should name its columns first", first(exported))
	}
	if bytes.Contains(exported, []byte("person 2,")) || bytes.Contains(exported, []byte(",person 2")) {
		t.Errorf("the export carries rows the filter left out:\n%s", exported)
	}

	// Clear the filter, so what the import adds is counted against the whole
	// table rather than against a view of it.
	g.SetFilterText(1, "")
	g.ApplyFilters()
	waitFor(t, h.q, "every row again", func() bool {
		h.w.Canvas().Capture()
		n, final := g.Model().Extent()
		return final && n == fixtureRows
	})

	// Import a file whose columns are in the other order, so that pairing
	// them by name is what puts each value in the right place.
	in := filepath.Join(dir, "newcomers.csv")
	const body = "name,id\nAda,1001\nGrace,1002\n"
	if err := os.WriteFile(in, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.s.Commands().Run("data.import"); err != nil {
		t.Fatalf("Import: %v", err)
	}
	h.files.picks(t, h.q, in)
	waitFor(t, h.q, "the import to open", func() bool {
		return h.tabs.Selected() != nil && strings.HasPrefix(h.tabs.Selected().Text, "Import newcomers.csv")
	})
	imp := h.tabs.Selected().Content

	// Wait for the file's columns to be paired with the table's before
	// asking anything of them. Until that lands the panel pairs by position,
	// and this file is deliberately in the other order — which is the point
	// of the journey, and would otherwise be a race that passes whenever the
	// two orders happen to agree.
	waitFor(t, h.q, "the columns to be paired", func() bool {
		picked := selected(imp)
		return slices.Contains(picked, "id") && slices.Contains(picked, "name")
	})

	// A dry run first. It reads every row as the import would write it and
	// says so in its own words, which are not the import's.
	test.Tap(button(t, imp, "Dry Run"))
	waitFor(t, h.q, "the dry run", func() bool {
		return strings.Contains(strings.Join(labelsIn(imp), " "), "All 2 rows would go in.")
	})
	if n, _ := g.Model().Extent(); n != fixtureRows {
		t.Errorf("the dry run wrote %d rows, and it must write none", n-fixtureRows)
	}

	// Then the import itself, which the table catches up with on its own.
	test.Tap(button(t, imp, "Import"))
	waitFor(t, h.q, "the table to hold them", func() bool {
		h.w.Canvas().Capture()
		n, final := g.Model().Extent()
		return final && n == fixtureRows+2
	})
	g.SetFilterText(0, "1001")
	g.ApplyFilters()
	waitFor(t, h.q, "the imported row", func() bool {
		h.w.Canvas().Capture()
		n, final := g.Model().Extent()
		return final && n == 1
	})
	row, ok := g.Model().Row(context.Background(), 0)
	if !ok {
		t.Fatal("the imported row never loaded")
	}
	if got := fmt.Sprint(row[1]); got != "Ada" {
		t.Errorf("the imported row reads %v; its columns were paired the wrong way round", row)
	}
}

// runJ7 is journey J7: work safely in production. The connection is marked
// where it can be seen, a write asks before it happens and will not be
// clicked past, and a read-only connection refuses whatever is typed.
func runJ7(t *testing.T, j journey) {
	// A production connection, under a name that has to be typed back.
	prod := j
	prod.conn.Name = "live"
	prod.conn.Environment = "production"
	h := start(t, prod)
	openTable(t, h, prod)

	// Visually distinct: the tab says what it is, in words and not only in
	// colour, so that it survives a screenshot and a colour-blind reader.
	waitFor(t, h.q, "the production band", func() bool {
		return strings.Contains(strings.Join(labelsIn(h.tabs.Selected().Content), " "),
			"Production: statements run here change live data.")
	})

	// A write asks before anything runs.
	h.s.OpenQuery(h.conn.ID)
	ed := find[*view.Editor](h.tabs.Selected().Content)[0]
	stmt := "UPDATE " + j.table + " SET name = 'changed in production' WHERE id = 1"
	test.Type(ed.Focusable(), stmt)
	runWhenReady(t, h.q, h.s, "query.run")

	waitFor(t, h.q, "the question", func() bool { return h.w.Canvas().Overlays().Top() != nil })
	ask := h.w.Canvas().Overlays().Top()
	if said := strings.Join(labelsIn(ask), " "); !strings.Contains(said, "marked Production") ||
		!strings.Contains(said, "Nothing has run yet") {
		t.Errorf("the question says %q", said)
	}

	// And it cannot be clicked past: the connection's name has to be typed.
	run := button(t, ask, "Run")
	if !run.Disabled() {
		t.Fatal("a production write could be confirmed with one click")
	}
	typed := find[*widget.Entry](ask)[0]
	typed.SetText("people")
	if !run.Disabled() {
		t.Error("something that is not the connection's name was accepted")
	}
	typed.SetText(prod.conn.Name)
	if run.Disabled() {
		t.Fatal("the connection was named and Run stayed out of reach")
	}
	test.Tap(run)
	waitFor(t, h.q, "the write to run", func() bool {
		return strings.Contains(strings.Join(labelsIn(h.tabs.Selected().Content), " "), "1 row affected")
	})

	// A read-only connection refuses the same statement, and offers nothing
	// to type: read-only is a decision about the connection, not a question
	// about the statement (NFR-S4).
	ro := j
	ro.conn.Name = "read only"
	ro.conn.ReadOnly = true
	h2 := start(t, ro)
	h2.s.OpenQuery(h2.conn.ID)
	ed2 := find[*view.Editor](h2.tabs.Selected().Content)[0]
	test.Type(ed2.Focusable(), stmt)
	runWhenReady(t, h2.q, h2.s, "query.run")

	waitFor(t, h2.q, "the refusal", func() bool {
		return strings.Contains(strings.Join(labelsIn(h2.tabs.Selected().Content), " "), "read-only")
	})
	if top := h2.w.Canvas().Overlays().Top(); top != nil {
		t.Errorf("a read-only connection offered a way past: %q", strings.Join(labelsIn(top), " "))
	}
}

// selected is what every Select under o currently reads, which is how the
// import's column pairing is seen from outside the shell.
func selected(o fyne.CanvasObject) []string {
	var out []string
	for _, s := range find[*widget.Select](o) {
		out = append(out, s.Selected)
	}
	return out
}

// first is the opening of a file, for an error message.
func first(b []byte) string {
	if len(b) > 40 {
		b = b[:40]
	}
	return string(b)
}

// labelsIn is every label's text under o, which is how a dialog's words are
// read here.
func labelsIn(o fyne.CanvasObject) []string {
	var out []string
	for _, l := range find[*widget.Label](o) {
		out = append(out, l.Text)
	}
	for _, r := range find[*widget.RichText](o) {
		out = append(out, r.String())
	}
	return out
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
