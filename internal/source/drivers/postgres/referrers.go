package postgres

import (
	"context"
	"fmt"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Referrers lists the foreign keys of other tables that refer to a table,
// each with the table it is in (FR-3.11, ADR-0041): pg_constraint read by
// the table referred to, rather than by the table the key is in. A key a
// partitioned table's partitions inherit is listed once, as its parent's.
func (s *pgSource) Referrers(ctx context.Context, ref model.ObjectRef) ([]model.Referrer, error) {
	if ref.Kind != model.KindTable || len(ref.Path) < 3 {
		return nil, fmt.Errorf("postgres: cannot list what refers to %s", ref)
	}
	p, err := s.poolFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	db, schema, name := ref.Path[0], ref.Path[1], ref.Path[2]
	rows, err := p.Query(ctx, `
		SELECT n.nspname::text, c.relname::text, con.conname::text,
		       ARRAY(SELECT a.attname::text FROM unnest(con.conkey) WITH ORDINALITY k(n, o)
		             JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.n ORDER BY k.o),
		       ARRAY(SELECT a.attname::text FROM unnest(con.confkey) WITH ORDINALITY k(n, o)
		             JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.n ORDER BY k.o),
		       con.confdeltype::text, con.confupdtype::text
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_class rc ON rc.oid = con.confrelid
		JOIN pg_namespace rn ON rn.oid = rc.relnamespace
		WHERE rn.nspname = $1 AND rc.relname = $2 AND con.contype = 'f' AND con.conparentid = 0
		ORDER BY n.nspname, c.relname, con.conname`, schema, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Referrer
	for rows.Next() {
		var fromSchema, fromTable, key, onDel, onUpd string
		var cols, refCols []string
		if err := rows.Scan(&fromSchema, &fromTable, &key, &cols, &refCols, &onDel, &onUpd); err != nil {
			return nil, err
		}
		out = append(out, model.Referrer{From: model.NewRef(model.KindTable, db, fromSchema, fromTable),
			Key: model.ForeignKey{Name: key, Columns: cols, RefSchema: schema, RefTable: name,
				RefColumns: refCols, OnDelete: fkActions[onDel], OnUpdate: fkActions[onUpd]}})
	}
	return out, rows.Err()
}
