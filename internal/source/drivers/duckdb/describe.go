//go:build duckdb

package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	strs "strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Describe loads an object's whole structure (FR-2.4).
func (s *duckSource) Describe(ctx context.Context, ref model.ObjectRef) (any, error) {
	if len(ref.Path) < 3 {
		return nil, fmt.Errorf("duckdb: incomplete reference %s", ref)
	}
	switch ref.Kind {
	case model.KindTable:
		return s.describeTable(ctx, ref)
	case model.KindView:
		return s.describeView(ctx, ref)
	}
	return nil, fmt.Errorf("duckdb: %s cannot be described", ref.Kind)
}

func (s *duckSource) describeTable(ctx context.Context, ref model.ObjectRef) (*model.Table, error) {
	info, err := s.tableInfo(ctx, ref)
	if err != nil {
		return nil, err
	}
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	t := &model.Table{Name: ref.Path[2], Columns: info.columns, RowsEstimate: -1}

	var comment sql.NullString
	var estimate sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT comment, estimated_size FROM duckdb_tables()
		WHERE database_name = ? AND schema_name = ? AND table_name = ?`,
		ref.Path[0], ref.Path[1], ref.Path[2]).Scan(&comment, &estimate)
	if err != nil && err != sql.ErrNoRows {
		return nil, statementError(err, ctx)
	}
	t.Comment = comment.String
	if estimate.Valid {
		t.RowsEstimate = estimate.Int64
	}

	if err := s.constraints(ctx, ref, t); err != nil {
		return nil, err
	}
	if t.Indexes, err = s.indexes(ctx, ref); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *duckSource) describeView(ctx context.Context, ref model.ObjectRef) (*model.View, error) {
	info, err := s.tableInfo(ctx, ref)
	if err != nil {
		return nil, err
	}
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	v := &model.View{Name: ref.Path[2], Columns: info.columns}
	var definition, comment sql.NullString
	err = db.QueryRowContext(ctx, `SELECT sql, comment FROM duckdb_views()
		WHERE database_name = ? AND schema_name = ? AND view_name = ?`,
		ref.Path[0], ref.Path[1], ref.Path[2]).Scan(&definition, &comment)
	if err != nil && err != sql.ErrNoRows {
		return nil, statementError(err, ctx)
	}
	v.Definition, v.Comment = definition.String, comment.String
	return v, nil
}

// constraints fills in the key, the foreign keys, the uniques and the
// checks.
//
// DuckDB keeps a NOT NULL as a constraint of its own, which is a column's
// business rather than a rule over the row, and it is left where it is:
// the column already says it cannot hold nothing.
func (s *duckSource) constraints(ctx context.Context, ref model.ObjectRef, t *model.Table) error {
	db, err := s.conn()
	if err != nil {
		return err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT constraint_type, constraint_text, COALESCE(constraint_name, ''),
		       constraint_column_names,
		       COALESCE(referenced_table, ''), referenced_column_names
		FROM duckdb_constraints()
		WHERE database_name = ? AND schema_name = ? AND table_name = ?
		  AND constraint_type <> 'NOT NULL'`,
		ref.Path[0], ref.Path[1], ref.Path[2])
	if err != nil {
		return statementError(err, ctx)
	}
	defer rows.Close()

	for rows.Next() {
		var kind, text, name, refTable string
		var cols, refCols []any
		if err := rows.Scan(&kind, &text, &name, &cols, &refTable, &refCols); err != nil {
			return statementError(err, ctx)
		}
		switch kind {
		case "PRIMARY KEY":
			t.PrimaryKey = &model.PrimaryKey{Name: name, Columns: nameList(cols)}
		case "UNIQUE":
			t.Uniques = append(t.Uniques, model.UniqueConstraint{Name: name, Columns: nameList(cols)})
		case "FOREIGN KEY":
			// DuckDB keeps no action for a foreign key: it refuses a
			// change that would break one rather than following it, so
			// there is nothing to read and nothing to report but the
			// standard's own default.
			t.ForeignKeys = append(t.ForeignKeys, model.ForeignKey{
				Name: name, Columns: nameList(cols), RefSchema: ref.Path[1], RefTable: refTable,
				RefColumns: nameList(refCols), OnDelete: model.ActionNoAction,
				OnUpdate: model.ActionNoAction,
			})
		default:
			// The query above says which kinds are wanted; this sorts
			// what it asked for. Naming them again here would be a
			// second filter for the same thing, and then neither could
			// be shown to matter.
			t.Checks = append(t.Checks, model.CheckConstraint{Name: name, Expression: text})
		}
	}
	return statementError(rows.Err(), ctx)
}

// indexed reads the columns an index is over out of the one piece of
// text the catalogue writes them as: [a, b].
func indexed(exprs string) []string {
	exprs = strs.TrimSuffix(strs.TrimPrefix(strs.TrimSpace(exprs), "["), "]")
	if strs.TrimSpace(exprs) == "" {
		return nil
	}
	parts := strs.Split(exprs, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strs.TrimSpace(p))
	}
	return out
}

// nameList reads a list of names out of what the library hands a list
// over as.
func nameList(vals []any) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// indexes reads a table's indexes.
//
// Only the ones somebody made: a key here is a constraint and is not an
// index this catalogue lists, so an index in this list is one that was
// declared.
func (s *duckSource) indexes(ctx context.Context, ref model.ObjectRef) ([]model.Index, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT index_name, is_unique, COALESCE(expressions, ''), COALESCE(comment, '')
		FROM duckdb_indexes()
		WHERE database_name = ? AND schema_name = ? AND table_name = ?
		ORDER BY index_name`, ref.Path[0], ref.Path[1], ref.Path[2])
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()

	var out []model.Index
	for rows.Next() {
		var name, exprs, comment string
		var unique bool
		if err := rows.Scan(&name, &unique, &exprs, &comment); err != nil {
			return nil, statementError(err, ctx)
		}
		// The catalogue writes what an index is over as one piece of
		// text, a list in brackets, rather than as a row apiece.
		ix := model.Index{Name: name, Unique: unique, Method: "art"}
		for _, e := range indexed(exprs) {
			ix.Columns = append(ix.Columns, model.IndexColumn{Name: e})
		}
		if comment != "" {
			ix.Attrs = map[string]string{"comment": comment}
		}
		out = append(out, ix)
	}
	return out, statementError(rows.Err(), ctx)
}
