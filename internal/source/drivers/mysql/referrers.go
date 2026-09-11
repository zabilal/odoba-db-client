package mysql

import (
	"context"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Referrers lists the foreign keys of other tables that refer to a table,
// each with the table it is in (FR-3.11, ADR-0041): information_schema
// read by the table referred to, rather than by the table the key is in.
func (s *mysqlSource) Referrers(ctx context.Context, ref model.ObjectRef) ([]model.Referrer, error) {
	if ref.Kind != model.KindTable || len(ref.Path) < 2 {
		return nil, fmt.Errorf("mysql: cannot list what refers to %s", ref)
	}
	db, name := ref.Path[0], ref.Path[1]
	rows, err := s.db.QueryContext(ctx, `SELECT k.TABLE_SCHEMA, k.TABLE_NAME, k.CONSTRAINT_NAME, k.COLUMN_NAME,
		  k.REFERENCED_COLUMN_NAME, r.UPDATE_RULE, r.DELETE_RULE
		FROM information_schema.KEY_COLUMN_USAGE k
		JOIN information_schema.REFERENTIAL_CONSTRAINTS r
		  ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME
		  AND r.TABLE_NAME = k.TABLE_NAME
		WHERE k.REFERENCED_TABLE_SCHEMA = ? AND k.REFERENCED_TABLE_NAME = ?
		ORDER BY k.TABLE_SCHEMA, k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`, db, name)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.Referrer
	for rows.Next() {
		var fromDB, fromTable, key, col, refCol, onUpdate, onDelete string
		if err := rows.Scan(&fromDB, &fromTable, &key, &col, &refCol, &onUpdate, &onDelete); err != nil {
			return nil, err
		}
		from := model.NewRef(model.KindTable, fromDB, fromTable)
		if n := len(out); n == 0 || !out[n-1].From.Equal(from) || out[n-1].Key.Name != key {
			out = append(out, model.Referrer{From: from, Key: model.ForeignKey{Name: key, RefSchema: db, RefTable: name,
				OnUpdate: model.ReferentialAction(onUpdate), OnDelete: model.ReferentialAction(onDelete)}})
		}
		k := &out[len(out)-1].Key
		k.Columns = append(k.Columns, col)
		k.RefColumns = append(k.RefColumns, refCol)
	}
	return out, rows.Err()
}
