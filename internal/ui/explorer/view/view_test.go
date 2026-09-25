package view

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// treeDriver serves a small schema: db → two schemas whose names collide when
// joined with dots → tables and columns.
type treeDriver struct{}

var dials atomic.Int64

func init() { source.Register(treeDriver{}) }

func (treeDriver) Describe() source.Descriptor {
	return source.Descriptor{ID: "treefake", Name: "Tree Fake", Paradigm: model.ParadigmRelational}
}

func (treeDriver) Open(_ context.Context, cfg source.ConnectionConfig) (source.Source, error) {
	dials.Add(1)
	if cfg.Host == "down" {
		return nil, &source.ConnectError{Kind: source.ConnectUnreachable, Hint: "the server could not be reached",
			Err: errors.New("dial tcp: refused")}
	}
	return &treeSource{}, nil
}

type treeSource struct{}

func node(k model.ObjectKind, label string, children, browsable bool, path ...string) model.Node {
	return model.Node{Ref: model.NewRef(k, path...), Label: label, HasChildren: children, Browsable: browsable}
}

func (treeSource) Root(context.Context) ([]model.Node, error) {
	return []model.Node{node(model.KindDatabase, "sales", true, false, "sales")}, nil
}

func (treeSource) Children(_ context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindDatabase:
		return []model.Node{
			node(model.KindSchema, "a.b", true, false, "sales", "a.b"),
			node(model.KindSchema, "a", true, false, "sales", "a"),
		}, nil
	case model.KindSchema:
		if ref.Name() == "a.b" {
			return []model.Node{node(model.KindTable, "c", true, true, "sales", "a.b", "c")}, nil
		}
		t := node(model.KindTable, "b.c", true, true, "sales", "a", "b.c")
		t.Badge = &model.Badge{Text: "1.2M", Exact: false}
		return []model.Node{t}, nil
	case model.KindTable:
		col := node(model.KindColumn, "id", false, false, append(ref.Path, "id")...)
		col.Attrs = map[string]string{"type": "bigint"}
		return []model.Node{col}, nil
	}
	return nil, nil
}

func (treeSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{Paradigm: model.ParadigmRelational}
}
func (treeSource) Info(context.Context) (source.ServerInfo, error) { return source.ServerInfo{}, nil }
func (treeSource) Ping(context.Context) error                      { return nil }
func (treeSource) Close() error                                    { return nil }
func (treeSource) Describe(context.Context, model.ObjectRef) (any, error) {
	return nil, nil
}

// Badge answers for table c; the rest have none. badgeCalls counts reads,
// and slowBadges makes them wait until cancelled, counting those.
var (
	badgeCalls, badgeCancels, inFlight, peakInFlight atomic.Int64
	slowBadges, panicBadges                          atomic.Bool
)

func (treeSource) Badge(ctx context.Context, ref model.ObjectRef) (model.Badge, bool, error) {
	badgeCalls.Add(1)
	if panicBadges.Load() {
		panic("treefake: badge fell over")
	}
	if slowBadges.Load() {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for p := peakInFlight.Load(); n > p && !peakInFlight.CompareAndSwap(p, n); p = peakInFlight.Load() {
		}
		<-ctx.Done()
		badgeCancels.Add(1)
		return model.Badge{}, false, ctx.Err()
	}
	if ref.Kind == model.KindTable && ref.Name() == "c" {
		return model.Badge{Text: "7", Exact: true}, true, nil
	}
	return model.Badge{}, false, nil
}
func (treeSource) Browse(context.Context, model.ObjectRef, source.BrowseOptions) (model.RowStream, error) {
	return nil, errors.New("not in this test")
}

func setup(t *testing.T, hosts ...string) (*Loader, []store.SavedConnection) {
	t.Helper()
	sf, _, err := store.OpenSettings(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	conns := app.NewConnections(sf, app.NewVault(secrets.NewMemory(), nil), nil)
	var saved []store.SavedConnection
	for i, h := range hosts {
		env := []string{"production", "dev"}[i%2]
		c, err := conns.Create(store.SavedConnection{Name: h, Driver: "treefake", Host: h, Environment: env}, nil)
		if err != nil {
			t.Fatal(err)
		}
		saved = append(saved, c)
	}
	ws := app.NewWorkspace(conns, app.MonitorConfig{Interval: time.Hour})
	t.Cleanup(func() { ws.CloseAll() })
	return &Loader{Conns: conns, WS: ws}, saved
}

func load(t *testing.T, l *Loader, parent explorer.Item) []explorer.Item {
	t.Helper()
	items, err := l.Load(context.Background(), parent)
	if err != nil {
		t.Fatalf("Load(%s): %v", debugID(parent.ID), err)
	}
	return items
}

func TestLoaderWalksConnectionToColumns(t *testing.T) {
	l, saved := setup(t, "primary")
	roots := load(t, l, explorer.Item{})
	if len(roots) != 1 || roots[0].Label != "primary" || roots[0].ID != ConnectionID(saved[0].ID) {
		t.Fatalf("root = %+v", roots)
	}
	dbs := load(t, l, roots[0])
	if len(dbs) != 1 || dbs[0].Label != "sales" {
		t.Fatalf("databases = %+v", dbs)
	}
	schemas := load(t, l, dbs[0])
	tables := load(t, l, schemas[1]) // schema "a"
	cols := load(t, l, tables[0])
	if len(cols) != 1 || cols[0].Label != "id" {
		t.Errorf("columns = %+v", cols)
	}
}

func TestNodeIDsAreUnambiguousForDottedNames(t *testing.T) {
	// Joined with dots, both of these would be "table:sales.a.b.c".
	l, _ := setup(t, "primary")
	roots := load(t, l, explorer.Item{})
	schemas := load(t, l, load(t, l, roots[0])[0])
	t1 := load(t, l, schemas[0])[0] // a.b / c
	t2 := load(t, l, schemas[1])[0] // a / b.c
	if t1.ID == t2.ID {
		t.Fatalf("two different tables share an ID: %s", debugID(t1.ID))
	}
}

func TestExpandingAgainDoesNotRedial(t *testing.T) {
	l, _ := setup(t, "primary")
	roots := load(t, l, explorer.Item{})
	before := dials.Load()
	for i := 0; i < 5; i++ {
		load(t, l, roots[0])
	}
	if n := dials.Load() - before; n != 1 {
		t.Errorf("five expansions dialled %d times; the workspace should share one connection", n)
	}
}

func TestUnreachableConnectionBecomesAnErrorRow(t *testing.T) {
	newApp(t)
	l, saved := setup(t, "down")
	e := New(l, (&uithread.Queue{}).Run, 0)
	root := e.Model.Children(explorer.RootID)
	waitReal(t, e.Model, explorer.RootID)
	root = e.Model.Children(explorer.RootID)
	e.Model.Children(root[0])
	kids := waitReal(t, e.Model, ConnectionID(saved[0].ID))
	it, st, err := e.Model.Item(kids[0])
	if st != explorer.Failed || err == nil || it.Label == "" {
		t.Errorf("want an error row explaining the failure, got %+v %v %v", it, st, err)
	}
	var ce *source.ConnectError
	if !errors.As(err, &ce) || ce.Kind != source.ConnectUnreachable {
		t.Errorf("the row should carry the classified connect error, got %v", err)
	}
}

func waitReal(t *testing.T, m *explorer.Model, id string) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		kids := m.Children(id)
		if _, st, _ := m.Item(id); st == explorer.Failed || (len(kids) > 0 && !explorer.IsPlaceholder(kids[0])) {
			return kids
		}
		time.Sleep(3 * time.Millisecond)
	}
	t.Fatalf("%s never loaded", debugID(id))
	return nil
}

func TestActivateOpensOnlyBrowsableNodes(t *testing.T) {
	newApp(t)
	l, saved := setup(t, "primary")
	e := New(l, (&uithread.Queue{}).Run, 0)
	var opened []string
	e.OnOpen = func(connID string, n model.Node) { opened = append(opened, connID+"/"+n.Label) }

	e.Model.Children(explorer.RootID)
	root := waitReal(t, e.Model, explorer.RootID)
	e.Model.Children(root[0])
	dbs := waitReal(t, e.Model, root[0])
	e.Model.Children(dbs[0])
	schemas := waitReal(t, e.Model, dbs[0])
	e.Model.Children(schemas[1])
	tables := waitReal(t, e.Model, schemas[1])

	e.activate(dbs[0]) // a database is not browsable
	e.activate(tables[0])
	if len(opened) != 1 || opened[0] != saved[0].ID+"/b.c" {
		t.Errorf("opened %v", opened)
	}
}

func TestRowsShowIconsBadgesAndEnvironmentInWords(t *testing.T) {
	newApp(t)
	l, _ := setup(t, "prod-db")
	e := New(l, (&uithread.Queue{}).Run, 0)
	e.Model.Children(explorer.RootID)
	root := waitReal(t, e.Model, explorer.RootID)

	r := newNodeRow()
	e.update(root[0], r)
	if r.label.Text != "prod-db" || r.badge.Text != "PROD" || !r.badge.TextStyle.Bold {
		t.Errorf("production connection row: label %q badge %q bold %v",
			r.label.Text, r.badge.Text, r.badge.TextStyle.Bold)
	}

	e.Model.Children(root[0])
	dbs := waitReal(t, e.Model, root[0])
	e.Model.Children(dbs[0])
	schemas := waitReal(t, e.Model, dbs[0])
	e.Model.Children(schemas[1])
	tables := waitReal(t, e.Model, schemas[1])
	e.update(tables[0], r)
	if r.badge.Text != "~1.2M" {
		t.Errorf("an estimated count must be marked as one: %q", r.badge.Text)
	}
	if r.icon.Resource == nil {
		t.Error("table row has no icon")
	}
	e.Model.Children(tables[0])
	cols := waitReal(t, e.Model, tables[0])
	e.update(cols[0], r)
	if r.badge.Text != "bigint" {
		t.Errorf("a column should show its type: %q", r.badge.Text)
	}
}

func TestPlaceholderRowsLookDifferent(t *testing.T) {
	newApp(t)
	r := newNodeRow()
	r.placeholder("Loading…", false)
	if !r.label.TextStyle.Italic || r.icon.Resource != nil {
		t.Error("a loading row should be italic with no icon")
	}
	r.placeholder("permission denied", true)
	if r.label.TextStyle.Italic {
		t.Error("an error row should not look like it is still loading")
	}
}

// newApp installs the application's own theme: Fyne's test app uses Fyne's
// default, which knows none of the custom object icons.
func newApp(t *testing.T) {
	t.Helper()
	a := test.NewTempApp(t)
	a.Settings().SetTheme(uitheme.New())
}

func TestRefreshesAreCoalescedOntoTheUIGoroutine(t *testing.T) {
	newApp(t)
	l, _ := setup(t, "primary")
	q := &uithread.Queue{}
	e := New(l, q.Run, 0)
	for i := 0; i < 50; i++ {
		e.Model.OnChange("x")
	}
	if q.Len() != 1 {
		t.Fatalf("50 changes queued %d refreshes, want 1", q.Len())
	}
	q.Flush() // runs the tree refresh here, on the test goroutine
	e.Model.OnChange("x")
	if q.Len() != 1 {
		t.Error("a change after the refresh ran must queue another")
	}
}

func TestConnectionOf(t *testing.T) {
	ref := model.NewRef(model.KindTable, "sales", "public", "orders")
	for id, want := range map[string]string{
		ConnectionID("abc"):                "abc",
		NodeID("abc", ref):                 "abc",
		NodeID("abc", ref) + "\x00loading": "abc",
		ConnectionID("abc") + "\x00error":  "abc",
	} {
		if got, ok := ConnectionOf(id); !ok || got != want {
			t.Errorf("ConnectionOf(%s) = %q %v, want %q", debugID(id), got, ok, want)
		}
	}
	if _, ok := ConnectionOf(""); ok {
		t.Error("the root belongs to no connection")
	}
}

// filtered builds an explorer over one connection with sales, both its
// schemas and schema a's table loaded.
func filtered(t *testing.T, hosts ...string) (*Explorer, *uithread.Queue, []string) {
	t.Helper()
	newApp(t)
	l, saved := setup(t, hosts...)
	q := &uithread.Queue{}
	e := New(l, q.Run, 0)
	e.Model.Children(explorer.RootID)
	waitReal(t, e.Model, explorer.RootID)
	conn := ConnectionID(saved[0].ID)
	e.Model.Children(conn)
	dbs := waitReal(t, e.Model, conn)
	e.Model.Children(dbs[0])
	schemas := waitReal(t, e.Model, dbs[0])
	e.Model.Children(schemas[1])
	waitReal(t, e.Model, schemas[1])
	q.Flush()
	return e, q, []string{conn, dbs[0], schemas[0], schemas[1]}
}

func TestTheFilterListsMatchingPathsInPlaceOfTheTree(t *testing.T) {
	e, q, _ := filtered(t, "primary")
	test.Type(e.Filter, "a b.c")
	q.Flush()
	if e.body.Objects[0] == e.Tree || len(e.hits) == 0 {
		t.Fatal("the matches should be listed in the tree's place")
	}
	if got := e.hits[0].Path; len(got) != 4 || got[3] != "b.c" || got[2] != "a" {
		t.Errorf("best match %v, want primary / sales / a / b.c", got)
	}
	var opened model.Node
	e.OnOpen = func(_ string, n model.Node) { opened = n }
	e.results.Select(0)
	if opened.Label != "b.c" {
		t.Errorf("choosing a table should open it, opened %q", opened.Label)
	}
}

func TestChoosingAFolderShowsItInTheTree(t *testing.T) {
	e, q, ids := filtered(t, "primary")
	e.Filter.SetText("a.b")
	q.Flush()
	at := -1
	for i, h := range e.hits {
		if h.ID == ids[2] {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("schema a.b not among %v", e.hits)
	}
	e.results.Select(at)
	if e.Filter.Text != "" || e.body.Objects[0] != e.Tree {
		t.Error("choosing a schema should clear the filter and bring the tree back")
	}
	if !e.Tree.IsBranchOpen(ids[0]) || !e.Tree.IsBranchOpen(ids[1]) || e.Selected() != ids[2] {
		t.Errorf("the schema should be selected with its connection and database open; selected %s", debugID(e.Selected()))
	}
}

func TestEscapeClearsTheFilter(t *testing.T) {
	e, q, _ := filtered(t, "primary")
	e.Filter.SetText("sales")
	q.Flush()
	e.Filter.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEscape})
	q.Flush()
	if e.Filter.Text != "" || e.body.Objects[0] != e.Tree {
		t.Error("Escape should clear the filter and bring the tree back")
	}
}

func TestTheFilterSaysWhatItDidNotSearch(t *testing.T) {
	e, q, _ := filtered(t, "primary", "secondary")
	e.Filter.SetText("sales")
	q.Flush()
	if want := "Not searched: 1 connection not opened."; !strings.Contains(e.note.Text, want) {
		t.Errorf("note %q, want it to say %q", e.note.Text, want)
	}
	e.Filter.SetText("zzz")
	q.Flush()
	if !strings.HasPrefix(e.note.Text, "No loaded objects match") {
		t.Errorf("note %q", e.note.Text)
	}
}

func TestMatchesFollowTheTreeAsItLoads(t *testing.T) {
	e, q, ids := filtered(t, "primary")
	e.Filter.SetText("a.b / c")
	q.Flush()
	if len(e.hits) != 0 {
		t.Fatalf("table c is not loaded yet, but the filter found %v", e.hits)
	}
	e.Model.Children(ids[2]) // schema a.b's table loads while the filter is on
	waitReal(t, e.Model, ids[2])
	q.Flush()
	if len(e.hits) == 0 || e.hits[0].Path[len(e.hits[0].Path)-1] != "c" {
		t.Errorf("the matches should follow the tree as it loads: %v", e.hits)
	}
}

func TestEnterOpensTheBestMatch(t *testing.T) {
	e, q, _ := filtered(t, "primary")
	var opened model.Node
	e.OnOpen = func(_ string, n model.Node) { opened = n }
	test.Type(e.Filter, "a b.c")
	q.Flush()
	e.Filter.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	if opened.Label != "b.c" {
		t.Errorf("Enter should open the best match, opened %q", opened.Label)
	}
}

func TestARightClickSelectsTheNodeAndAsksForItsMenu(t *testing.T) {
	newApp(t)
	l, saved := setup(t, "primary")
	e := New(l, (&uithread.Queue{}).Run, 0)
	e.Model.Children(explorer.RootID)
	waitReal(t, e.Model, explorer.RootID)
	conn := ConnectionID(saved[0].ID)
	var asked string
	e.OnMenu = func(id string, _ fyne.Position) { asked = id }
	r := newNodeRow()
	e.update(conn, r)
	r.TappedSecondary(&fyne.PointEvent{})
	if asked != conn || e.Selected() != conn {
		t.Errorf("asked for %s with %s selected; a right-click should select the node, then ask for its menu",
			debugID(asked), debugID(e.Selected()))
	}
	asked = ""
	loading := e.Model.Children(conn)[0] // the connection has not loaded: a loading row
	e.update(loading, r)
	r.TappedSecondary(&fyne.PointEvent{})
	if asked != "" {
		t.Error("a loading row has no menu")
	}
}

// badgeTree is an explorer over one connection with schema a.b's table c
// loaded, and the badge counters reset.
func badgeTree(t *testing.T) (*Explorer, *uithread.Queue, []string) {
	t.Helper()
	badgeCalls.Store(0)
	badgeCancels.Store(0)
	inFlight.Store(0)
	peakInFlight.Store(0)
	slowBadges.Store(false)
	panicBadges.Store(false)
	t.Cleanup(func() { slowBadges.Store(false); panicBadges.Store(false) })
	e, q, ids := filtered(t, "primary")
	e.Model.Children(ids[2])
	c := waitReal(t, e.Model, ids[2])[0] // schema a.b's table c
	return e, q, append(ids, c)
}

func drawn(e *Explorer, id string) string {
	r := newNodeRow()
	e.update(id, r)
	return r.badge.Text
}

func waitFor(t *testing.T, q *uithread.Queue, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never held")
		}
		if q.Flush() == 0 {
			time.Sleep(2 * time.Millisecond)
		}
	}
}

func TestABadgeIsReadWhenItsRowIsDrawn(t *testing.T) {
	e, q, ids := badgeTree(t)
	c := ids[4]
	if got := drawn(e, c); got != "" {
		t.Errorf("the first draw should not wait for the badge, drew %q", got)
	}
	waitFor(t, q, func() bool { return drawn(e, c) == "7" })
	calls := badgeCalls.Load()
	drawn(e, c)
	if badgeCalls.Load() != calls {
		t.Error("a badge read once should not be read again on every redraw")
	}
}

func TestANodesOwnBadgeIsNotReadAgain(t *testing.T) {
	e, _, ids := badgeTree(t)
	e.Model.Children(ids[3])
	bc := waitReal(t, e.Model, ids[3])[0] // table b.c, which came with ~1.2M
	if got := drawn(e, bc); got != "~1.2M" || badgeCalls.Load() != 0 {
		t.Errorf("drew %q after %d reads; a node's own badge needs no read", got, badgeCalls.Load())
	}
}

func TestNoBadgeIsAskedOnce(t *testing.T) {
	e, q, ids := badgeTree(t)
	drawn(e, ids[2]) // schema a.b: the fake has no badge for a schema
	waitFor(t, q, func() bool { _, ok := e.badges[ids[2]]; return ok })
	calls := badgeCalls.Load()
	drawn(e, ids[2])
	if badgeCalls.Load() != calls {
		t.Error("a node with no badge should not be asked again on every redraw")
	}
}

func TestClosingABranchCancelsItsBadges(t *testing.T) {
	e, q, ids := badgeTree(t)
	slowBadges.Store(true)
	drawn(e, ids[4])
	waitFor(t, q, func() bool { return inFlight.Load() == 1 })
	e.Tree.OpenBranch(ids[2])
	e.Tree.CloseBranch(ids[2]) // schema a.b, table c's parent
	waitFor(t, q, func() bool { return badgeCancels.Load() == 1 })
	if _, ok := e.badges[ids[4]]; ok {
		t.Error("a cancelled read should be forgotten, to be read again when its row is drawn")
	}
}

func TestAtMostFourBadgesAreReadAtOnce(t *testing.T) {
	e, q, ids := badgeTree(t)
	slowBadges.Store(true)
	it, _, _ := e.Model.Item(ids[4])
	d := it.Data.(objItem)
	for i := range 6 {
		e.fetchBadge(fmt.Sprintf("%s#%d", ids[4], i), d)
	}
	waitFor(t, q, func() bool { return inFlight.Load() == badgeWorkers })
	time.Sleep(20 * time.Millisecond)
	if p := peakInFlight.Load(); p != badgeWorkers {
		t.Errorf("%d badges were read at once, want at most %d", p, badgeWorkers)
	}
	e.Refresh(explorer.RootID)
	waitFor(t, q, func() bool { return len(e.waiting) == 0 && inFlight.Load() == 0 })
}

func TestABadgeThatPanicsIsNoBadge(t *testing.T) {
	e, q, ids := badgeTree(t)
	panicBadges.Store(true)
	drawn(e, ids[4])
	waitFor(t, q, func() bool { _, ok := e.badges[ids[4]]; return ok })
	if got := drawn(e, ids[4]); got != "" {
		t.Errorf("a driver that panicked drew %q", got)
	}
}

// A workspace narrows the tree to the connections it is over (FR-15.9).
func TestAWorkspaceNarrowsTheTree(t *testing.T) {
	l, saved := setup(t, "primary", "reporting")
	l.Shows = func(id string) bool { return id == saved[0].ID }
	roots := load(t, l, explorer.Item{})
	if len(roots) != 1 || roots[0].Label != "primary" {
		t.Fatalf("the tree shows %+v", labelsOf(roots))
	}
	// And with no workspace, both are there again.
	l.Shows = nil
	if roots := load(t, l, explorer.Item{}); len(roots) != 2 {
		t.Errorf("without a workspace the tree shows %+v", labelsOf(roots))
	}
}

// It narrows what a folder holds, and drops a folder it has emptied: an
// empty folder would open onto nothing.
func TestAWorkspaceNarrowsFoldersToo(t *testing.T) {
	l, saved := setup(t, "primary", "reporting", "archive")
	billing, err := l.Conns.CreateFolder("Billing", "")
	if err != nil {
		t.Fatal(err)
	}
	reports, err := l.Conns.CreateFolder("Reports", "")
	if err != nil {
		t.Fatal(err)
	}
	// Two in one folder, so that narrowing has something to narrow, and a
	// third in another, so that emptying has something to empty.
	for _, in := range []struct{ conn, folder string }{
		{saved[0].ID, billing.ID}, {saved[1].ID, billing.ID}, {saved[2].ID, reports.ID},
	} {
		if err := l.Conns.SetFolder(in.conn, in.folder); err != nil {
			t.Fatal(err)
		}
	}
	l.Shows = func(id string) bool { return id == saved[0].ID }
	roots := load(t, l, explorer.Item{})
	if len(roots) != 1 || roots[0].Label != "Billing" {
		t.Fatalf("the tree shows %+v", labelsOf(roots))
	}
	if in := load(t, l, roots[0]); len(in) != 1 || in[0].Label != "primary" {
		t.Errorf("the folder holds %+v", labelsOf(in))
	}
}

// A folder nobody has filled yet stays in a tree with no workspace: it is
// a shelf somebody just made.
func TestAnEmptyFolderStaysWithoutAWorkspace(t *testing.T) {
	l, _ := setup(t)
	if _, err := l.Conns.CreateFolder("Billing", ""); err != nil {
		t.Fatal(err)
	}
	roots := load(t, l, explorer.Item{})
	if len(roots) != 1 || roots[0].Label != "Billing" || roots[0].HasChildren {
		t.Fatalf("the tree shows %+v", roots)
	}
}

func labelsOf(items []explorer.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Label)
	}
	return out
}
