package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
	"github.com/ikigai-db/ikigai-db/internal/source/sqlcomplete"
)

// defaultSchemaTimeout bounds one background load. A server that has stopped
// answering must not leave completion waiting on it for ever.
const defaultSchemaTimeout = 20 * time.Second

// SchemaCache is what completion knows about a connection's objects: its
// databases, schemas, tables, views, columns and routines (FR-5.2).
//
// It implements sqlcomplete.Catalog, which answers from memory and never
// blocks: completion runs on the keystroke path. What it does not hold yet it
// fetches in the background, through the same lazy introspection the explorer
// uses, and tells its subscribers when the answer lands so that the popup can
// ask again.
//
// One cache belongs to one connection and is shared by every tab on it.
type SchemaCache struct {
	src     source.Source
	timeout time.Duration
	ctx     context.Context
	cancel  context.CancelFunc

	mu sync.Mutex

	// gen rises with every Invalidate. A load carrying an older one is
	// dropped when it lands: it read the schema as it was before the change.
	gen int

	rootLoaded bool
	databases  []string
	current    string // the database the source says the connection is in

	// nested records whether a database holds schemas of its own, which is
	// what tells PostgreSQL's shape from MySQL's and SQLite's.
	nested        map[string]bool
	schemas       map[string][]string
	schemasLoaded map[string]bool

	// A key present in one of these maps has been loaded, whether or not it
	// holds anything: a schema with no tables must not be asked for again on
	// every keystroke.
	objects  map[string][]sqlcomplete.Object
	columns  map[string][]sqlcomplete.Column
	routines map[string][]string
	loading  map[string]bool

	subs    map[int]func()
	nextSub int
}

// NewSchemaCache builds a cache over a connection.
func NewSchemaCache(src source.Source) *SchemaCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &SchemaCache{
		src: src, timeout: defaultSchemaTimeout, ctx: ctx, cancel: cancel,
		nested: map[string]bool{}, schemas: map[string][]string{}, schemasLoaded: map[string]bool{},
		objects: map[string][]sqlcomplete.Object{}, columns: map[string][]sqlcomplete.Column{},
		routines: map[string][]string{}, loading: map[string]bool{},
		subs: map[int]func(){},
	}
}

// Subscribe registers a function called, off the UI goroutine, whenever a
// load has added something. The returned function stops it.
func (c *SchemaCache) Subscribe(fn func()) (cancel func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextSub
	c.nextSub++
	c.subs[id] = fn
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.subs, id)
	}
}

// Invalidate empties the cache: DDL has run, or the explorer was refreshed,
// so every name in it may have changed. Loads already in flight are dropped
// when they land.
func (c *SchemaCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.rootLoaded, c.databases, c.current = false, nil, ""
	c.nested = map[string]bool{}
	c.schemas = map[string][]string{}
	c.schemasLoaded = map[string]bool{}
	c.objects = map[string][]sqlcomplete.Object{}
	c.columns = map[string][]sqlcomplete.Column{}
	c.routines = map[string][]string{}
	c.loading = map[string]bool{}
}

// Close stops the loads in flight. The cache still answers from what it has.
func (c *SchemaCache) Close() { c.cancel() }

// Databases implements sqlcomplete.Catalog.
func (c *SchemaCache) Databases() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wantRoot()
	return c.databases
}

// Schemas implements sqlcomplete.Catalog. Where a source has no schema level
// of its own — MySQL, SQLite — its databases are what a name is qualified
// with, so they are what is offered.
func (c *SchemaCache) Schemas(database string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	db, ok := c.resolve(database)
	if !ok || !c.wantSchemas(db) {
		return nil
	}
	if c.nested[db] {
		return c.schemas[db]
	}
	return c.databases
}

// Objects implements sqlcomplete.Catalog.
func (c *SchemaCache) Objects(database, schema string) []sqlcomplete.Object {
	c.mu.Lock()
	defer c.mu.Unlock()
	ref, ok := c.container(database, schema)
	if !ok {
		return nil
	}
	key := "objects\x00" + refKey(ref)
	objs, ok := c.objects[key]
	if !ok {
		c.loadObjects(key, ref)
	}
	return objs
}

// Columns implements sqlcomplete.Catalog.
func (c *SchemaCache) Columns(database, schema, object string) []sqlcomplete.Column {
	c.mu.Lock()
	defer c.mu.Unlock()
	ref, ok := c.container(database, schema)
	if !ok {
		return nil
	}
	obj := model.NewRef(c.kindOf(ref, object), append(append([]string{}, ref.Path...), object)...)
	key := "columns\x00" + refKey(obj)
	cols, ok := c.columns[key]
	if !ok {
		c.loadColumns(key, obj)
	}
	return cols
}

// Routines implements sqlcomplete.Catalog.
func (c *SchemaCache) Routines(database, schema string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	ref, ok := c.container(database, schema)
	if !ok {
		return nil
	}
	key := "routines\x00" + refKey(ref)
	names, ok := c.routines[key]
	if !ok {
		c.loadRoutines(key, ref)
	}
	return names
}

// resolve reads the database a completion request names. A request carries
// the connection's own setting, which is not always a name the source knows —
// SQLite's is a file path — so an unknown one means the database the
// connection is in. Called under the lock.
func (c *SchemaCache) resolve(database string) (string, bool) {
	if !c.wantRoot() {
		return "", false
	}
	for _, db := range c.databases {
		if strings.EqualFold(db, database) {
			return db, true
		}
	}
	if c.current != "" {
		return c.current, true
	}
	if len(c.databases) == 1 {
		return c.databases[0], true
	}
	return "", false
}

// container is the node whose classes hold a schema's tables: the schema
// itself where the source has schemas, else the database. Called under the
// lock.
func (c *SchemaCache) container(database, schema string) (model.ObjectRef, bool) {
	db, ok := c.resolve(database)
	if !ok || !c.wantSchemas(db) {
		return model.ObjectRef{}, false
	}
	if !c.nested[db] {
		// The database is the schema. A qualifier naming another one is
		// another database; one naming nothing known is nobody's table.
		if schema == "" || strings.EqualFold(schema, db) {
			return model.NewRef(model.KindDatabase, db), true
		}
		other, ok := c.resolve(schema)
		if !ok || !strings.EqualFold(other, schema) {
			return model.ObjectRef{}, false
		}
		return model.NewRef(model.KindDatabase, other), true
	}
	if schema == "" {
		// The schema a person works in is the one the source lists first:
		// PostgreSQL sorts public ahead of the rest for exactly this reason.
		if len(c.schemas[db]) == 0 {
			return model.ObjectRef{}, false
		}
		return model.NewRef(model.KindSchema, db, c.schemas[db][0]), true
	}
	for _, s := range c.schemas[db] {
		if strings.EqualFold(s, schema) {
			return model.NewRef(model.KindSchema, db, s), true
		}
	}
	return model.ObjectRef{}, false
}

// kindOf is what the cache knows an object to be, so that a view's columns
// are asked for as a view's. Called under the lock.
func (c *SchemaCache) kindOf(container model.ObjectRef, name string) model.ObjectKind {
	for _, o := range c.objects["objects\x00"+refKey(container)] {
		if strings.EqualFold(o.Name, name) {
			return o.Kind
		}
	}
	return model.KindTable
}

// wantRoot reports whether the databases are known, starting the load that
// finds them out where they are not. Called under the lock.
func (c *SchemaCache) wantRoot() bool {
	if c.rootLoaded {
		return true
	}
	c.begin("root", func(ctx context.Context) func() {
		nodes, err := rootNodes(ctx, c.src)
		if err != nil {
			return func() { c.rootLoaded = true } // asked and answered badly; ask again on Invalidate
		}
		var dbs []string
		current, flat := "", ""
		for _, n := range nodes {
			switch n.Ref.Kind {
			case model.KindDatabase:
				dbs = append(dbs, n.Ref.Name())
				if n.Attrs["current"] == "true" {
					current = n.Ref.Name()
				}
			case model.KindFolder:
				// A source with one database lists its classes at the root
				// (SQLite). The class's own path names the database.
				if len(n.Ref.Path) >= 2 {
					flat = n.Ref.Path[0]
				}
			}
		}
		return func() {
			c.rootLoaded = true
			c.databases, c.current = dbs, current
			if len(dbs) == 0 && flat != "" {
				c.databases, c.current = []string{flat}, flat
				c.nested[flat], c.schemasLoaded[flat] = false, true
			}
		}
	})
	return false
}

// wantSchemas reports whether a database's schemas are known, starting the
// load where they are not. Called under the lock.
func (c *SchemaCache) wantSchemas(db string) bool {
	if c.schemasLoaded[db] {
		return true
	}
	ref := model.NewRef(model.KindDatabase, db)
	c.begin("schemas\x00"+db, func(ctx context.Context) func() {
		nodes, err := childNodes(ctx, c.src, ref)
		if err != nil {
			return func() { c.schemasLoaded[db] = true }
		}
		var names []string
		for _, n := range nodes {
			if n.Ref.Kind == model.KindSchema {
				names = append(names, n.Ref.Name())
			}
		}
		return func() {
			c.schemasLoaded[db] = true
			c.nested[db] = len(names) > 0
			c.schemas[db] = names
		}
	})
	return false
}

// objectClasses are the classes whose objects a query can read from.
var objectClasses = []model.ObjectKind{model.KindTable, model.KindView, model.KindMaterializedView}

func isObjectClass(k model.ObjectKind) bool {
	for _, c := range objectClasses {
		if c == k {
			return true
		}
	}
	return false
}

// loadObjects fetches a container's tables and views. Called under the lock.
func (c *SchemaCache) loadObjects(key string, ref model.ObjectRef) {
	c.begin(key, func(ctx context.Context) func() {
		var objs []sqlcomplete.Object
		seen := map[string]bool{}
		take := func(nodes []model.Node, want model.ObjectKind) {
			for _, n := range nodes {
				kind := n.Ref.Kind
				if want != "" && kind != want || !isObjectClass(kind) || seen[n.Ref.Name()] {
					continue
				}
				seen[n.Ref.Name()] = true
				objs = append(objs, sqlcomplete.Object{Name: n.Ref.Name(), Kind: kind})
			}
		}
		// A source that lists its objects straight under the schema, rather
		// than under a class for each kind, is read here.
		if nodes, err := childNodes(ctx, c.src, ref); err == nil {
			take(nodes, "")
		}
		for _, kind := range objectClasses {
			// A class a source does not have answers with an error, which is
			// not a failure: the others still hold what there is.
			nodes, err := childNodes(ctx, c.src, model.ClassRef(ref, kind))
			if err != nil {
				continue
			}
			take(nodes, kind)
		}
		return func() { c.objects[key] = objs }
	})
}

// loadColumns fetches one table's or view's columns. Called under the lock.
func (c *SchemaCache) loadColumns(key string, ref model.ObjectRef) {
	c.begin(key, func(ctx context.Context) func() {
		var cols []sqlcomplete.Column
		nodes, err := childNodes(ctx, c.src, ref)
		if err == nil {
			for _, n := range nodes {
				if n.Ref.Kind == model.KindColumn {
					cols = append(cols, sqlcomplete.Column{Name: n.Ref.Name(), Type: n.Attrs["type"]})
				}
			}
		}
		return func() { c.columns[key] = cols }
	})
}

// loadRoutines fetches a container's functions and procedures. Called under
// the lock.
func (c *SchemaCache) loadRoutines(key string, ref model.ObjectRef) {
	c.begin(key, func(ctx context.Context) func() {
		var names []string
		nodes, err := childNodes(ctx, c.src, model.ClassRef(ref, model.KindRoutine))
		if err == nil {
			for _, n := range nodes {
				if n.Ref.Kind == model.KindRoutine {
					names = append(names, n.Ref.Name())
				}
			}
		}
		return func() { c.routines[key] = names }
	})
}

// begin starts one background load under a key, unless that key is already
// loading. work does the fetching off the lock and returns what to record,
// which runs under it. Called under the lock.
func (c *SchemaCache) begin(key string, work func(ctx context.Context) func()) {
	if c.loading[key] {
		return
	}
	c.loading[key] = true
	gen := c.gen
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, c.timeout)
		defer cancel()
		apply := work(ctx)

		c.mu.Lock()
		delete(c.loading, key)
		stale := gen != c.gen
		if !stale && apply != nil {
			apply()
		}
		subs := make([]func(), 0, len(c.subs))
		for _, fn := range c.subs {
			subs = append(subs, fn)
		}
		c.mu.Unlock()
		if stale || apply == nil {
			return
		}
		for _, fn := range subs {
			fn()
		}
	}()
}

// refKey names a node for the cache's maps. A path element cannot hold the
// separator: it is a name, and no engine's names hold a zero byte.
func refKey(ref model.ObjectRef) string { return strings.Join(ref.Path, "\x00") }

// rootNodes and childNodes are the driver calls, with a driver's panic turned
// into an error (ADR-0017).
func rootNodes(ctx context.Context, src source.Source) (_ []model.Node, err error) {
	defer panics.Recover(&err, "listing the databases")
	return src.Root(ctx)
}

func childNodes(ctx context.Context, src source.Source, ref model.ObjectRef) (_ []model.Node, err error) {
	defer panics.Recover(&err, "listing what is in a "+string(ref.Kind))
	return src.Children(ctx, ref)
}
