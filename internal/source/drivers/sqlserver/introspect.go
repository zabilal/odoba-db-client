package sqlserver

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
//	  schema        [db, schema]
//	    class       [db, schema, kind]          model.ClassRef: "table", "index", …
//	      object    [db, schema, name]
//	        column  [db, schema, table, column]
//
// Every level is one round trip, made when the node is expanded, and every
// name reaches the server bound rather than written into the statement.
//
// The queries read sys.* rather than INFORMATION_SCHEMA. The standard views
// are a subset — they have no identity column, no index, no filegroup — and
// SQL Server's own documentation says to prefer the catalogue views.

// systemDatabases are the four the server makes for itself. They are shown,
// because somebody connecting as an administrator has business in them, but
// marked, so they sort out of the way of their own.
func systemDatabase(id int64) bool { return id <= 4 }

// Root is the databases this login can open.
//
// A database that is offline, restoring or in recovery is left out: it
// cannot be read from, and an expander that turns and then reports an error
// is worse than a name that is not there. HAS_DBACCESS answers for the login
// rather than for the server, so a shared server shows each person theirs.
func (s *sqlServerSource) Root(ctx context.Context) ([]model.Node, error) {
	db, err := s.open(ctx, s.primary)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT name, database_id FROM sys.databases
		WHERE state_desc = 'ONLINE' AND HAS_DBACCESS(name) = 1 ORDER BY name`)
	if err != nil {
		return nil, statementError(err, ctx, "")
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var name string
		var id int64
		if err := rows.Scan(&name, &id); err != nil {
			return nil, err
		}
		node := model.Node{Ref: model.NewRef(model.KindDatabase, name), Label: name, HasChildren: true}
		switch {
		case systemDatabase(id):
			node.Attrs = map[string]string{"system": "true"}
		case name == s.primary:
			node.Attrs = map[string]string{"current": "true"}
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func (s *sqlServerSource) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindDatabase:
		return s.schemas(ctx, ref)
	case model.KindSchema:
		return s.folders(ctx, ref)
	case model.KindFolder:
		return s.folderContents(ctx, ref)
	case model.KindTable, model.KindView:
		if len(ref.Path) < 3 {
			return nil, fmt.Errorf("sqlserver: incomplete reference %s", ref)
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
			out[i] = model.Node{Ref: model.NewRef(model.KindColumn, ref.Path[0], ref.Path[1], ref.Path[2], c.Name),
				Label: c.Name, Attrs: attrs}
		}
		return out, nil
	}
	return nil, nil
}

// schemas lists a database's own schemas.
//
// The fixed ones are left out: sys and INFORMATION_SCHEMA are the catalogue,
// guest is nobody's, and the dozen named after the fixed database roles
// exist so that a role can own something and hold nothing. Those have a
// schema_id of 16384 and up, which is where the server puts them.
func (s *sqlServerSource) schemas(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	db, err := s.pool(ctx, ref)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT name FROM sys.schemas
		WHERE schema_id < 16384 AND name NOT IN ('sys', 'INFORMATION_SCHEMA', 'guest')
		ORDER BY CASE WHEN name = 'dbo' THEN 0 ELSE 1 END, name`)
	if err != nil {
		return nil, statementError(err, ctx, "")
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, model.Node{Ref: model.NewRef(model.KindSchema, ref.Path[0], name),
			Label: name, HasChildren: true})
	}
	return out, rows.Err()
}

// folders is a schema's object classes that hold something (FR-2.2), with
// exact counts, in one round trip.
func (s *sqlServerSource) folders(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 2 {
		return nil, nil
	}
	db, err := s.pool(ctx, ref)
	if err != nil {
		return nil, err
	}
	var tables, views, indexes, routines, triggers, sequences, types int64
	err = db.QueryRowContext(ctx, `DECLARE @s int = SCHEMA_ID(@p1);
		SELECT (SELECT count(*) FROM sys.tables WHERE schema_id = @s),
		       (SELECT count(*) FROM sys.views WHERE schema_id = @s),
		       (SELECT count(*) FROM sys.indexes i JOIN sys.objects o ON o.object_id = i.object_id
		          WHERE o.schema_id = @s AND i.index_id > 0 AND o.type IN ('U', 'V')),
		       (SELECT count(*) FROM sys.objects WHERE schema_id = @s AND type IN ('P', 'FN', 'IF', 'TF')),
		       (SELECT count(*) FROM sys.triggers t JOIN sys.objects o ON o.object_id = t.parent_id
		          WHERE o.schema_id = @s),
		       (SELECT count(*) FROM sys.sequences WHERE schema_id = @s),
		       (SELECT count(*) FROM sys.types WHERE schema_id = @s AND is_user_defined = 1)`,
		ref.Path[1]).Scan(&tables, &views, &indexes, &routines, &triggers, &sequences, &types)
	if err != nil {
		return nil, statementError(err, ctx, "")
	}
	out := model.ClassNodes(ref, map[model.ObjectKind]int64{
		model.KindTable: tables, model.KindView: views, model.KindIndex: indexes,
		model.KindRoutine: routines, model.KindTrigger: triggers,
		model.KindSequence: sequences, model.KindUserType: types,
	})
	if len(out) == 0 {
		// A schema holding nothing still shows its tables, empty: a schema
		// node says it has children, and one that then lists none draws an
		// expander that turns and never opens (FR-2.2).
		out = []model.Node{model.ClassNode(ref, model.KindTable, 0)}
	}
	return out, nil
}

// classQueries list a class's objects as a name, a detail and a row
// estimate. The detail is an index's or a trigger's table, or a routine's
// or a type's kind.
var classQueries = map[model.ObjectKind]string{
	model.KindTable: `SELECT t.name, '', ISNULL((SELECT SUM(p.rows) FROM sys.partitions p
		  WHERE p.object_id = t.object_id AND p.index_id IN (0, 1)), 0)
		FROM sys.tables t WHERE t.schema_id = SCHEMA_ID(@p1) ORDER BY t.name`,
	model.KindView: `SELECT name, '', NULL FROM sys.views
		WHERE schema_id = SCHEMA_ID(@p1) ORDER BY name`,
	model.KindIndex: `SELECT i.name, o.name, NULL FROM sys.indexes i
		JOIN sys.objects o ON o.object_id = i.object_id
		WHERE o.schema_id = SCHEMA_ID(@p1) AND i.index_id > 0 AND o.type IN ('U', 'V')
		ORDER BY i.name, o.name`,
	model.KindRoutine: `SELECT name, CASE type WHEN 'P' THEN 'procedure' ELSE 'function' END, NULL
		FROM sys.objects WHERE schema_id = SCHEMA_ID(@p1) AND type IN ('P', 'FN', 'IF', 'TF')
		ORDER BY name`,
	model.KindTrigger: `SELECT t.name, o.name, NULL FROM sys.triggers t
		JOIN sys.objects o ON o.object_id = t.parent_id
		WHERE o.schema_id = SCHEMA_ID(@p1) ORDER BY t.name`,
	model.KindSequence: `SELECT name, '', NULL FROM sys.sequences
		WHERE schema_id = SCHEMA_ID(@p1) ORDER BY name`,
	model.KindUserType: `SELECT name, CASE WHEN is_table_type = 1 THEN 'table' ELSE 'alias' END, NULL
		FROM sys.types WHERE schema_id = SCHEMA_ID(@p1) AND is_user_defined = 1 ORDER BY name`,
}

func (s *sqlServerSource) folderContents(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 3 {
		return nil, nil
	}
	kind, ok := model.ClassOf(ref)
	if !ok {
		return nil, nil // a folder that is no class lists nothing
	}
	q, ok := classQueries[kind]
	if !ok {
		return nil, fmt.Errorf("sqlserver: no %s class", kind)
	}
	db, err := s.pool(ctx, ref)
	if err != nil {
		return nil, err
	}
	database, schema := ref.Path[0], ref.Path[1]
	rows, err := db.QueryContext(ctx, q, schema)
	if err != nil {
		return nil, statementError(err, ctx, "")
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var name, detail string
		var est sql.NullInt64
		if err := rows.Scan(&name, &detail, &est); err != nil {
			return nil, err
		}
		n := model.Node{Ref: model.NewRef(kind, database, schema, name), Label: name}
		switch kind {
		case model.KindTable, model.KindView:
			n.HasChildren, n.Browsable = true, true
			if est.Valid {
				// Partition row counts, which the server keeps as it writes:
				// exact unless something is being written as this is read.
				n.Badge = &model.Badge{Text: humanCount(est.Int64)}
			}
		case model.KindIndex, model.KindTrigger:
			// Named per table: two tables may each have a PK_id.
			n.Ref = model.NewRef(kind, database, schema, detail, name)
			n.Label, n.Attrs = model.OnTable(name, detail), map[string]string{"table": detail}
		case model.KindRoutine, model.KindUserType:
			n.Attrs = map[string]string{"kind": detail}
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// columns reads a table's or view's columns, with the type as somebody
// would write it and whether the column is part of the primary key.
func (s *sqlServerSource) columns(ctx context.Context, ref model.ObjectRef) ([]model.Column, error) {
	db, err := s.pool(ctx, ref)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT c.name, TYPE_NAME(c.user_type_id), c.max_length, c.precision,
		  c.scale, c.is_nullable, c.is_identity, c.column_id, ISNULL(d.definition, ''),
		  ISNULL(cc.definition, ''), CASE WHEN d.object_id IS NULL THEN 0 ELSE 1 END,
		  ISNULL(CAST(ep.value AS nvarchar(max)), ''),
		  CASE WHEN EXISTS (SELECT 1 FROM sys.indexes i
		      JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
		      WHERE i.object_id = c.object_id AND i.is_primary_key = 1 AND ic.column_id = c.column_id)
		    THEN 1 ELSE 0 END
		FROM sys.columns c
		JOIN sys.objects o ON o.object_id = c.object_id
		LEFT JOIN sys.default_constraints d ON d.object_id = c.default_object_id
		LEFT JOIN sys.computed_columns cc ON cc.object_id = c.object_id AND cc.column_id = c.column_id
		LEFT JOIN sys.extended_properties ep ON ep.major_id = c.object_id AND ep.minor_id = c.column_id
		  AND ep.class = 1 AND ep.name = 'MS_Description'
		WHERE o.schema_id = SCHEMA_ID(@p1) AND o.name = @p2
		ORDER BY c.column_id`, ref.Path[1], ref.Path[2])
	if err != nil {
		return nil, statementError(err, ctx, "")
	}
	defer rows.Close()
	var out []model.Column
	for rows.Next() {
		var name, typ, deflt, computed, comment string
		var maxLen, precision, scale int64
		var nullable, identity, hasDefault, primary bool
		var position int
		if err := rows.Scan(&name, &typ, &maxLen, &precision, &scale, &nullable, &identity,
			&position, &deflt, &computed, &hasDefault, &comment, &primary); err != nil {
			return nil, err
		}
		dt := dataType(typ)
		dt.Native, dt.Nullable = nativeType(typ, maxLen, precision, scale), nullable
		dt.Length, dt.Precision, dt.Scale = declaredLength(typ, maxLen), int32(precision), int32(scale)
		c := model.Column{Name: name, Type: dt, Position: position, Comment: comment,
			Default: trimDefault(deflt), HasDefault: hasDefault,
			Identity: identity, AutoIncrement: identity, Generated: trimDefault(computed)}
		if primary {
			c.Attrs = map[string]string{"key": "primary"}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// trimDefault strips the brackets the server wraps a stored expression in:
// it keeps ((0)) for what was written as 0, and shows it back that way.
func trimDefault(def string) string {
	for len(def) > 1 && def[0] == '(' && def[len(def)-1] == ')' && balanced(def[1:len(def)-1]) {
		def = def[1 : len(def)-1]
	}
	return def
}

// balanced reports whether every bracket in s is closed within it, so that
// the outer pair of "(a) + (b)" is not mistaken for a wrapper.
func balanced(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

// Describe loads a table's structure (*model.Table) or a view's
// (*model.View).
func (s *sqlServerSource) Describe(ctx context.Context, ref model.ObjectRef) (any, error) {
	if !browsableKinds[ref.Kind] || len(ref.Path) < 3 {
		return nil, fmt.Errorf("sqlserver: cannot describe %s", ref)
	}
	db, err := s.pool(ctx, ref)
	if err != nil {
		return nil, err
	}
	cols, err := s.columns(ctx, ref)
	if err != nil {
		return nil, err
	}
	schema, name := ref.Path[1], ref.Path[2]
	if ref.Kind == model.KindView {
		// From sys.views outwards, so that a view which is not there is no
		// rows rather than an empty definition: a description of nothing
		// would be shown as a view with no columns.
		var def sql.NullString
		if err := db.QueryRowContext(ctx, `SELECT m.definition FROM sys.views v
			LEFT JOIN sys.sql_modules m ON m.object_id = v.object_id
			WHERE v.schema_id = SCHEMA_ID(@p1) AND v.name = @p2`, schema, name).Scan(&def); err != nil {
			return nil, statementError(err, ctx, "")
		}
		return &model.View{Name: name, Columns: cols, Definition: def.String}, nil
	}
	t := &model.Table{Name: name, Columns: cols, RowsEstimate: -1}
	var est sql.NullInt64
	var comment sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT
		  (SELECT SUM(p.rows) FROM sys.partitions p WHERE p.object_id = t.object_id AND p.index_id IN (0, 1)),
		  CAST(ep.value AS nvarchar(max))
		FROM sys.tables t
		LEFT JOIN sys.extended_properties ep ON ep.major_id = t.object_id AND ep.minor_id = 0
		  AND ep.class = 1 AND ep.name = 'MS_Description'
		WHERE t.schema_id = SCHEMA_ID(@p1) AND t.name = @p2`, schema, name).Scan(&est, &comment); err != nil {
		return nil, statementError(err, ctx, "")
	}
	t.Comment = comment.String
	if est.Valid {
		t.RowsEstimate = est.Int64
	}
	if t.Indexes, t.PrimaryKey, t.Uniques, err = s.indexes(ctx, ref); err != nil {
		return nil, err
	}
	if t.ForeignKeys, err = s.foreignKeys(ctx, ref); err != nil {
		return nil, err
	}
	if t.Checks, err = s.checks(ctx, ref); err != nil {
		return nil, err
	}
	return t, nil
}

// indexes reads a table's indexes, telling the three things apart that SQL
// Server stores as one: the primary key, a unique constraint and an index.
func (s *sqlServerSource) indexes(ctx context.Context, ref model.ObjectRef) ([]model.Index, *model.PrimaryKey, []model.UniqueConstraint, error) {
	db, err := s.pool(ctx, ref)
	if err != nil {
		return nil, nil, nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT i.name, i.is_unique, i.is_primary_key, i.is_unique_constraint,
		  LOWER(i.type_desc), c.name, ic.is_descending_key, ic.is_included_column, ISNULL(i.filter_definition, '')
		FROM sys.indexes i
		JOIN sys.objects o ON o.object_id = i.object_id
		JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
		JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
		WHERE o.schema_id = SCHEMA_ID(@p1) AND o.name = @p2 AND i.index_id > 0
		ORDER BY i.index_id, ic.key_ordinal`, ref.Path[1], ref.Path[2])
	if err != nil {
		return nil, nil, nil, statementError(err, ctx, "")
	}
	defer rows.Close()
	var ixs []model.Index
	var pk *model.PrimaryKey
	var uniques []model.UniqueConstraint
	for rows.Next() {
		var name, method, col, filter string
		var unique, primary, constraint, descending, included bool
		if err := rows.Scan(&name, &unique, &primary, &constraint, &method, &col,
			&descending, &included, &filter); err != nil {
			return nil, nil, nil, err
		}
		switch {
		case primary:
			if pk == nil {
				pk = &model.PrimaryKey{Name: name}
			}
			pk.Columns = append(pk.Columns, col)
		case constraint:
			if len(uniques) == 0 || uniques[len(uniques)-1].Name != name {
				uniques = append(uniques, model.UniqueConstraint{Name: name})
			}
			u := &uniques[len(uniques)-1]
			u.Columns = append(u.Columns, col)
		default:
			if len(ixs) == 0 || ixs[len(ixs)-1].Name != name {
				ixs = append(ixs, model.Index{Name: name, Unique: unique, Method: method, Predicate: filter})
			}
			ix := &ixs[len(ixs)-1]
			if included {
				ix.Include = append(ix.Include, col)
			} else {
				ix.Columns = append(ix.Columns, model.IndexColumn{Name: col, Descending: descending})
			}
		}
	}
	return ixs, pk, uniques, rows.Err()
}

func (s *sqlServerSource) foreignKeys(ctx context.Context, ref model.ObjectRef) ([]model.ForeignKey, error) {
	db, err := s.pool(ctx, ref)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT fk.name, pc.name, rs.name, rt.name, rc.name,
		  fk.update_referential_action_desc, fk.delete_referential_action_desc
		FROM sys.foreign_keys fk
		JOIN sys.objects po ON po.object_id = fk.parent_object_id
		JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
		JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id
		JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id
		JOIN sys.objects rt ON rt.object_id = fk.referenced_object_id
		JOIN sys.schemas rs ON rs.schema_id = rt.schema_id
		WHERE po.schema_id = SCHEMA_ID(@p1) AND po.name = @p2
		ORDER BY fk.name, fkc.constraint_column_id`, ref.Path[1], ref.Path[2])
	if err != nil {
		return nil, statementError(err, ctx, "")
	}
	defer rows.Close()
	var out []model.ForeignKey
	for rows.Next() {
		var name, col, refSchema, refTable, refCol, onUpdate, onDelete string
		if err := rows.Scan(&name, &col, &refSchema, &refTable, &refCol, &onUpdate, &onDelete); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Name != name {
			out = append(out, model.ForeignKey{Name: name, RefSchema: refSchema, RefTable: refTable,
				OnUpdate: referentialAction(onUpdate), OnDelete: referentialAction(onDelete)})
		}
		fk := &out[len(out)-1]
		fk.Columns = append(fk.Columns, col)
		fk.RefColumns = append(fk.RefColumns, refCol)
	}
	return out, rows.Err()
}

// referentialAction reads the server's spelling: NO_ACTION, SET_NULL.
func referentialAction(desc string) model.ReferentialAction {
	return model.ReferentialAction(strings.ReplaceAll(desc, "_", " "))
}

func (s *sqlServerSource) checks(ctx context.Context, ref model.ObjectRef) ([]model.CheckConstraint, error) {
	db, err := s.pool(ctx, ref)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT cc.name, cc.definition FROM sys.check_constraints cc
		JOIN sys.objects o ON o.object_id = cc.parent_object_id
		WHERE o.schema_id = SCHEMA_ID(@p1) AND o.name = @p2 ORDER BY cc.name`, ref.Path[1], ref.Path[2])
	if err != nil {
		return nil, statementError(err, ctx, "")
	}
	defer rows.Close()
	var out []model.CheckConstraint
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			return nil, err
		}
		out = append(out, model.CheckConstraint{Name: name, Expression: trimDefault(def)})
	}
	return out, rows.Err()
}

// Badge is a table's row count (FR-2.5), which the server keeps in the
// partition it writes to rather than working out when asked.
func (s *sqlServerSource) Badge(ctx context.Context, ref model.ObjectRef) (model.Badge, bool, error) {
	if ref.Kind != model.KindTable || len(ref.Path) < 3 {
		return model.Badge{}, false, nil
	}
	db, err := s.pool(ctx, ref)
	if err != nil {
		return model.Badge{}, false, err
	}
	var n sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT SUM(p.rows) FROM sys.partitions p
		JOIN sys.tables t ON t.object_id = p.object_id
		WHERE t.schema_id = SCHEMA_ID(@p1) AND t.name = @p2 AND p.index_id IN (0, 1)`,
		ref.Path[1], ref.Path[2]).Scan(&n)
	if err != nil || !n.Valid {
		return model.Badge{}, false, statementError(err, ctx, "")
	}
	return model.Badge{Text: humanCount(n.Int64)}, true, nil
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
	s := strconv.FormatFloat(f, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}
