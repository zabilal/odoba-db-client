package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

const (
	folderTables = "tables"
	folderViews  = "views"
)

var systemSchemas = map[string]bool{"information_schema": true, "mysql": true, "performance_schema": true, "sys": true}

// Root is the server's databases. Each says whether it holds anything, so an
// empty one shows no expander instead of one that opens onto nothing.
func (s *mysqlSource) Root(ctx context.Context) ([]model.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.SCHEMA_NAME, COUNT(t.TABLE_NAME)
		FROM information_schema.SCHEMATA s
		LEFT JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = s.SCHEMA_NAME
		GROUP BY s.SCHEMA_NAME ORDER BY s.SCHEMA_NAME`)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		node := model.Node{Ref: model.NewRef(model.KindDatabase, name), Label: name, HasChildren: n > 0}
		switch {
		case systemSchemas[name]:
			node.Attrs = map[string]string{"system": "true"}
		case name == s.cfg.Database:
			node.Attrs = map[string]string{"current": "true"}
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func (s *mysqlSource) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindDatabase:
		return s.folders(ctx, ref.Path[0])
	case model.KindFolder:
		if len(ref.Path) < 2 {
			return nil, fmt.Errorf("mysql: incomplete reference %s", ref)
		}
		return s.objects(ctx, ref.Path[0], ref.Path[1])
	case model.KindTable, model.KindView:
		if len(ref.Path) < 2 {
			return nil, fmt.Errorf("mysql: incomplete reference %s", ref)
		}
		cols, err := s.columns(ctx, ref.Path[0], ref.Path[1])
		if err != nil {
			return nil, err
		}
		out := make([]model.Node, len(cols))
		for i, c := range cols {
			out[i] = model.Node{Ref: model.NewRef(model.KindColumn, ref.Path[0], ref.Path[1], c.Name), Label: c.Name,
				Attrs: map[string]string{"type": c.Type.Native}}
		}
		return out, nil
	}
	return nil, nil
}

func (s *mysqlSource) folders(ctx context.Context, db string) ([]model.Node, error) {
	var tables, views int64
	err := s.db.QueryRowContext(ctx, `SELECT
		  COALESCE(SUM(TABLE_TYPE = 'BASE TABLE'), 0),
		  COALESCE(SUM(TABLE_TYPE IN ('VIEW', 'SYSTEM VIEW')), 0)
		FROM information_schema.TABLES WHERE TABLE_SCHEMA = ?`, db).Scan(&tables, &views)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	var out []model.Node
	for _, f := range []struct {
		key, label string
		n          int64
	}{{folderTables, "Tables", tables}, {folderViews, "Views", views}} {
		if f.n > 0 {
			out = append(out, model.Node{Ref: model.NewRef(model.KindFolder, db, f.key), Label: f.label,
				HasChildren: true, Badge: &model.Badge{Text: strconv.FormatInt(f.n, 10), Exact: true}})
		}
	}
	return out, nil
}

func (s *mysqlSource) objects(ctx context.Context, db, folder string) ([]model.Node, error) {
	types, kind := "'BASE TABLE'", model.KindTable
	if folder == folderViews {
		types, kind = "'VIEW', 'SYSTEM VIEW'", model.KindView
	}
	rows, err := s.db.QueryContext(ctx, `SELECT TABLE_NAME, TABLE_ROWS FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_TYPE IN (`+types+`) ORDER BY TABLE_NAME`, db)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var name string
		var est sql.NullInt64
		if err := rows.Scan(&name, &est); err != nil {
			return nil, err
		}
		n := model.Node{Ref: model.NewRef(kind, db, name), Label: name, HasChildren: true, Browsable: true}
		if kind == model.KindTable && est.Valid {
			n.Badge = &model.Badge{Text: compact(est.Int64)} // statistics, not a count
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *mysqlSource) columns(ctx context.Context, db, table string) ([]model.Column, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COLUMN_NAME, DATA_TYPE, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT,
		  EXTRA, COLUMN_COMMENT, ORDINAL_POSITION, COALESCE(GENERATION_EXPRESSION, '')
		FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION`, db, table)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.Column
	for rows.Next() {
		var name, dataTyp, colType, nullable, extra, comment, gen string
		var dflt sql.NullString
		var pos int
		if err := rows.Scan(&name, &dataTyp, &colType, &nullable, &dflt, &extra, &comment, &pos, &gen); err != nil {
			return nil, err
		}
		dt := dataType(dataTyp)
		dt.Native, dt.Nullable = colType, nullable == "YES"
		out = append(out, model.Column{Name: name, Type: dt, Position: pos, Default: dflt.String, HasDefault: dflt.Valid,
			AutoIncrement: contains(extra, "auto_increment"), Generated: gen, Comment: comment})
	}
	return out, rows.Err()
}

// Describe loads a table's structure (*model.Table) or a view's
// (*model.View).
func (s *mysqlSource) Describe(ctx context.Context, ref model.ObjectRef) (any, error) {
	if (ref.Kind != model.KindTable && ref.Kind != model.KindView) || len(ref.Path) < 2 {
		return nil, fmt.Errorf("mysql: cannot describe %s", ref)
	}
	db, name := ref.Path[0], ref.Path[1]
	cols, err := s.columns(ctx, db, name)
	if err != nil {
		return nil, err
	}
	if ref.Kind == model.KindView {
		var def sql.NullString
		if err := s.db.QueryRowContext(ctx, `SELECT VIEW_DEFINITION FROM information_schema.VIEWS
			WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, db, name).Scan(&def); err != nil {
			return nil, statementError(err, ctx)
		}
		return &model.View{Name: name, Columns: cols, Definition: def.String}, nil
	}
	t := &model.Table{Name: name, Columns: cols, RowsEstimate: -1}
	var est sql.NullInt64
	var comment string
	if err := s.db.QueryRowContext(ctx, `SELECT TABLE_ROWS, TABLE_COMMENT FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, db, name).Scan(&est, &comment); err == nil {
		t.Comment = comment
		if est.Valid {
			t.RowsEstimate = est.Int64
		}
	}
	if t.Indexes, t.PrimaryKey, err = s.indexes(ctx, db, name); err != nil {
		return nil, statementError(err, ctx)
	}
	if t.ForeignKeys, err = s.foreignKeys(ctx, db, name); err != nil {
		return nil, statementError(err, ctx)
	}
	return t, nil
}

func (s *mysqlSource) indexes(ctx context.Context, db, table string) ([]model.Index, *model.PrimaryKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT INDEX_NAME, NON_UNIQUE, COALESCE(COLUMN_NAME, ''),
		  COALESCE(COLLATION, 'A'), INDEX_TYPE, COALESCE(EXPRESSION, '')
		FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
		ORDER BY INDEX_NAME, SEQ_IN_INDEX`, db, table)
	if err != nil {
		// MariaDB's STATISTICS has no EXPRESSION column (REQ-DB-2).
		rows, err = s.db.QueryContext(ctx, `SELECT INDEX_NAME, NON_UNIQUE, COALESCE(COLUMN_NAME, ''),
			  COALESCE(COLLATION, 'A'), INDEX_TYPE, ''
			FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
			ORDER BY INDEX_NAME, SEQ_IN_INDEX`, db, table)
		if err != nil {
			return nil, nil, err
		}
	}
	defer rows.Close()
	var out []model.Index
	var pk *model.PrimaryKey
	for rows.Next() {
		var name, col, coll, method, expr string
		var nonUnique int
		if err := rows.Scan(&name, &nonUnique, &col, &coll, &method, &expr); err != nil {
			return nil, nil, err
		}
		if name == "PRIMARY" {
			if pk == nil {
				pk = &model.PrimaryKey{Name: name}
			}
			pk.Columns = append(pk.Columns, col)
			continue
		}
		if len(out) == 0 || out[len(out)-1].Name != name {
			out = append(out, model.Index{Name: name, Unique: nonUnique == 0, Method: lower(method)})
		}
		ix := &out[len(out)-1]
		ix.Columns = append(ix.Columns, model.IndexColumn{Name: col, Descending: coll == "D", Expression: expr})
	}
	return out, pk, rows.Err()
}

func (s *mysqlSource) foreignKeys(ctx context.Context, db, table string) ([]model.ForeignKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT k.CONSTRAINT_NAME, k.COLUMN_NAME, k.REFERENCED_TABLE_SCHEMA,
		  k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME, r.UPDATE_RULE, r.DELETE_RULE
		FROM information_schema.KEY_COLUMN_USAGE k
		JOIN information_schema.REFERENTIAL_CONSTRAINTS r
		  ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME
		  AND r.TABLE_NAME = k.TABLE_NAME
		WHERE k.TABLE_SCHEMA = ? AND k.TABLE_NAME = ? AND k.REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY k.CONSTRAINT_NAME, k.ORDINAL_POSITION`, db, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.ForeignKey
	for rows.Next() {
		var name, col, refDB, refTable, refCol, onUpdate, onDelete string
		if err := rows.Scan(&name, &col, &refDB, &refTable, &refCol, &onUpdate, &onDelete); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Name != name {
			out = append(out, model.ForeignKey{Name: name, RefSchema: refDB, RefTable: refTable,
				OnUpdate: model.ReferentialAction(onUpdate), OnDelete: model.ReferentialAction(onDelete)})
		}
		fk := &out[len(out)-1]
		fk.Columns = append(fk.Columns, col)
		fk.RefColumns = append(fk.RefColumns, refCol)
	}
	return out, rows.Err()
}

// Badge is the table statistics' row estimate (FR-2.5): cheap, and marked
// as an estimate, because InnoDB's is one.
func (s *mysqlSource) Badge(ctx context.Context, ref model.ObjectRef) (model.Badge, bool, error) {
	if ref.Kind != model.KindTable || len(ref.Path) < 2 {
		return model.Badge{}, false, nil
	}
	var est sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT TABLE_ROWS FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`, ref.Path[0], ref.Path[1]).Scan(&est); err != nil || !est.Valid {
		return model.Badge{}, false, err
	}
	return model.Badge{Text: compact(est.Int64)}, true, nil
}

// compact writes a count the way a badge has room for: 12, 3.4k, 1.2M.
func compact(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10)
	case n < 1_000_000:
		return strconv.FormatFloat(float64(n)/1e3, 'f', 1, 64) + "k"
	case n < 1_000_000_000:
		return strconv.FormatFloat(float64(n)/1e6, 'f', 1, 64) + "M"
	}
	return strconv.FormatFloat(float64(n)/1e9, 'f', 1, 64) + "B"
}
