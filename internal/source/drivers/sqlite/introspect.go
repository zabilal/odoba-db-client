package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// userObjects excludes SQLite's own tables (sqlite_schema, sqlite_sequence,
// sqlite_stat1) and the indexes it makes for keys (sqlite_autoindex_…):
// internals, not the user's.
const userObjects = `name NOT LIKE 'sqlite\_%' ESCAPE '\'`

// schemaTypes is the sqlite_schema type each class lists.
var schemaTypes = map[model.ObjectKind]string{
	model.KindTable: "table", model.KindView: "view", model.KindIndex: "index", model.KindTrigger: "trigger",
}

// Root is the database's object classes (FR-2.2); a file is one database.
func (s *sqliteSource) Root(ctx context.Context) ([]model.Node, error) {
	counts := map[model.ObjectKind]int64{}
	rows, err := s.db.QueryContext(ctx, `SELECT type, count(*) FROM sqlite_schema
		WHERE `+userObjects+` GROUP BY type`)
	if err != nil {
		return nil, statementError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		var n int64
		if err := rows.Scan(&typ, &n); err != nil {
			return nil, err
		}
		for k, t := range schemaTypes {
			if t == typ {
				counts[k] = n
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Empty classes are noise, as the other drivers agree; but an empty
	// database still shows its Tables, so the tree does not look broken.
	db := model.NewRef(model.KindDatabase, "main")
	out := model.ClassNodes(db, counts)
	if len(out) == 0 {
		out = []model.Node{model.ClassNode(db, model.KindTable, 0)}
	}
	return out, nil
}

func (s *sqliteSource) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindFolder:
		kind, _ := model.ClassOf(ref)
		typ, ok := schemaTypes[kind]
		if !ok {
			return nil, fmt.Errorf("sqlite: no such class %s", ref)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT name, tbl_name FROM sqlite_schema
			WHERE type = ? AND `+userObjects+` ORDER BY name COLLATE NOCASE`, typ)
		if err != nil {
			return nil, statementError(err)
		}
		defer rows.Close()
		var out []model.Node
		for rows.Next() {
			var name, table string
			if err := rows.Scan(&name, &table); err != nil {
				return nil, err
			}
			n := model.Node{Ref: model.NewRef(kind, "main", name), Label: name}
			if kind == model.KindTable || kind == model.KindView {
				n.HasChildren, n.Browsable = true, true
			} else { // an index or a trigger, named with its table
				n.Label, n.Attrs = model.OnTable(name, table), map[string]string{"table": table}
			}
			out = append(out, n)
		}
		return out, rows.Err()
	case model.KindTable, model.KindView:
		cols, err := s.columns(ctx, ref.Name())
		if err != nil {
			return nil, err
		}
		out := make([]model.Node, len(cols))
		for i, c := range cols {
			out[i] = model.Node{Ref: model.NewRef(model.KindColumn, "main", ref.Name(), c.Name), Label: c.Name,
				Attrs: map[string]string{"type": c.Type.Native}}
		}
		return out, nil
	}
	return nil, nil
}

// columns reads a table's or view's columns. Hidden columns of virtual
// tables are left out; generated columns are kept and marked.
func (s *sqliteSource) columns(ctx context.Context, table string) ([]model.Column, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT cid, name, type, "notnull", dflt_value, hidden
		FROM pragma_table_xinfo(?) ORDER BY cid`, table)
	if err != nil {
		return nil, statementError(err)
	}
	defer rows.Close()
	var out []model.Column
	for rows.Next() {
		var cid, notnull, hidden int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &hidden); err != nil {
			return nil, err
		}
		if hidden == 1 {
			continue
		}
		dt := dataType(typ)
		dt.Nullable = notnull == 0
		c := model.Column{Name: name, Type: dt, Position: cid + 1, Default: dflt.String, HasDefault: dflt.Valid}
		if hidden == 2 || hidden == 3 {
			c.Generated = "generated" // SQLite does not return the expression here
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Describe loads an object's structure: for a table its columns, primary
// key, indexes, unique constraints and foreign keys (*model.Table); for a view
// its columns and definition (*model.View).
func (s *sqliteSource) Describe(ctx context.Context, ref model.ObjectRef) (any, error) {
	if ref.Kind != model.KindTable && ref.Kind != model.KindView {
		return nil, fmt.Errorf("sqlite: cannot describe %s", ref)
	}
	name := ref.Name()
	cols, err := s.columns(ctx, name)
	if err != nil {
		return nil, err
	}
	if ref.Kind == model.KindView {
		var def sql.NullString
		if err := s.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type = 'view' AND name = ?`,
			name).Scan(&def); err != nil {
			return nil, statementError(err)
		}
		return &model.View{Name: name, Columns: cols, Definition: def.String}, nil
	}
	t := &model.Table{Name: name, Columns: cols, RowsEstimate: -1}
	if pk, err := s.primaryKey(ctx, name); err != nil {
		return nil, statementError(err)
	} else if len(pk) > 0 {
		t.PrimaryKey = &model.PrimaryKey{Columns: pk}
	}
	if t.Indexes, t.Uniques, err = s.indexes(ctx, name); err != nil {
		return nil, statementError(err)
	}
	if t.ForeignKeys, err = s.foreignKeys(ctx, name); err != nil {
		return nil, statementError(err)
	}
	return t, nil
}

// indexes reads a table's indexes. Those SQLite made for a UNIQUE constraint
// (origin "u") are reported as the constraint; the one behind the primary
// key (origin "pk") is the key itself, so it is left out.
func (s *sqliteSource) indexes(ctx context.Context, table string) ([]model.Index, []model.UniqueConstraint, error) {
	type entry struct {
		name, origin string
		unique       bool
		partial      bool
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, "unique", origin, partial FROM pragma_index_list(?) ORDER BY seq DESC`, table)
	if err != nil {
		return nil, nil, err
	}
	var list []entry
	for rows.Next() {
		var e entry
		var unique, partial int
		if err := rows.Scan(&e.name, &unique, &e.origin, &partial); err != nil {
			rows.Close()
			return nil, nil, err
		}
		e.unique, e.partial = unique == 1, partial == 1
		list = append(list, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var idx []model.Index
	var uniq []model.UniqueConstraint
	for _, e := range list {
		members, err := s.indexColumns(ctx, e.name)
		if err != nil {
			return nil, nil, err
		}
		switch e.origin {
		case "pk":
			continue
		case "u":
			names := make([]string, len(members))
			for i, m := range members {
				names[i] = m.Name
			}
			uniq = append(uniq, model.UniqueConstraint{Name: e.name, Columns: names})
		default:
			ix := model.Index{Name: e.name, Columns: members, Unique: e.unique, Method: "btree"}
			if e.partial {
				ix.Attrs = map[string]string{"partial": "true"}
			}
			idx = append(idx, ix)
		}
	}
	return idx, uniq, nil
}

// indexColumns reads an index's key columns in order. A member on an
// expression has no name (cid -2); it is reported as an expression.
func (s *sqliteSource) indexColumns(ctx context.Context, index string) ([]model.IndexColumn, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT cid, name, "desc" FROM pragma_index_xinfo(?) WHERE key = 1 ORDER BY seqno`, index)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.IndexColumn
	for rows.Next() {
		var cid, desc int
		var name sql.NullString
		if err := rows.Scan(&cid, &name, &desc); err != nil {
			return nil, err
		}
		c := model.IndexColumn{Name: name.String, Descending: desc == 1}
		if cid == -2 {
			c.Name, c.Expression = "", "expression"
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// foreignKeys reads a table's foreign keys, one per constraint however many
// columns it spans.
func (s *sqliteSource) foreignKeys(ctx context.Context, table string) ([]model.ForeignKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, "table", "from", "to", on_update, on_delete
		FROM pragma_foreign_key_list(?) ORDER BY id, seq`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ForeignKey
	last := -1
	for rows.Next() {
		var id int
		var ref, from, onUpdate, onDelete string
		var to sql.NullString // NULL when the key refers to the parent's primary key implicitly
		if err := rows.Scan(&id, &ref, &from, &to, &onUpdate, &onDelete); err != nil {
			return nil, err
		}
		if id != last {
			out = append(out, model.ForeignKey{RefSchema: "main", RefTable: ref,
				OnUpdate: model.ReferentialAction(onUpdate), OnDelete: model.ReferentialAction(onDelete)})
			last = id
		}
		fk := &out[len(out)-1]
		fk.Columns = append(fk.Columns, from)
		fk.RefColumns = append(fk.RefColumns, to.String)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close() // before the next statement: a database may hold one connection
	for i := range out {
		if err := s.implicitKey(ctx, &out[i]); err != nil { // referrers.go
			return nil, err
		}
	}
	return out, nil
}

// Badge has nothing to add: SQLite keeps no row estimates unless ANALYZE has
// run, and an exact count is what the grid's own count is for.
func (s *sqliteSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}
