package cassandra

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
)

// The tree (FR-2.1, FR-2.2, FR-12.3, T2.48): keyspaces, the classes each one
// holds, and the objects in them — the shape every other relational driver
// presents, so the explorer, the tabs and session restore need to know
// nothing about Cassandra (REQ-DB-4).
//
// A keyspace is this paradigm's database: it holds tables, the materialized
// views built over them, the indexes on them and the types they are written
// in. A table's own children are its columns, as they are on PostgreSQL.
//
// Everything here is read from system_schema, which is the cluster's own
// account of itself and needs no permission beyond reading it.

// Root lists the keyspaces a person put on the cluster.
func (s *cassandraSource) Root(ctx context.Context) (_ []model.Node, err error) {
	defer panics.Recover(&err, "listing the keyspaces")
	names, err := s.keyspaces(ctx)
	if err != nil {
		return nil, err
	}
	current := strings.TrimSpace(s.cfg.Database)
	out := make([]model.Node, 0, len(names))
	for _, name := range names {
		// Describable: a keyspace has how it is replicated and how it is
		// durably written, which Describe has returned all along and nothing
		// could reach (ADR-0106).
		node := model.Node{Ref: model.NewRef(model.KindDatabase, name), Label: name,
			HasChildren: true, Describable: true}
		if name == current {
			node.Attrs = map[string]string{"current": "true"}
		}
		out = append(out, node)
	}
	return out, nil
}

// keyspaces are the cluster's keyspaces but its own, in name order.
//
// The server's are hidden as another engine's system schemas are: system,
// system_schema and the rest are the cluster's account of itself rather than
// anybody's data. A connection may still be opened on one by name.
func (s *cassandraSource) keyspaces(ctx context.Context) ([]string, error) {
	iter := s.session.Query("SELECT keyspace_name FROM system_schema.keyspaces").WithContext(ctx).Iter()
	var read []string
	var name string
	for iter.Scan(&name) {
		read = append(read, name)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return userKeyspaces(read), nil
}

// userKeyspaces are the keyspaces somebody made, in name order.
//
// A cluster hands them back in the order of the tokens of their names, which
// is no order to read a list in, so they are sorted here. Its own — system,
// system_schema and the rest — are its account of itself rather than
// anybody's data, and are left out as another engine's system schemas are.
func userKeyspaces(read []string) []string {
	var out []string
	for _, name := range read {
		if !theServersOwn(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// theServersOwn reports a keyspace the cluster keeps for itself.
func theServersOwn(keyspace string) bool {
	return keyspace == "system" || strings.HasPrefix(keyspace, "system_") ||
		keyspace == "dse_system" || strings.HasPrefix(keyspace, "dse_")
}

// Children lists a node's direct children.
func (s *cassandraSource) Children(ctx context.Context, ref model.ObjectRef) (_ []model.Node, err error) {
	defer panics.Recover(&err, "listing what is in a "+string(ref.Kind))
	switch ref.Kind {
	case model.KindDatabase:
		return s.keyspaceClasses(ctx, ref)
	case model.KindFolder:
		kind, ok := model.ClassOf(ref)
		if !ok {
			return nil, fmt.Errorf("cassandra: no such class %s", ref)
		}
		return s.classContents(ctx, ref, kind)
	case model.KindTable, model.KindMaterializedView:
		return s.columnNodes(ctx, ref)
	}
	return nil, nil
}

// keyspaceClasses are the classes a keyspace holds, each with an exact count.
func (s *cassandraSource) keyspaceClasses(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	held, err := s.holdings(ctx, ref.Name())
	if err != nil {
		return nil, err
	}
	out := model.ClassNodes(ref, held)
	if len(out) == 0 {
		// A keyspace with nothing in it still shows its tables, empty: a node
		// that opens onto nothing reads as a tree that failed rather than as
		// a keyspace nobody has written to yet.
		out = []model.Node{model.ClassNode(ref, model.KindTable, 0)}
	}
	return out, nil
}

// holdings is how many objects of each class a keyspace holds.
func (s *cassandraSource) holdings(ctx context.Context, keyspace string) (map[model.ObjectKind]int64, error) {
	out := map[model.ObjectKind]int64{}
	for _, c := range []struct {
		kind  model.ObjectKind
		query string
	}{
		{model.KindTable, "SELECT table_name FROM system_schema.tables WHERE keyspace_name = ?"},
		{model.KindMaterializedView, "SELECT view_name FROM system_schema.views WHERE keyspace_name = ?"},
		{model.KindIndex, "SELECT index_name FROM system_schema.indexes WHERE keyspace_name = ?"},
		{model.KindUserType, "SELECT type_name FROM system_schema.types WHERE keyspace_name = ?"},
	} {
		names, err := s.names(ctx, c.query, keyspace)
		if err != nil {
			return nil, err
		}
		out[c.kind] = int64(len(names))
	}
	return out, nil
}

// classContents are the objects of one class in a keyspace.
func (s *cassandraSource) classContents(ctx context.Context, class model.ObjectRef, kind model.ObjectKind) ([]model.Node, error) {
	keyspace := class.Path[0]
	switch kind {
	case model.KindTable:
		names, err := s.names(ctx, "SELECT table_name FROM system_schema.tables WHERE keyspace_name = ?", keyspace)
		if err != nil {
			return nil, err
		}
		return objectNodes(model.KindTable, keyspace, names, true), nil
	case model.KindMaterializedView:
		names, err := s.names(ctx, "SELECT view_name FROM system_schema.views WHERE keyspace_name = ?", keyspace)
		if err != nil {
			return nil, err
		}
		return objectNodes(model.KindMaterializedView, keyspace, names, true), nil
	case model.KindUserType:
		names, err := s.names(ctx, "SELECT type_name FROM system_schema.types WHERE keyspace_name = ?", keyspace)
		if err != nil {
			return nil, err
		}
		return objectNodes(model.KindUserType, keyspace, names, false), nil
	case model.KindIndex:
		return s.indexNodes(ctx, keyspace)
	}
	return nil, nil
}

// objectNodes are a class's objects, in name order.
func objectNodes(kind model.ObjectKind, keyspace string, names []string, holds bool) []model.Node {
	out := make([]model.Node, 0, len(names))
	for _, name := range names {
		out = append(out, model.Node{
			Ref: model.NewRef(kind, keyspace, name), Label: name,
			// A table and a view hold columns and rows; a type holds neither.
			HasChildren: holds, Browsable: holds,
		})
	}
	return out
}

// indexNodes lists a keyspace's indexes, each named with the table it is on:
// they are listed together, and two tables' may share a name.
func (s *cassandraSource) indexNodes(ctx context.Context, keyspace string) ([]model.Node, error) {
	iter := s.session.Query("SELECT index_name, table_name, kind FROM system_schema.indexes WHERE keyspace_name = ?",
		keyspace).WithContext(ctx).Iter()
	var out []model.Node
	var name, table, kind string
	for iter.Scan(&name, &table, &kind) {
		out = append(out, model.Node{
			Ref:   model.NewRef(model.KindIndex, keyspace, table, name),
			Label: model.OnTable(name, table),
			Attrs: map[string]string{"table": table, "kind": strings.ToLower(kind)},
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	// In name order already, as the tables and the types are.
	return out, nil
}

// columnNodes are a table's or a view's columns, keys first.
func (s *cassandraSource) columnNodes(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 2 {
		return nil, nil
	}
	cols, err := s.columns(ctx, ref.Path[0], ref.Path[1])
	if err != nil {
		return nil, err
	}
	out := make([]model.Node, 0, len(cols))
	for _, c := range cols {
		attrs := map[string]string{"type": c.Type.Native, "nullable": strconv.FormatBool(c.Type.Nullable)}
		if kind := c.Attrs["kind"]; kind == partitionKey || kind == clusteringKey {
			attrs["key"] = kind
		}
		out = append(out, model.Node{
			Ref:   model.NewRef(model.KindColumn, ref.Path[0], ref.Path[1], c.Name),
			Label: c.Name, Attrs: attrs,
		})
	}
	return out, nil
}

// names reads one text column of a system_schema query, in the order the
// cluster keeps them, which is already by name.
func (s *cassandraSource) names(ctx context.Context, query, keyspace string) ([]string, error) {
	iter := s.session.Query(query, keyspace).WithContext(ctx).Iter()
	var out []string
	var name string
	for iter.Scan(&name) {
		out = append(out, name)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	// In name order already: within a keyspace these are clustered by their
	// own names, so the cluster hands them back in it.
	return out, nil
}

// Describe loads what the structure tab shows: a keyspace's replication, or
// a table's columns, key and indexes.
func (s *cassandraSource) Describe(ctx context.Context, ref model.ObjectRef) (_ any, err error) {
	defer panics.Recover(&err, "describing a "+string(ref.Kind))
	switch ref.Kind {
	case model.KindDatabase:
		return s.describeKeyspace(ctx, ref.Name())
	case model.KindTable:
		return s.describeTable(ctx, ref)
	case model.KindMaterializedView:
		return s.describeView(ctx, ref)
	}
	return nil, nil
}

// describeKeyspace is how a keyspace is replicated, which is the first thing
// to know about one: how many copies of a row there are, and where they are
// (FR-12.3).
func (s *cassandraSource) describeKeyspace(ctx context.Context, keyspace string) (*model.Schema, error) {
	var durable bool
	replication := map[string]string{}
	err := s.session.Query("SELECT durable_writes, replication FROM system_schema.keyspaces WHERE keyspace_name = ?",
		keyspace).WithContext(ctx).Scan(&durable, &replication)
	if err != nil {
		return nil, err
	}
	out := &model.Schema{Name: keyspace, Attrs: map[string]string{
		"durable writes": strconv.FormatBool(durable),
	}}
	for name, value := range replication {
		if name == "class" {
			// The strategy is written as a Java class, and the part of it
			// that says anything is the last.
			out.Attrs["strategy"] = value[strings.LastIndex(value, ".")+1:]
			continue
		}
		out.Attrs[name] = value
	}
	// What the keyspace holds, named rather than counted, so the panel can
	// say how much of each there is without reading any of them.
	tables, err := s.names(ctx, "SELECT table_name FROM system_schema.tables WHERE keyspace_name = ?", keyspace)
	if err != nil {
		return nil, err
	}
	for _, name := range tables {
		out.Tables = append(out.Tables, model.Table{Name: name, RowsEstimate: -1})
	}
	views, err := s.names(ctx, "SELECT view_name FROM system_schema.views WHERE keyspace_name = ?", keyspace)
	if err != nil {
		return nil, err
	}
	for _, name := range views {
		out.Views = append(out.Views, model.View{Name: name, Materialized: true})
	}
	types, err := s.names(ctx, "SELECT type_name FROM system_schema.types WHERE keyspace_name = ?", keyspace)
	if err != nil {
		return nil, err
	}
	for _, name := range types {
		out.UserTypes = append(out.UserTypes, model.UserType{Name: name})
	}
	return out, nil
}

// describeTable is a table's columns, the key they are partitioned and
// ordered by, and the indexes on it.
func (s *cassandraSource) describeTable(ctx context.Context, ref model.ObjectRef) (*model.Table, error) {
	if len(ref.Path) < 2 {
		return nil, fmt.Errorf("cassandra: %s is not a table", ref)
	}
	keyspace, name := ref.Path[0], ref.Path[1]
	var comment string
	if err := s.session.Query("SELECT comment FROM system_schema.tables WHERE keyspace_name = ? AND table_name = ?",
		keyspace, name).WithContext(ctx).Scan(&comment); err != nil {
		return nil, err
	}
	cols, err := s.columns(ctx, keyspace, name)
	if err != nil {
		return nil, err
	}
	indexes, err := s.indexes(ctx, keyspace, name)
	if err != nil {
		return nil, err
	}
	// A row's address is never counted: Cassandra has no cheap count of a
	// table, and counting one is a read of every partition (FR-2.5).
	out := &model.Table{Name: name, Comment: comment, Columns: cols, Indexes: indexes, RowsEstimate: -1}
	if key := primaryKey(cols); len(key) > 0 {
		out.PrimaryKey = &model.PrimaryKey{Columns: key}
	}
	return out, nil
}

// describeView is a materialized view: the columns it holds, and the table
// whose rows it is written from.
func (s *cassandraSource) describeView(ctx context.Context, ref model.ObjectRef) (*model.View, error) {
	if len(ref.Path) < 2 {
		return nil, fmt.Errorf("cassandra: %s is not a view", ref)
	}
	keyspace, name := ref.Path[0], ref.Path[1]
	var base, where string
	if err := s.session.Query("SELECT base_table_name, where_clause FROM system_schema.views WHERE keyspace_name = ? AND view_name = ?",
		keyspace, name).WithContext(ctx).Scan(&base, &where); err != nil {
		return nil, err
	}
	cols, err := s.columns(ctx, keyspace, name)
	if err != nil {
		return nil, err
	}
	// The definition a person reads is the one they would write: the table it
	// is built from, and the rows of it that reach the view.
	definition := "FROM " + base
	if strings.TrimSpace(where) != "" {
		definition += " WHERE " + where
	}
	return &model.View{Name: name, Materialized: true, Definition: definition, Columns: cols}, nil
}

// The kinds of column Cassandra has, in its own words.
const (
	partitionKey  = "partition_key"
	clusteringKey = "clustering"
	staticColumn  = "static"
)

// columns reads a table's or a view's columns: the partition key first, then
// what orders the rows within a partition, then the rest by name. That is
// the order a person writes them in, and the order they are addressed in.
func (s *cassandraSource) columns(ctx context.Context, keyspace, table string) ([]model.Column, error) {
	iter := s.session.Query(`SELECT column_name, kind, position, clustering_order, type
		FROM system_schema.columns WHERE keyspace_name = ? AND table_name = ?`,
		keyspace, table).WithContext(ctx).Iter()
	type column struct {
		model.Column
		kind     string
		position int
	}
	var read []column
	var name, kind, order, cqlType string
	var position int
	for iter.Scan(&name, &kind, &position, &order, &cqlType) {
		c := column{kind: kind, position: position}
		c.Name = name
		c.Type = cqlDataType(cqlType)
		// A key column is part of a row's address, and an address is never
		// empty; everything else may be.
		c.Type.Nullable = kind != partitionKey && kind != clusteringKey
		c.Attrs = map[string]string{"kind": kind}
		if kind == clusteringKey && order != "" && order != "none" {
			c.Attrs["order"] = strings.ToLower(order)
		}
		read = append(read, c)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	sort.SliceStable(read, func(i, j int) bool {
		a, b := read[i], read[j]
		if ra, rb := columnRank(a.kind), columnRank(b.kind); ra != rb {
			return ra < rb
		}
		if a.position != b.position && a.position >= 0 && b.position >= 0 {
			return a.position < b.position
		}
		return a.Name < b.Name
	})
	out := make([]model.Column, 0, len(read))
	for i, c := range read {
		c.Column.Position = i + 1
		out = append(out, c.Column)
	}
	return out, nil
}

// columnRank orders the kinds of column: the partition key, then what
// clusters rows within a partition, then the static columns a partition
// shares, then the rest.
func columnRank(kind string) int {
	switch kind {
	case partitionKey:
		return 0
	case clusteringKey:
		return 1
	case staticColumn:
		return 2
	}
	return 3
}

// primaryKey is what addresses a row: the partition key, then the columns
// that order rows within the partition. Cassandra does not name it.
func primaryKey(cols []model.Column) []string {
	var out []string
	for _, c := range cols {
		if kind := c.Attrs["kind"]; kind == partitionKey || kind == clusteringKey {
			out = append(out, c.Name)
		}
	}
	return out
}

// indexes reads the indexes on one table. What each is on is in its options,
// under a name the server chose: "target".
func (s *cassandraSource) indexes(ctx context.Context, keyspace, table string) ([]model.Index, error) {
	iter := s.session.Query(`SELECT index_name, kind, options FROM system_schema.indexes
		WHERE keyspace_name = ? AND table_name = ?`, keyspace, table).WithContext(ctx).Iter()
	var out []model.Index
	var name, kind string
	options := map[string]string{}
	for iter.Scan(&name, &kind, &options) {
		ix := model.Index{Name: name, Method: strings.ToLower(kind)}
		if target := options["target"]; target != "" {
			ix.Columns = []model.IndexColumn{{Name: target}}
		}
		out = append(out, ix)
		options = map[string]string{}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Badge is nothing. Cassandra keeps no count of a table's rows, and counting
// them is a read of every partition on every node, which a badge must never
// be (FR-2.5).
func (s *cassandraSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}
