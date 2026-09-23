package clickhouse

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The explorer tree (FR-2.1, FR-2.2):
//
//	database        [db]
//	  class         [db, kind]            model.ClassRef: "table", "view", …
//	    object      [db, name]
//	      column    [db, table, column]
//
// Two levels above the objects rather than three: ClickHouse has no schema
// between a database and its tables.
//
// The queries read the system database, which is where ClickHouse keeps
// what it knows about itself. Every name reaches the server bound.

// systemDatabases are the ones the server keeps for itself. They are shown,
// because system.query_log and system.parts are where somebody looks when
// something is wrong, but marked so they sort out of the way.
var systemDatabases = map[string]bool{
	"system": true, "information_schema": true, "INFORMATION_SCHEMA": true,
}

// Root is the databases this login can see.
func (s *clickhouseSource) Root(ctx context.Context) ([]model.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM system.databases ORDER BY name`)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		node := model.Node{Ref: model.NewRef(model.KindDatabase, name), Label: name, HasChildren: true}
		switch {
		case systemDatabases[name]:
			node.Attrs = map[string]string{"system": "true"}
		case name == s.primary:
			node.Attrs = map[string]string{"current": "true"}
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func (s *clickhouseSource) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindDatabase:
		return s.folders(ctx, ref)
	case model.KindFolder:
		kind, ok := model.ClassOf(ref)
		if !ok || len(ref.Path) < 1 {
			return nil, fmt.Errorf("clickhouse: no such class %s", ref)
		}
		return s.objects(ctx, ref.Path[0], kind)
	case model.KindTable, model.KindView, model.KindMaterializedView:
		if len(ref.Path) < 2 {
			return nil, fmt.Errorf("clickhouse: incomplete reference %s", ref)
		}
		cols, err := s.columns(ctx, ref)
		if err != nil {
			return nil, err
		}
		out := make([]model.Node, len(cols))
		for i, c := range cols {
			attrs := map[string]string{"type": c.Type.Native, "nullable": strconv.FormatBool(c.Type.Nullable)}
			if c.Attrs["key"] != "" {
				attrs["key"] = c.Attrs["key"]
			}
			out[i] = model.Node{Ref: model.NewRef(model.KindColumn, ref.Path[0], ref.Path[1], c.Name),
				Label: c.Name, Attrs: attrs}
		}
		return out, nil
	}
	return nil, nil
}

// folders is a database's object classes that hold something (FR-2.2), with
// exact counts, in one round trip.
//
// What ClickHouse calls a table, a view, a materialized view and a
// dictionary all live in system.tables, told apart by their engine.
func (s *clickhouseSource) folders(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	db := ref.Path[0]
	var tables, views, matviews, dicts int64
	err := s.db.QueryRowContext(ctx, `SELECT
		  countIf(engine NOT IN ('View', 'MaterializedView', 'Dictionary')),
		  countIf(engine = 'View'),
		  countIf(engine = 'MaterializedView'),
		  countIf(engine = 'Dictionary')
		FROM system.tables WHERE database = ?`, db).Scan(&tables, &views, &matviews, &dicts)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	out := model.ClassNodes(ref, map[model.ObjectKind]int64{
		model.KindTable: tables, model.KindView: views,
		model.KindMaterializedView: matviews, model.KindRoutine: dicts,
	})
	if len(out) == 0 {
		// A database holding nothing still shows its tables, empty: a
		// database node says it has children, and one that then lists none
		// draws an expander that turns and never opens (FR-2.2).
		out = []model.Node{model.ClassNode(ref, model.KindTable, 0)}
	}
	return out, nil
}

// engineFilter is what tells a class apart in system.tables.
var engineFilter = map[model.ObjectKind]string{
	model.KindTable:            `engine NOT IN ('View', 'MaterializedView', 'Dictionary')`,
	model.KindView:             `engine = 'View'`,
	model.KindMaterializedView: `engine = 'MaterializedView'`,
	model.KindRoutine:          `engine = 'Dictionary'`,
}

func (s *clickhouseSource) objects(ctx context.Context, db string, kind model.ObjectKind) ([]model.Node, error) {
	where, ok := engineFilter[kind]
	if !ok {
		return nil, fmt.Errorf("clickhouse: no %s class", kind)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, engine, total_rows FROM system.tables
		WHERE database = ? AND `+where+` ORDER BY name`, db)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var name, engine string
		var total sql.NullInt64
		if err := rows.Scan(&name, &engine, &total); err != nil {
			return nil, err
		}
		n := model.Node{Ref: model.NewRef(kind, db, name), Label: name,
			Attrs: map[string]string{"engine": engine}}
		if kind != model.KindRoutine {
			n.HasChildren, n.Browsable = true, true
		}
		// The engine knows how many rows it holds, and says so for the
		// engines that keep a count. A View holds none of its own.
		if total.Valid {
			n.Badge = &model.Badge{Text: humanCount(total.Int64), Exact: true}
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// columns reads a table's columns, with the type as ClickHouse declares it
// and whether the column is part of what orders the table.
func (s *clickhouseSource) columns(ctx context.Context, ref model.ObjectRef) ([]model.Column, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, type, position, default_kind, default_expression,
		  comment, is_in_primary_key, is_in_sorting_key, is_in_partition_key
		FROM system.columns WHERE database = ? AND table = ? ORDER BY position`,
		ref.Path[0], ref.Path[1])
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.Column
	for rows.Next() {
		var name, typ, kind, expr, comment string
		var position uint64
		var inPrimary, inSorting, inPartition uint8
		if err := rows.Scan(&name, &typ, &position, &kind, &expr, &comment,
			&inPrimary, &inSorting, &inPartition); err != nil {
			return nil, err
		}
		c := model.Column{Name: name, Type: dataType(typ), Position: int(position), Comment: comment}
		// DEFAULT is a value the row takes; MATERIALIZED and ALIAS are
		// worked out, which is what Generated means.
		switch strings.ToUpper(kind) {
		case "DEFAULT":
			c.Default, c.HasDefault = expr, expr != ""
		case "MATERIALIZED", "ALIAS", "EPHEMERAL":
			c.Generated = expr
		}
		c.Attrs = map[string]string{}
		if inPrimary == 1 {
			// ClickHouse's primary key is the prefix of the sorting key it
			// keeps an index over. It is not unique and addresses no row,
			// so it is shown as what orders the table rather than as a key.
			c.Attrs["key"] = "sorting"
		}
		if inSorting == 1 {
			c.Attrs["sorting"] = "true"
		}
		if inPartition == 1 {
			c.Attrs["partition"] = "true"
		}
		if len(c.Attrs) == 0 {
			c.Attrs = nil
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Describe loads a table's structure (*model.Table) or a view's
// (*model.View).
func (s *clickhouseSource) Describe(ctx context.Context, ref model.ObjectRef) (any, error) {
	if !browsableKinds[ref.Kind] || len(ref.Path) < 2 {
		return nil, fmt.Errorf("clickhouse: cannot describe %s", ref)
	}
	cols, err := s.columns(ctx, ref)
	if err != nil {
		return nil, err
	}
	var engine, sortingKey, partitionKey, primaryKey, asSelect, comment string
	var total sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT engine, sorting_key, partition_key, primary_key,
		  as_select, comment, total_rows
		FROM system.tables WHERE database = ? AND name = ?`, ref.Path[0], ref.Path[1]).
		Scan(&engine, &sortingKey, &partitionKey, &primaryKey, &asSelect, &comment, &total)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	if ref.Kind != model.KindTable {
		return &model.View{Name: ref.Path[1], Columns: cols, Definition: asSelect,
			Comment: comment, Materialized: ref.Kind == model.KindMaterializedView}, nil
	}
	t := &model.Table{Name: ref.Path[1], Columns: cols, Comment: comment, RowsEstimate: -1,
		Attrs: map[string]string{"engine": engine}}
	if total.Valid {
		t.RowsEstimate = total.Int64
	}
	// The sorting key and the partition key are what a column store is
	// arranged by, and they are what somebody reading its structure needs.
	// They are not constraints, so they are not written as one.
	for key, value := range map[string]string{
		"sorting_key": sortingKey, "partition_key": partitionKey, "primary_key": primaryKey,
	} {
		if value != "" {
			t.Attrs[key] = value
		}
	}
	return t, nil
}

// Badge is the row count the engine keeps as it writes (FR-2.5).
//
// Whatever the catalogue counts, and nothing else: a view holds no rows of
// its own and a dictionary's are not rows somebody opens, and the
// catalogue says so by counting neither.
func (s *clickhouseSource) Badge(ctx context.Context, ref model.ObjectRef) (model.Badge, bool, error) {
	if len(ref.Path) < 2 {
		return model.Badge{}, false, nil
	}
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT total_rows FROM system.tables
		WHERE database = ? AND name = ?`, ref.Path[0], ref.Path[1]).Scan(&total)
	if err != nil || !total.Valid {
		if err == sql.ErrNoRows {
			err = nil
		}
		return model.Badge{}, false, statementError(err, ctx)
	}
	return model.Badge{Text: humanCount(total.Int64), Exact: true}, true, nil
}

// humanCount abbreviates a row count for a tree badge: 1234 → 1.2K.
func humanCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return trimZero(float64(n)/1e9) + "B"
	case n >= 1_000_000:
		return trimZero(float64(n)/1e6) + "M"
	case n >= 1_000:
		return trimZero(float64(n)/1e3) + "K"
	}
	return strconv.FormatInt(n, 10)
}

func trimZero(f float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(f, 'f', 1, 64), ".0")
}
