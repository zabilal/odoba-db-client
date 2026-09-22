//go:build conformance

package cassandra

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// The tree, against a real cluster (T2.48). The fixture is a keyspace of this
// suite's own, made here so that a cluster that has never seen these tests
// still runs them.

const (
	fixture = "ikigai_it"
	// empty is a keyspace nobody has written to, which sorts after fixture.
	empty = "ikigai_it_empty"
)

// seeded makes the keyspace these tests read, and says whether the cluster
// allowed a materialized view: they are experimental, and a cluster that was
// not told to enable them refuses.
func seeded(t *testing.T, src source.Source) bool {
	t.Helper()
	ctx := context.Background()
	s := src.(*cassandraSource)
	for _, ddl := range []string{
		`CREATE KEYSPACE IF NOT EXISTS ` + fixture + ` WITH replication =
			{'class': 'SimpleStrategy', 'replication_factor': 1}`,
		`CREATE TYPE IF NOT EXISTS ` + fixture + `.address (street text, city text)`,
		`CREATE TABLE IF NOT EXISTS ` + fixture + `.people (
			country text, id int, name text, score double, home frozen<address>,
			tags set<text>, seen timestamp, PRIMARY KEY ((country), id))
			WITH CLUSTERING ORDER BY (id DESC) AND comment = 'people, by country'`,
		`CREATE INDEX IF NOT EXISTS people_by_name ON ` + fixture + `.people (name)`,
		`CREATE TABLE IF NOT EXISTS ` + fixture + `.orders (id int PRIMARY KEY, total decimal)`,
		// A keyspace with nothing in it, which still has a tree.
		`CREATE KEYSPACE IF NOT EXISTS ` + empty + ` WITH replication =
			{'class': 'SimpleStrategy', 'replication_factor': 1}`,
	} {
		if err := s.session.Query(ddl).WithContext(ctx).Exec(); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
	view := `CREATE MATERIALIZED VIEW IF NOT EXISTS ` + fixture + `.people_by_score AS
		SELECT country, id, score FROM ` + fixture + `.people
		WHERE country IS NOT NULL AND id IS NOT NULL AND score IS NOT NULL
		PRIMARY KEY ((country), score, id)`
	return s.session.Query(view).WithContext(ctx).Exec() == nil
}

// labels are the nodes' labels, in the order they came.
func labels(nodes []model.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Label)
	}
	return out
}

func TestLiveListsTheKeyspacesAPersonPutThere(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	nodes, err := src.Root(context.Background())
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	var found *model.Node
	for i, n := range nodes {
		if n.Label == fixture {
			found = &nodes[i]
		}
		// The cluster's own account of itself is not somebody's data.
		if theServersOwn(n.Label) {
			t.Errorf("the tree shows the cluster's own keyspace %s", n.Label)
		}
		if n.Ref.Kind != model.KindDatabase || !n.HasChildren {
			t.Errorf("%s is %+v", n.Label, n)
		}
		// A keyspace is described — how it is replicated, and whether it is
		// durably written — and has no rows of its own, so it must offer the
		// description or nothing can reach it (ADR-0106).
		if !n.Describable {
			t.Errorf("the keyspace %s offers no description, though Describe returns one", n.Label)
		}
	}
	if found == nil {
		t.Fatalf("the keyspaces are %v, and %s is not among them", labels(nodes), fixture)
	}
	// In name order: a cluster keeps its keyspaces in the order of the tokens
	// of their names, which is no order to read a list in.
	if names := labels(nodes); !sort.StringsAreSorted(names) {
		t.Errorf("the keyspaces read %v", names)
	}
	// The keyspace this connection was opened on is marked as the one it is.
	if found.Attrs["current"] != "true" {
		t.Errorf("%s is not marked as the one opened on: %+v", fixture, found.Attrs)
	}
}

func TestLiveListsWhatAKeyspaceHolds(t *testing.T) {
	src := live(t, liveConfig(fixture))
	views := seeded(t, src)
	ctx := context.Background()
	keyspace := model.NewRef(model.KindDatabase, fixture)

	classes, err := src.Children(ctx, keyspace)
	if err != nil {
		t.Fatalf("classes: %v", err)
	}
	held := map[string]model.Node{}
	for _, c := range classes {
		held[c.Label] = c
	}
	for _, want := range []string{"Tables", "Indexes", "Types"} {
		c, ok := held[want]
		if !ok {
			t.Fatalf("the classes are %v, without %s", labels(classes), want)
		}
		if c.Badge == nil || !c.Badge.Exact || c.Badge.Text == "0" {
			t.Errorf("%s is counted %+v", want, c.Badge)
		}
	}
	if views != (held["Materialized Views"].Label != "") {
		t.Errorf("materialized views are %v, and the tree says %+v", views, held["Materialized Views"])
	}

	// Tables, and then a table's columns: the key first, then what orders
	// rows within a partition, then the rest.
	tables, err := src.Children(ctx, held["Tables"].Ref)
	if err != nil {
		t.Fatalf("the tables: %v", err)
	}
	// In name order, and counted as they are listed. Other tests put tables
	// of their own here, so what is asserted is what this one made.
	if names := labels(tables); !sort.StringsAreSorted(names) {
		t.Errorf("the tables read %v", names)
	}
	if held["Tables"].Badge.Text != strconv.Itoa(len(tables)) {
		t.Errorf("%d tables are counted %+v", len(tables), held["Tables"].Badge)
	}
	var people model.Node
	for _, n := range tables {
		if n.Label == "people" {
			people = n
		}
	}
	if people.Label == "" {
		t.Fatalf("the tables are %v, without people", labels(tables))
	}
	cols, err := src.Children(ctx, people.Ref)
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	if got := strings.Join(labels(cols), " "); got != "country id home name score seen tags" {
		t.Errorf("the columns read %s", got)
	}
	by := map[string]model.Node{}
	for _, c := range cols {
		by[c.Label] = c
	}
	if by["country"].Attrs["key"] != partitionKey || by["id"].Attrs["key"] != clusteringKey {
		t.Errorf("the key columns are %+v and %+v", by["country"].Attrs, by["id"].Attrs)
	}
	if by["name"].Attrs["key"] != "" || by["name"].Attrs["type"] != "text" {
		t.Errorf("an ordinary column is %+v", by["name"].Attrs)
	}
	// A key column is part of a row's address, and an address is never empty.
	if by["country"].Attrs["nullable"] != "false" || by["name"].Attrs["nullable"] != "true" {
		t.Errorf("what may be empty: %v, %v", by["country"].Attrs, by["name"].Attrs)
	}
	if by["home"].Attrs["type"] != "frozen<address>" || by["tags"].Attrs["type"] != "set<text>" {
		t.Errorf("the types read %v and %v", by["home"].Attrs, by["tags"].Attrs)
	}

	// An index is listed with the table it is on: two tables' may share a name.
	indexes, err := src.Children(ctx, held["Indexes"].Ref)
	if err != nil || len(indexes) != 1 || indexes[0].Label != "people_by_name on people" {
		t.Fatalf("the indexes are %v: %v", labels(indexes), err)
	}
	types, err := src.Children(ctx, held["Types"].Ref)
	if err != nil || len(types) != 1 || types[0].Label != "address" || types[0].HasChildren {
		t.Fatalf("the types are %+v: %v", types, err)
	}

	// A materialized view holds columns of its own, as a table does.
	if views {
		mv, err := src.Children(ctx, held["Materialized Views"].Ref)
		if err != nil || len(mv) != 1 {
			t.Fatalf("the views are %v: %v", labels(mv), err)
		}
		held, err := src.Children(ctx, mv[0].Ref)
		if err != nil || strings.Join(labels(held), " ") != "country score id" {
			t.Errorf("the view's columns read %v: %v", labels(held), err)
		}
	}

	// A keyspace nobody has written to still opens onto its tables, empty: a
	// node that opens onto nothing reads as a tree that failed.
	none, err := src.Children(ctx, model.NewRef(model.KindDatabase, empty))
	if err != nil || len(none) != 1 || none[0].Label != "Tables" || none[0].Badge.Text != "0" {
		t.Errorf("an empty keyspace holds %+v: %v", none, err)
	}
}

func TestLiveSaysHowAKeyspaceIsReplicated(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	desc, err := src.Describe(context.Background(), model.NewRef(model.KindDatabase, fixture))
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	schema, ok := desc.(*model.Schema)
	if !ok {
		t.Fatalf("a keyspace is described as %T", desc)
	}
	// The strategy is written as a Java class, and what it says is the end
	// of it: a person reads SimpleStrategy, not its package.
	if schema.Name != fixture || schema.Attrs["strategy"] != "SimpleStrategy" {
		t.Errorf("the keyspace is %+v", schema.Attrs)
	}
	if schema.Attrs["replication_factor"] != "1" || schema.Attrs["durable writes"] != "true" {
		t.Errorf("how it is replicated: %+v", schema.Attrs)
	}
	held := make([]string, 0, len(schema.Tables))
	for _, tbl := range schema.Tables {
		held = append(held, tbl.Name)
	}
	if !sort.StringsAreSorted(held) || !slices.Contains(held, "people") || len(schema.UserTypes) != 1 {
		t.Errorf("what it holds: %+v %+v", schema.Tables, schema.UserTypes)
	}
}

func TestLiveDescribesATableAsWhatAddressesItsRows(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	desc, err := src.Describe(context.Background(), model.NewRef(model.KindTable, fixture, "people"))
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	table, ok := desc.(*model.Table)
	if !ok {
		t.Fatalf("a table is described as %T", desc)
	}
	if table.Comment != "people, by country" {
		t.Errorf("the comment is %q", table.Comment)
	}
	if table.PrimaryKey == nil || strings.Join(table.PrimaryKey.Columns, ", ") != "country, id" {
		t.Errorf("the key is %+v", table.PrimaryKey)
	}
	// Cassandra keeps no count of a table's rows, and this one does not
	// pretend to (FR-2.5).
	if table.RowsEstimate != -1 {
		t.Errorf("a table counted as %d rows", table.RowsEstimate)
	}
	by := map[string]model.Column{}
	for _, c := range table.Columns {
		by[c.Name] = c
	}
	if by["id"].Attrs["order"] != "desc" {
		t.Errorf("what orders a partition's rows: %+v", by["id"].Attrs)
	}
	if by["score"].Type.Class != model.TypeFloat || by["seen"].Type.Class != model.TypeTimestamp {
		t.Errorf("the types are %+v and %+v", by["score"].Type, by["seen"].Type)
	}
	if by["country"].Position != 1 || by["id"].Position != 2 {
		t.Errorf("the key columns are at %d and %d", by["country"].Position, by["id"].Position)
	}
	if len(table.Indexes) != 1 || table.Indexes[0].Name != "people_by_name" ||
		len(table.Indexes[0].Columns) != 1 || table.Indexes[0].Columns[0].Name != "name" {
		t.Errorf("the indexes are %+v", table.Indexes)
	}
}

func TestLiveDescribesAMaterializedViewAsWhatItIsWrittenFrom(t *testing.T) {
	src := live(t, liveConfig(fixture))
	if !seeded(t, src) {
		t.Skip("this cluster does not allow materialized views (they are experimental)")
	}
	desc, err := src.Describe(context.Background(),
		model.NewRef(model.KindMaterializedView, fixture, "people_by_score"))
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	view, ok := desc.(*model.View)
	if !ok {
		t.Fatalf("a view is described as %T", desc)
	}
	if !view.Materialized || !strings.Contains(view.Definition, "FROM people") {
		t.Errorf("the view is %+v", view)
	}
	if !strings.Contains(view.Definition, "WHERE") || !strings.Contains(view.Definition, "score IS NOT NULL") {
		t.Errorf("the rows that reach it: %q", view.Definition)
	}
	if len(view.Columns) != 3 {
		t.Errorf("the view holds %d columns", len(view.Columns))
	}
}

func TestLiveCountsNothingItWouldHaveToReadToCount(t *testing.T) {
	src := live(t, liveConfig(fixture))
	seeded(t, src)
	for _, ref := range []model.ObjectRef{
		model.NewRef(model.KindDatabase, fixture),
		model.NewRef(model.KindTable, fixture, "people"),
	} {
		if badge, ok, err := src.Badge(context.Background(), ref); ok || err != nil {
			t.Errorf("%s is badged %+v, %v", ref, badge, err)
		}
	}
}
