package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/capability"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlcomplete"
)

// treeSource is a server whose tree is written out in full: the nodes each
// ref answers with. It counts the calls, so the cache's laziness can be seen.
type treeSource struct {
	fakeSource
	mu    sync.Mutex
	root  []model.Node
	kids  map[string][]model.Node
	calls map[string]int
	fail  map[string]bool
	block chan struct{} // held shut, a load waits on it
	on    string        // which call waits; every call when empty
	panic string
}

func newTree() *treeSource {
	return &treeSource{kids: map[string][]model.Node{}, calls: map[string]int{}, fail: map[string]bool{}}
}

func (s *treeSource) add(ref model.ObjectRef, nodes ...model.Node) {
	s.kids[strings.Join(ref.Path, "/")+"|"+string(ref.Kind)] = nodes
}

func (s *treeSource) Root(ctx context.Context) ([]model.Node, error) {
	nodes, fail := s.read(ctx, "root", s.root)
	if s.panic == "root" {
		panic("a driver that falls over")
	}
	if fail {
		return nil, errors.New("no")
	}
	return nodes, nil
}

func (s *treeSource) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	key := strings.Join(ref.Path, "/") + "|" + string(ref.Kind)
	nodes, fail := s.read(ctx, key, nil)
	if fail {
		return nil, errors.New("no such class")
	}
	return nodes, nil
}

// read takes what the tree holds now, then waits where the tree is held: a
// load in flight answers with the schema as it was when it was asked, which
// is what makes a stale answer tellable from a fresh one.
func (s *treeSource) read(ctx context.Context, key string, root []model.Node) ([]model.Node, bool) {
	s.mu.Lock()
	s.calls[key]++
	nodes := root
	if nodes == nil {
		nodes = s.kids[key]
	}
	fail, block := s.fail[key], s.block
	if s.on != "" && s.on != key {
		block = nil
	}
	s.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
		}
	}
	return nodes, fail
}

// hold stops every load from answering until release is called; holdOn stops
// only the one call, so the rest of a load runs on.
func (s *treeSource) hold() { s.holdOn("") }

func (s *treeSource) holdOn(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.block, s.on = make(chan struct{}), key
}

func (s *treeSource) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.block)
	s.block, s.on = nil, ""
}

// held waits until the call that is being held has been made.
func (s *treeSource) held(t *testing.T, key string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for s.count(key) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%s was never asked for", key)
		}
		time.Sleep(time.Millisecond)
	}
}

func (s *treeSource) count(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[key]
}

func (s *treeSource) Capabilities() capability.Capabilities {
	return capability.Capabilities{Paradigm: model.ParadigmRelational}
}

// pgShaped is a server with databases, schemas and classes, as PostgreSQL.
func pgShaped() *treeSource {
	s := newTree()
	s.root = []model.Node{
		{Ref: model.NewRef(model.KindDatabase, "shop"), Label: "shop", Attrs: map[string]string{"current": "true"}},
		{Ref: model.NewRef(model.KindDatabase, "other"), Label: "other"},
	}
	shop := model.NewRef(model.KindDatabase, "shop")
	s.add(shop,
		model.Node{Ref: model.NewRef(model.KindSchema, "shop", "public"), Label: "public"},
		model.Node{Ref: model.NewRef(model.KindSchema, "shop", "audit"), Label: "audit"},
	)
	public := model.NewRef(model.KindSchema, "shop", "public")
	s.add(model.ClassRef(public, model.KindTable),
		model.Node{Ref: model.NewRef(model.KindTable, "shop", "public", "orders"), Label: "orders"},
		model.Node{Ref: model.NewRef(model.KindTable, "shop", "public", "order_items"), Label: "order_items"},
	)
	s.add(model.ClassRef(public, model.KindView),
		model.Node{Ref: model.NewRef(model.KindView, "shop", "public", "open_orders"), Label: "open_orders"},
	)
	s.add(model.ClassRef(public, model.KindRoutine),
		model.Node{Ref: model.NewRef(model.KindRoutine, "shop", "public", "order_total"), Label: "order_total"},
	)
	s.add(model.NewRef(model.KindTable, "shop", "public", "orders"),
		model.Node{Ref: model.NewRef(model.KindColumn, "shop", "public", "orders", "id"), Label: "id",
			Attrs: map[string]string{"type": "integer"}},
		model.Node{Ref: model.NewRef(model.KindColumn, "shop", "public", "orders", "placed"), Label: "placed",
			Attrs: map[string]string{"type": "date"}},
	)
	s.add(model.NewRef(model.KindView, "shop", "public", "open_orders"),
		model.Node{Ref: model.NewRef(model.KindColumn, "shop", "public", "open_orders", "id"), Label: "id",
			Attrs: map[string]string{"type": "integer"}},
	)
	audit := model.NewRef(model.KindSchema, "shop", "audit")
	s.add(model.ClassRef(audit, model.KindTable),
		model.Node{Ref: model.NewRef(model.KindTable, "shop", "audit", "changes"), Label: "changes"},
	)
	return s
}

// mysqlShaped is a server whose databases hold classes directly.
func mysqlShaped() *treeSource {
	s := newTree()
	s.root = []model.Node{
		{Ref: model.NewRef(model.KindDatabase, "shop"), Label: "shop", Attrs: map[string]string{"current": "true"}},
		{Ref: model.NewRef(model.KindDatabase, "archive"), Label: "archive"},
	}
	shop := model.NewRef(model.KindDatabase, "shop")
	s.add(shop, model.ClassNode(shop, model.KindTable, 1))
	s.add(model.ClassRef(shop, model.KindTable),
		model.Node{Ref: model.NewRef(model.KindTable, "shop", "orders"), Label: "orders"})
	s.add(model.NewRef(model.KindTable, "shop", "orders"),
		model.Node{Ref: model.NewRef(model.KindColumn, "shop", "orders", "id"), Label: "id",
			Attrs: map[string]string{"type": "int"}})
	archive := model.NewRef(model.KindDatabase, "archive")
	s.add(archive, model.ClassNode(archive, model.KindTable, 1))
	s.add(model.ClassRef(archive, model.KindTable),
		model.Node{Ref: model.NewRef(model.KindTable, "archive", "orders_2019"), Label: "orders_2019"})
	return s
}

// sqliteShaped is a server that lists its classes at the root.
func sqliteShaped() *treeSource {
	s := newTree()
	main := model.NewRef(model.KindDatabase, "main")
	s.root = []model.Node{model.ClassNode(main, model.KindTable, 1)}
	s.add(model.ClassRef(main, model.KindTable),
		model.Node{Ref: model.NewRef(model.KindTable, "main", "notes"), Label: "notes"})
	s.add(model.NewRef(model.KindTable, "main", "notes"),
		model.Node{Ref: model.NewRef(model.KindColumn, "main", "notes", "body"), Label: "body",
			Attrs: map[string]string{"type": "TEXT"}})
	return s
}

// settled asks the cache until a load has landed, as the popup does when the
// cache tells it something arrived.
func settled[T any](t *testing.T, ask func() []T) []T {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := ask(); len(got) > 0 {
			return got
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
}

func objectNames(objs []sqlcomplete.Object) []string {
	out := make([]string, 0, len(objs))
	for _, o := range objs {
		out = append(out, o.Name)
	}
	return out
}

func TestSchemaCacheAnswersFromMemoryAndLoadsBehind(t *testing.T) {
	src := pgShaped()
	c := NewSchemaCache(src)
	defer c.Close()

	// The first ask answers with nothing: the keystroke does not wait.
	if got := c.Objects("shop", "public"); got != nil {
		t.Errorf("first ask returned %v, want nothing while it loads", got)
	}
	got := settled(t, func() []sqlcomplete.Object { return c.Objects("shop", "public") })
	names := objectNames(got)
	if len(names) != 3 {
		t.Fatalf("objects %v, want the two tables and the view", names)
	}
	for _, o := range got {
		if o.Name == "open_orders" && o.Kind != model.KindView {
			t.Errorf("open_orders is a %s, want a view", o.Kind)
		}
	}
	// Asked again, it answers from memory: the server is not asked twice.
	before := src.count("shop/public/table|folder")
	for i := 0; i < 5; i++ {
		c.Objects("shop", "public")
	}
	if after := src.count("shop/public/table|folder"); after != before {
		t.Errorf("%d calls after asking five more times, want %d", after, before)
	}
}

func TestSchemaCacheLoadsColumnsOfOneObject(t *testing.T) {
	src := pgShaped()
	c := NewSchemaCache(src)
	defer c.Close()
	settled(t, func() []sqlcomplete.Object { return c.Objects("shop", "public") })

	cols := settled(t, func() []sqlcomplete.Column { return c.Columns("shop", "public", "orders") })
	if len(cols) != 2 || cols[0].Name != "id" || cols[0].Type != "integer" {
		t.Errorf("columns %+v, want id integer and placed date", cols)
	}
	// A view's columns are asked for as a view's.
	if got := settled(t, func() []sqlcomplete.Column { return c.Columns("shop", "public", "open_orders") }); len(got) != 1 {
		t.Errorf("view columns %+v, want the one", got)
	}
	// Only the tables asked about are read.
	if n := src.count("shop/public/order_items|table"); n != 0 {
		t.Errorf("a table nobody asked about was read %d times", n)
	}
}

func TestSchemaCacheReadsTheShapeOfTheTree(t *testing.T) {
	t.Run("schemas of their own", func(t *testing.T) {
		src := pgShaped()
		c := NewSchemaCache(src)
		defer c.Close()
		if got := settled(t, func() []string { return c.Schemas("shop") }); len(got) != 2 || got[0] != "public" {
			t.Errorf("schemas %v, want public and audit", got)
		}
		// The schema a person is in is the one listed first.
		if got := objectNames(settled(t, func() []sqlcomplete.Object { return c.Objects("shop", "") })); len(got) != 3 {
			t.Errorf("objects of no named schema %v, want the first schema's", got)
		}
		if got := objectNames(settled(t, func() []sqlcomplete.Object { return c.Objects("shop", "audit") })); len(got) != 1 || got[0] != "changes" {
			t.Errorf("objects %v, want the named schema's", got)
		}
		if got := c.Objects("shop", "nowhere"); got != nil {
			t.Errorf("objects of an unknown schema %v, want none", objectNames(got))
		}
		time.Sleep(20 * time.Millisecond)
		if n := src.count("shop/nowhere/table|folder"); n != 0 {
			t.Errorf("a schema the source does not have was read %d times", n)
		}
	})

	t.Run("databases as schemas", func(t *testing.T) {
		c := NewSchemaCache(mysqlShaped())
		defer c.Close()
		// With no schema level, what a name is qualified with is a database.
		if got := settled(t, func() []string { return c.Schemas("shop") }); len(got) != 2 {
			t.Errorf("schemas %v, want the databases", got)
		}
		if got := objectNames(settled(t, func() []sqlcomplete.Object { return c.Objects("shop", "") })); len(got) != 1 || got[0] != "orders" {
			t.Errorf("objects %v, want the database's tables", got)
		}
		if got := objectNames(settled(t, func() []sqlcomplete.Object { return c.Objects("shop", "archive") })); len(got) != 1 || got[0] != "orders_2019" {
			t.Errorf("objects %v, want the named database's tables", got)
		}
		if got := settled(t, func() []sqlcomplete.Column { return c.Columns("shop", "", "orders") }); len(got) != 1 {
			t.Errorf("columns %+v, want the table's", got)
		}
	})

	t.Run("one database, listed as classes", func(t *testing.T) {
		c := NewSchemaCache(sqliteShaped())
		defer c.Close()
		if got := settled(t, func() []string { return c.Databases() }); len(got) != 1 || got[0] != "main" {
			t.Errorf("databases %v, want the one the classes hang from", got)
		}
		// The connection's own setting is a file path, which is no name the
		// source knows: its one database is meant.
		if got := objectNames(settled(t, func() []sqlcomplete.Object {
			return c.Objects("/tmp/notes.db", "")
		})); len(got) != 1 || got[0] != "notes" {
			t.Errorf("objects %v, want the only database's", got)
		}
		if got := settled(t, func() []sqlcomplete.Column {
			return c.Columns("/tmp/notes.db", "", "notes")
		}); len(got) != 1 || got[0].Type != "TEXT" {
			t.Errorf("columns %+v, want the table's with its type", got)
		}
	})
}

func TestSchemaCacheOffersRoutines(t *testing.T) {
	c := NewSchemaCache(pgShaped())
	defer c.Close()
	if got := settled(t, func() []string { return c.Routines("shop", "public") }); len(got) != 1 || got[0] != "order_total" {
		t.Errorf("routines %v, want the schema's", got)
	}
}

func TestSchemaCacheTellsWhatHasLoaded(t *testing.T) {
	c := NewSchemaCache(pgShaped())
	defer c.Close()
	var mu sync.Mutex
	loads := 0
	cancel := c.Subscribe(func() {
		mu.Lock()
		loads++
		mu.Unlock()
	})
	settled(t, func() []sqlcomplete.Object { return c.Objects("shop", "public") })
	mu.Lock()
	got := loads
	mu.Unlock()
	if got == 0 {
		t.Error("nothing was said about the loads that landed")
	}
	// Unsubscribed, it hears no more.
	cancel()
	mu.Lock()
	loads = 0
	mu.Unlock()
	settled(t, func() []sqlcomplete.Column { return c.Columns("shop", "public", "orders") })
	mu.Lock()
	defer mu.Unlock()
	if loads != 0 {
		t.Errorf("%d loads reported after unsubscribing", loads)
	}
}

func TestSchemaCacheIsEmptiedByInvalidate(t *testing.T) {
	src := pgShaped()
	c := NewSchemaCache(src)
	defer c.Close()
	settled(t, func() []sqlcomplete.Object { return c.Objects("shop", "public") })
	before := src.count("shop/public/table|folder")

	c.Invalidate()
	if got := c.Objects("shop", "public"); got != nil {
		t.Errorf("objects %v straight after emptying, want none", objectNames(got))
	}
	if got := c.Databases(); got != nil {
		t.Errorf("databases %v straight after emptying, want none", got)
	}
	settled(t, func() []sqlcomplete.Object { return c.Objects("shop", "public") })
	if after := src.count("shop/public/table|folder"); after != before+1 {
		t.Errorf("%d calls after emptying, want one more than %d", after, before)
	}
}

func TestSchemaCacheAsksOnceWhileALoadIsInFlight(t *testing.T) {
	src := pgShaped()
	src.hold()
	c := NewSchemaCache(src)
	defer c.Close()
	for i := 0; i < 20; i++ {
		c.Objects("shop", "public") // twenty keystrokes, one load
	}
	src.release()
	settled(t, func() []sqlcomplete.Object { return c.Objects("shop", "public") })
	if n := src.count("root"); n != 1 {
		t.Errorf("the databases were read %d times, want once", n)
	}
}

func TestSchemaCacheDropsALoadEmptiedUnderIt(t *testing.T) {
	src := pgShaped()
	c := NewSchemaCache(src)
	defer c.Close()
	settled(t, func() []string { return c.Schemas("shop") })

	const class = "shop/public/table|folder"
	src.holdOn(class)
	c.Objects("shop", "public") // the load starts, and is held
	src.held(t, class)          // it has read the schema as it is now
	// The schema changes under it.
	src.mu.Lock()
	src.kids[class] = append(src.kids[class],
		model.Node{Ref: model.NewRef(model.KindTable, "shop", "public", "refunds"), Label: "refunds"})
	src.mu.Unlock()
	c.Invalidate()
	src.release()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if names := objectNames(c.Objects("shop", "public")); has(names, "refunds") {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("objects %v, want the schema as it is now: the load in flight read it as it was",
		objectNames(c.Objects("shop", "public")))
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func TestSchemaCacheSurvivesAServerThatWillNotAnswer(t *testing.T) {
	src := pgShaped()
	src.fail["root"] = true
	c := NewSchemaCache(src)
	defer c.Close()
	for i := 0; i < 50; i++ {
		if got := c.Objects("shop", "public"); got != nil {
			t.Fatalf("objects %v from a server that failed", objectNames(got))
		}
		time.Sleep(time.Millisecond)
	}
	// It asked once, not once a keystroke.
	if n := src.count("root"); n != 1 {
		t.Errorf("the root was read %d times after failing, want once", n)
	}
	// A class the source has no notion of leaves the others alone.
	src2 := pgShaped()
	src2.fail["shop/public/view|folder"] = true
	c2 := NewSchemaCache(src2)
	defer c2.Close()
	if got := objectNames(settled(t, func() []sqlcomplete.Object { return c2.Objects("shop", "public") })); len(got) != 2 {
		t.Errorf("objects %v, want the tables though views could not be listed", got)
	}
}

func TestSchemaCacheSurvivesADriverThatPanics(t *testing.T) {
	src := pgShaped()
	src.panic = "root"
	c := NewSchemaCache(src)
	defer c.Close()
	for i := 0; i < 20; i++ {
		c.Databases()
		time.Sleep(time.Millisecond)
	}
	if got := c.Databases(); got != nil {
		t.Errorf("databases %v from a driver that fell over", got)
	}
}

func TestSchemaCacheIsOneCachePerConnection(t *testing.T) {
	l := startLive("c1", &fakeSource{}, MonitorConfig{Interval: time.Hour}, nil)
	defer l.Close()
	if a, b := l.Schema(), l.Schema(); a != b {
		t.Error("a connection handed out two caches")
	}
}

var _ source.Source = (*treeSource)(nil)
