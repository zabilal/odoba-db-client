package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Describe loads an object's full structure: what the structure tab, the DDL
// generator and schema comparison need, which is more than the tree shows.
func (s *pgSource) Describe(ctx context.Context, ref model.ObjectRef) (any, error) {
	if len(ref.Path) < 3 {
		return nil, fmt.Errorf("postgres: incomplete reference %s", ref)
	}
	p, err := s.poolFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	schema, name := ref.Path[1], ref.Path[2]

	switch ref.Kind {
	case model.KindTable:
		return s.describeTable(ctx, p, schema, name)
	case model.KindView, model.KindMaterializedView:
		return s.describeView(ctx, p, schema, name, ref.Kind == model.KindMaterializedView)
	}
	return nil, fmt.Errorf("postgres: describing %s is not supported yet", ref.Kind)
}

func (s *pgSource) describeTable(ctx context.Context, p *pgxpool.Pool, schema, name string) (*model.Table, error) {
	t := &model.Table{Name: name, RowsEstimate: -1}
	err := p.QueryRow(ctx, `
		SELECT COALESCE(obj_description(c.oid, 'pg_class'), ''), c.reltuples::bigint
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2`, schema, name).Scan(&t.Comment, &t.RowsEstimate)
	if err != nil {
		return nil, fmt.Errorf("postgres: table %s.%s: %w", schema, name, err)
	}
	if t.Columns, err = describeColumns(ctx, p, schema, name); err != nil {
		return nil, err
	}
	if err := describeConstraints(ctx, p, schema, name, t); err != nil {
		return nil, err
	}
	if t.Indexes, err = describeIndexes(ctx, p, schema, name); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *pgSource) describeView(ctx context.Context, p *pgxpool.Pool, schema, name string, mat bool) (*model.View, error) {
	v := &model.View{Name: name, Materialized: mat}
	err := p.QueryRow(ctx, `
		SELECT pg_get_viewdef(c.oid, true), COALESCE(obj_description(c.oid, 'pg_class'), '')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2`, schema, name).Scan(&v.Definition, &v.Comment)
	if err != nil {
		return nil, fmt.Errorf("postgres: view %s.%s: %w", schema, name, err)
	}
	if v.Columns, err = describeColumns(ctx, p, schema, name); err != nil {
		return nil, err
	}
	if mat {
		if v.Indexes, err = describeIndexes(ctx, p, schema, name); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func describeColumns(ctx context.Context, p *pgxpool.Pool, schema, rel string) ([]model.Column, error) {
	rows, err := p.Query(ctx, `
		SELECT a.attname, a.atttypid, a.atttypmod, format_type(a.atttypid, a.atttypmod),
		       a.attnotnull, COALESCE(pg_get_expr(d.adbin, d.adrelid), ''), d.adbin IS NOT NULL,
		       a.attidentity <> '', a.attgenerated <> '',
		       COALESCE(col_description(c.oid, a.attnum), ''), a.attnum
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
		WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, schema, rel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Column
	for rows.Next() {
		var c model.Column
		var oid uint32
		var typmod int32
		var native, def string
		var notNull, hasDef, identity, generated bool
		var pos int16
		if err := rows.Scan(&c.Name, &oid, &typmod, &native, &notNull, &def, &hasDef,
			&identity, &generated, &c.Comment, &pos); err != nil {
			return nil, err
		}
		c.Type = dataType(oid, typmod)
		c.Type.Native = native // format_type knows user types that the OID table does not
		c.Type.Nullable = !notNull
		c.Position = int(pos)
		c.Identity = identity
		if generated {
			// For a generated column the stored expression is the generation
			// expression, not a default; conflating them would make a DDL
			// round trip turn it into a plain default.
			c.Generated = def
		} else {
			c.Default, c.HasDefault = def, hasDef
			c.AutoIncrement = strings.HasPrefix(def, "nextval(")
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var fkActions = map[string]model.ReferentialAction{
	"a": model.ActionNoAction, "r": model.ActionRestrict, "c": model.ActionCascade,
	"n": model.ActionSetNull, "d": model.ActionSetDefault,
}

func describeConstraints(ctx context.Context, p *pgxpool.Pool, schema, rel string, t *model.Table) error {
	rows, err := p.Query(ctx, `
		SELECT con.conname, con.contype::text,
		       ARRAY(SELECT a.attname::text FROM unnest(con.conkey) WITH ORDINALITY k(n, o)
		             JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.n ORDER BY k.o),
		       COALESCE(rn.nspname::text, ''), COALESCE(rc.relname::text, ''),
		       ARRAY(SELECT a.attname::text FROM unnest(con.confkey) WITH ORDINALITY k(n, o)
		             JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.n ORDER BY k.o),
		       con.confdeltype::text, con.confupdtype::text, pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_class rc ON rc.oid = con.confrelid
		LEFT JOIN pg_namespace rn ON rn.oid = rc.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND con.contype IN ('p', 'u', 'f', 'c')
		ORDER BY con.conname`, schema, rel)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var name, kind, refSchema, refTable, onDel, onUpd, def string
		var cols, refCols []string
		if err := rows.Scan(&name, &kind, &cols, &refSchema, &refTable, &refCols, &onDel, &onUpd, &def); err != nil {
			return err
		}
		switch kind {
		case "p":
			t.PrimaryKey = &model.PrimaryKey{Name: name, Columns: cols}
		case "u":
			t.Uniques = append(t.Uniques, model.UniqueConstraint{Name: name, Columns: cols})
		case "f":
			t.ForeignKeys = append(t.ForeignKeys, model.ForeignKey{
				Name: name, Columns: cols, RefSchema: refSchema, RefTable: refTable,
				RefColumns: refCols, OnDelete: fkActions[onDel], OnUpdate: fkActions[onUpd],
			})
		case "c":
			t.Checks = append(t.Checks, model.CheckConstraint{
				Name: name, Expression: strings.TrimPrefix(def, "CHECK "),
			})
		}
	}
	return rows.Err()
}

func describeIndexes(ctx context.Context, p *pgxpool.Pool, schema, rel string) ([]model.Index, error) {
	// The primary key's own index is reported through PrimaryKey, so listing
	// it again here would make schema comparison see it twice.
	rows, err := p.Query(ctx, `
		SELECT ic.relname, i.indisunique, am.amname,
		       COALESCE(pg_get_expr(i.indpred, i.indrelid), ''),
		       ARRAY(SELECT pg_get_indexdef(i.indexrelid, k, true)
		             FROM generate_series(1, i.indnkeyatts) k ORDER BY k),
		       ARRAY(SELECT (i.indoption[k - 1] & 1) = 1
		             FROM generate_series(1, i.indnkeyatts) k ORDER BY k),
		       ARRAY(SELECT pg_get_indexdef(i.indexrelid, k, true)
		             FROM generate_series(i.indnkeyatts + 1, i.indnatts) k ORDER BY k)
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_class ic ON ic.oid = i.indexrelid
		JOIN pg_am am ON am.oid = ic.relam
		WHERE n.nspname = $1 AND c.relname = $2 AND NOT i.indisprimary
		ORDER BY ic.relname`, schema, rel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Index
	for rows.Next() {
		var ix model.Index
		var defs, include []string
		var desc []bool
		if err := rows.Scan(&ix.Name, &ix.Unique, &ix.Method, &ix.Predicate, &defs, &desc, &include); err != nil {
			return nil, err
		}
		for k, d := range defs {
			col := model.IndexColumn{Descending: k < len(desc) && desc[k]}
			// pg_get_indexdef gives a bare name for a column and the
			// expression text for an expression index.
			if strings.ContainsAny(d, "( ") {
				col.Expression = d
			} else {
				col.Name = strings.Trim(d, `"`)
			}
			ix.Columns = append(ix.Columns, col)
		}
		ix.Include = include
		out = append(out, ix)
	}
	return out, rows.Err()
}
