package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// resultOrigins says where a query's columns were read from (FR-4.8,
// ADR-0035). The row description gives each column's table and its number
// there; the table's name and the column's are looked up. When every column
// comes from one table and its primary key is among them, the result is
// known by that key, and its rows can be edited.
//
// The lookup runs on another pooled connection, since the result is still
// open on this one, and nothing is cached: a column renamed since would
// otherwise be written as the wrong one. A lookup that fails leaves the
// result as it was, read-only.
func (s *pgSource) resultOrigins(ctx context.Context, db string, r *rowStream, fds []pgconn.FieldDescription) {
	if len(fds) == 0 || len(fds) != len(r.cols) {
		return
	}
	p, err := s.pool(ctx, db)
	if err != nil {
		return
	}
	lctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	type table struct {
		ref  model.ObjectRef
		cols map[int16]string
	}
	tables := map[uint32]*table{}
	one := true // every column from the first column's table
	for _, fd := range fds {
		if fd.TableOID != fds[0].TableOID {
			one = false
		}
		if _, seen := tables[fd.TableOID]; seen || fd.TableOID == 0 {
			continue
		}
		var schema, name, kind string
		var nums []int16
		var names []string
		if err := p.QueryRow(lctx, `
			SELECT n.nspname::text, c.relname::text, c.relkind::text,
			       array_agg(a.attnum ORDER BY a.attnum), array_agg(a.attname::text ORDER BY a.attnum)
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
			WHERE c.oid = $1
			GROUP BY 1, 2, 3`, fd.TableOID).Scan(&schema, &name, &kind, &nums, &names); err != nil {
			return
		}
		var t *table
		if kind == "r" || kind == "p" { // a table, or a partitioned one
			t = &table{ref: model.NewRef(model.KindTable, db, schema, name), cols: map[int16]string{}}
			for i, n := range nums {
				t.cols[n] = names[i]
			}
		}
		tables[fd.TableOID] = t
	}
	for i, fd := range fds {
		if t := tables[fd.TableOID]; t != nil {
			if n, ok := t.cols[int16(fd.TableAttributeNumber)]; ok {
				r.cols[i].Origin, r.cols[i].OriginColumn = t.ref, n
			}
		}
	}
	t := tables[fds[0].TableOID]
	if !one || t == nil {
		return
	}
	pk, err := s.primaryKey(lctx, p, t.ref)
	if err != nil || len(pk) == 0 {
		return
	}
	have := map[string]bool{}
	for _, c := range r.cols {
		have[c.OriginColumn] = true
	}
	for _, k := range pk {
		if !have[k] {
			return
		}
	}
	r.id = model.RowIdentity{Kind: model.IdentityPrimaryKey, Columns: pk, Target: t.ref}
}
