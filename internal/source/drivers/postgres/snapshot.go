package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Reading a whole database in one pass, for schema comparison (FR-7.1).
//
// Describe answers one object and asks the catalogue five times to do it.
// Comparing two databases that way is one query per object per side, which
// on a real schema is thousands of round trips — and that is why
// source.Snapshotter exists.
//
// So every query here reads the whole database at once and the rows are
// stitched together by relation afterwards. The queries are the same
// catalogue queries Describe uses with the per-object filter taken out,
// which is deliberate: two readings of the same catalogue that could drift
// apart would make a comparison report differences that are this program's
// and not the databases'.

// relKey addresses a relation within a snapshot.
type relKey struct{ schema, name string }

// Snapshot loads a complete database structure for comparison.
func (s *pgSource) Snapshot(ctx context.Context, database string) (*model.Database, error) {
	if database == "" {
		database = s.primary
	}
	p, err := s.pool(ctx, database)
	if err != nil {
		return nil, err
	}
	db := &model.Database{Name: database}
	if err := p.QueryRow(ctx, `
		SELECT COALESCE(pg_encoding_to_char(encoding), ''), COALESCE(datcollate, ''),
		       COALESCE(shobj_description(oid, 'pg_database'), '')
		FROM pg_database WHERE datname = $1`, database).
		Scan(&db.Charset, &db.Collate, &db.Comment); err != nil {
		return nil, fmt.Errorf("postgres: database %s: %w", database, err)
	}

	schemas, order, err := snapshotSchemas(ctx, p)
	if err != nil {
		return nil, err
	}
	tables, views, err := snapshotRelations(ctx, p, schemas)
	if err != nil {
		return nil, err
	}
	if err := snapshotColumns(ctx, p, tables, views); err != nil {
		return nil, err
	}
	if err := snapshotConstraints(ctx, p, tables); err != nil {
		return nil, err
	}
	if err := snapshotIndexes(ctx, p, tables, views); err != nil {
		return nil, err
	}
	if err := snapshotTriggers(ctx, p, tables); err != nil {
		return nil, err
	}
	for _, fill := range []func(context.Context, *pgxpool.Pool, map[string]*model.Schema) error{
		snapshotRoutines, snapshotSequences, snapshotUserTypes,
	} {
		if err := fill(ctx, p, schemas); err != nil {
			return nil, err
		}
	}

	db.Schemas = make([]model.Schema, 0, len(order))
	for _, name := range order {
		db.Schemas = append(db.Schemas, *schemas[name])
	}
	return db, nil
}

// snapshotSchemas lists the schemas somebody put there, leaving out
// PostgreSQL's own — a comparison that reported pg_catalog would report it
// every time and be right about nothing.
func snapshotSchemas(ctx context.Context, p *pgxpool.Pool) (map[string]*model.Schema, []string, error) {
	rows, err := p.Query(ctx, `
		SELECT n.nspname, COALESCE(pg_get_userbyid(n.nspowner), ''),
		       COALESCE(obj_description(n.oid, 'pg_namespace'), '')
		FROM pg_namespace n
		WHERE n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
		ORDER BY n.nspname`)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: reading the schemas: %w", err)
	}
	defer rows.Close()

	out := map[string]*model.Schema{}
	var order []string
	for rows.Next() {
		var s model.Schema
		if err := rows.Scan(&s.Name, &s.Owner, &s.Comment); err != nil {
			return nil, nil, err
		}
		out[s.Name] = &s
		order = append(order, s.Name)
	}
	return out, order, rows.Err()
}

// snapshotRelations reads the tables, views and materialized views, and
// answers each one by where it is so the rest can be hung on it.
func snapshotRelations(ctx context.Context, p *pgxpool.Pool, schemas map[string]*model.Schema) (
	map[relKey]*model.Table, map[relKey]*model.View, error) {
	rows, err := p.Query(ctx, `
		SELECT n.nspname, c.relname, c.relkind::text, c.reltuples::bigint,
		       COALESCE(obj_description(c.oid, 'pg_class'), ''),
		       CASE WHEN c.relkind IN ('v', 'm') THEN pg_get_viewdef(c.oid, true) ELSE '' END
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r', 'p', 'v', 'm')
		  AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
		ORDER BY n.nspname, c.relname`)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: reading the relations: %w", err)
	}
	defer rows.Close()

	tables := map[relKey]*model.Table{}
	views := map[relKey]*model.View{}
	for rows.Next() {
		var schema, name, kind, comment, def string
		var tuples int64
		if err := rows.Scan(&schema, &name, &kind, &tuples, &comment, &def); err != nil {
			return nil, nil, err
		}
		s, ok := schemas[schema]
		if !ok {
			continue
		}
		switch kind {
		case "r", "p":
			s.Tables = append(s.Tables, model.Table{Name: name, Comment: comment, RowsEstimate: tuples})
			tables[relKey{schema, name}] = &s.Tables[len(s.Tables)-1]
		default:
			s.Views = append(s.Views, model.View{Name: name, Comment: comment,
				Materialized: kind == "m", Definition: def})
			views[relKey{schema, name}] = &s.Views[len(s.Views)-1]
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	// The slices are done growing, so the pointers taken above are stable
	// only from here. Taken again now, they are pointers into the final
	// arrays rather than into arrays append has since replaced.
	return relink(schemas, tables, views)
}

// relink takes the pointers again once every append is finished.
//
// Appending to a slice can move it, so a pointer into it taken mid-loop can
// address an array nobody reads any more. Rather than index arithmetic
// scattered through five functions, the pointers are taken once at the end
// and everything after this writes through them.
func relink(schemas map[string]*model.Schema, tables map[relKey]*model.Table, views map[relKey]*model.View) (
	map[relKey]*model.Table, map[relKey]*model.View, error) {
	for name, s := range schemas {
		for i := range s.Tables {
			tables[relKey{name, s.Tables[i].Name}] = &s.Tables[i]
		}
		for i := range s.Views {
			views[relKey{name, s.Views[i].Name}] = &s.Views[i]
		}
	}
	return tables, views, nil
}

func snapshotColumns(ctx context.Context, p *pgxpool.Pool, tables map[relKey]*model.Table, views map[relKey]*model.View) error {
	rows, err := p.Query(ctx, `
		SELECT n.nspname, c.relname,
		       a.attname, a.atttypid, a.atttypmod, format_type(a.atttypid, a.atttypmod),
		       a.attnotnull, COALESCE(pg_get_expr(d.adbin, d.adrelid), ''), d.adbin IS NOT NULL,
		       a.attidentity <> '', a.attgenerated <> '',
		       COALESCE(col_description(c.oid, a.attnum), ''), a.attnum
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_attrdef d ON d.adrelid = c.oid AND d.adnum = a.attnum
		WHERE c.relkind IN ('r', 'p', 'v', 'm') AND a.attnum > 0 AND NOT a.attisdropped
		  AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
		ORDER BY n.nspname, c.relname, a.attnum`)
	if err != nil {
		return fmt.Errorf("postgres: reading the columns: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, rel string
		var c model.Column
		var oid uint32
		var typmod int32
		var native, def string
		var notNull, hasDef, identity, generated bool
		var pos int16
		if err := rows.Scan(&schema, &rel, &c.Name, &oid, &typmod, &native, &notNull, &def, &hasDef,
			&identity, &generated, &c.Comment, &pos); err != nil {
			return err
		}
		c.Type = dataType(oid, typmod)
		c.Type.Native = native
		c.Type.Nullable = !notNull
		c.Position = int(pos)
		c.Identity = identity
		if generated {
			c.Generated = def
		} else {
			c.Default, c.HasDefault = def, hasDef
			c.AutoIncrement = strings.HasPrefix(def, "nextval(")
		}
		if t, ok := tables[relKey{schema, rel}]; ok {
			t.Columns = append(t.Columns, c)
		} else if v, ok := views[relKey{schema, rel}]; ok {
			v.Columns = append(v.Columns, c)
		}
	}
	return rows.Err()
}

func snapshotConstraints(ctx context.Context, p *pgxpool.Pool, tables map[relKey]*model.Table) error {
	rows, err := p.Query(ctx, `
		SELECT n.nspname, c.relname, con.conname, con.contype::text,
		       ARRAY(SELECT a.attname::text FROM unnest(con.conkey) WITH ORDINALITY k(num, o)
		             JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.num ORDER BY k.o),
		       COALESCE(rn.nspname::text, ''), COALESCE(rc.relname::text, ''),
		       ARRAY(SELECT a.attname::text FROM unnest(con.confkey) WITH ORDINALITY k(num, o)
		             JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.num ORDER BY k.o),
		       con.confdeltype::text, con.confupdtype::text, pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_class rc ON rc.oid = con.confrelid
		LEFT JOIN pg_namespace rn ON rn.oid = rc.relnamespace
		WHERE con.contype IN ('p', 'u', 'f', 'c')
		  AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
		ORDER BY n.nspname, c.relname, con.conname`)
	if err != nil {
		return fmt.Errorf("postgres: reading the constraints: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, rel, name, kind, refSchema, refTable, onDel, onUpd, def string
		var cols, refCols []string
		if err := rows.Scan(&schema, &rel, &name, &kind, &cols, &refSchema, &refTable, &refCols,
			&onDel, &onUpd, &def); err != nil {
			return err
		}
		t, ok := tables[relKey{schema, rel}]
		if !ok {
			continue
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

func snapshotIndexes(ctx context.Context, p *pgxpool.Pool, tables map[relKey]*model.Table, views map[relKey]*model.View) error {
	// The primary key's own index is reported through PrimaryKey, so listing
	// it here too would make a comparison see it twice.
	rows, err := p.Query(ctx, `
		SELECT n.nspname, c.relname, ic.relname, i.indisunique, am.amname,
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
		WHERE NOT i.indisprimary
		  AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
		ORDER BY n.nspname, c.relname, ic.relname`)
	if err != nil {
		return fmt.Errorf("postgres: reading the indexes: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, rel string
		var ix model.Index
		var defs, include []string
		var desc []bool
		if err := rows.Scan(&schema, &rel, &ix.Name, &ix.Unique, &ix.Method, &ix.Predicate,
			&defs, &desc, &include); err != nil {
			return err
		}
		ix.Columns = indexColumnsOf(defs, desc)
		ix.Include = trimNames(include)
		if t, ok := tables[relKey{schema, rel}]; ok {
			t.Indexes = append(t.Indexes, ix)
		} else if v, ok := views[relKey{schema, rel}]; ok {
			v.Indexes = append(v.Indexes, ix)
		}
	}
	return rows.Err()
}

func snapshotTriggers(ctx context.Context, p *pgxpool.Pool, tables map[relKey]*model.Table) error {
	rows, err := p.Query(ctx, `
		SELECT n.nspname, c.relname, tr.tgname, pg_get_triggerdef(tr.oid), tr.tgtype::int,
		       COALESCE(pg_get_expr(tr.tgqual, tr.tgrelid), '')
		FROM pg_trigger tr
		JOIN pg_class c ON c.oid = tr.tgrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE NOT tr.tgisinternal
		  AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
		ORDER BY n.nspname, c.relname, tr.tgname`)
	if err != nil {
		return fmt.Errorf("postgres: reading the triggers: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, rel, name, def, cond string
		var kind int32
		if err := rows.Scan(&schema, &rel, &name, &def, &kind, &cond); err != nil {
			return err
		}
		if t, ok := tables[relKey{schema, rel}]; ok {
			t.Triggers = append(t.Triggers, triggerOf(name, def, kind, cond))
		}
	}
	return rows.Err()
}

func snapshotRoutines(ctx context.Context, p *pgxpool.Pool, schemas map[string]*model.Schema) error {
	rows, err := p.Query(ctx, `
		SELECT n.nspname,
		       p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')',
		       pg_get_functiondef(p.oid), l.lanname,
		       CASE p.prokind WHEN 'p' THEN 'procedure' ELSE 'function' END,
		       COALESCE(obj_description(p.oid, 'pg_proc'), '')
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		JOIN pg_language l ON l.oid = p.prolang
		WHERE p.prokind IN ('f', 'p') AND `+ownRoutines+`
		  AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
		ORDER BY n.nspname, 2`)
	if err != nil {
		return fmt.Errorf("postgres: reading the routines: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema, kind string
		var r model.Routine
		if err := rows.Scan(&schema, &r.Name, &r.Definition, &r.Language, &kind, &r.Comment); err != nil {
			return err
		}
		r.Kind = model.RoutineKind(kind)
		if s, ok := schemas[schema]; ok {
			s.Routines = append(s.Routines, r)
		}
	}
	return rows.Err()
}

func snapshotSequences(ctx context.Context, p *pgxpool.Pool, schemas map[string]*model.Schema) error {
	rows, err := p.Query(ctx, `
		SELECT s.schemaname, s.sequencename, s.data_type::text, s.start_value, s.increment_by,
		       s.min_value, s.max_value, s.cycle,
		       COALESCE(obj_description(c.oid, 'pg_class'), '')
		FROM pg_sequences s
		JOIN pg_namespace n ON n.nspname = s.schemaname
		JOIN pg_class c ON c.relname = s.sequencename AND c.relnamespace = n.oid
		WHERE s.schemaname !~ '^pg_' AND s.schemaname <> 'information_schema'
		ORDER BY s.schemaname, s.sequencename`)
	if err != nil {
		return fmt.Errorf("postgres: reading the sequences: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema string
		var q model.Sequence
		var min, max *int64
		if err := rows.Scan(&schema, &q.Name, &q.DataType, &q.Start, &q.Increment,
			&min, &max, &q.Cycle, &q.Comment); err != nil {
			return err
		}
		q.MinValue, q.MaxValue = min, max
		if s, ok := schemas[schema]; ok {
			s.Sequences = append(s.Sequences, q)
		}
	}
	return rows.Err()
}

// snapshotUserTypes reads the enums, domains, ranges and composites.
//
// Describe has never answered a user type — nothing asked it to until now —
// so this is the first reading of one, and it reads only what the model can
// hold rather than inventing fields for it.
func snapshotUserTypes(ctx context.Context, p *pgxpool.Pool, schemas map[string]*model.Schema) error {
	rows, err := p.Query(ctx, `
		SELECT n.nspname, t.typname,
		       CASE t.typtype WHEN 'e' THEN 'enum' WHEN 'd' THEN 'domain'
		                      WHEN 'r' THEN 'range' ELSE 'composite' END,
		       ARRAY(SELECT e.enumlabel::text FROM pg_enum e
		             WHERE e.enumtypid = t.oid ORDER BY e.enumsortorder),
		       CASE WHEN t.typtype = 'd' THEN format_type(t.typbasetype, t.typtypmod) ELSE '' END,
		       COALESCE(obj_description(t.oid, 'pg_type'), '')
		FROM pg_type t
		JOIN pg_namespace n ON n.oid = t.typnamespace
		WHERE `+userTypes+`
		  AND n.nspname !~ '^pg_' AND n.nspname <> 'information_schema'
		ORDER BY n.nspname, t.typname`)
	if err != nil {
		return fmt.Errorf("postgres: reading the types: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var schema string
		var u model.UserType
		if err := rows.Scan(&schema, &u.Name, &u.Category, &u.EnumValues, &u.BaseType, &u.Comment); err != nil {
			return err
		}
		if s, ok := schemas[schema]; ok {
			s.UserTypes = append(s.UserTypes, u)
		}
	}
	return rows.Err()
}
