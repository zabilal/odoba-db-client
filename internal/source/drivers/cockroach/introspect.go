package cockroach

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

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
// Each level is one round trip, fetched only when somebody expands a node.
// Every value reaches the server as a bound parameter; the one thing
// written into statement text is a database name, quoted, because a
// catalogue here is named rather than connected to.
//
// Two catalogues answer, and each is asked what it is best at. What was
// declared comes from pg_catalog, which CockroachDB keeps faithfully.
// What the engine has since measured comes from its own SHOW, which
// carries a table's row estimate in the same row as its name. What is not
// asked at all is crdb_internal: the server now refuses it unless a
// session declares itself willing to read unsupported internals, and this
// driver does not ask that of anybody's cluster.

// catalog names a table in a database's own pg_catalog.
//
// PostgreSQL reads a three-part name as a cross-database reference and
// refuses it; CockroachDB answers it. That is what lets one connection
// read the whole cluster (ADR-0145).
func (d dialect) catalog(db, rel string) string {
	return d.QuoteIdentifier(db) + ".pg_catalog." + rel
}

// relkinds maps a class to the pg_class.relkind values it lists.
var relkinds = map[model.ObjectKind][]string{
	model.KindIndex: {"i"},
}

// showTypes maps a class to the word SHOW TABLES uses for it.
var showTypes = map[model.ObjectKind]string{
	model.KindTable:            "table",
	model.KindView:             "view",
	model.KindMaterializedView: "materialized view",
	model.KindSequence:         "sequence",
}

// Root lists the databases in the cluster this user may read.
//
// Databases without CONNECT privilege are hidden rather than listed:
// showing them only for every expansion to fail is noise.
func (s *crdbSource) Root(ctx context.Context) ([]model.Node, error) {
	p, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := p.Query(ctx, `
		SELECT datname FROM pg_catalog.pg_database
		WHERE datname <> 'system' AND has_database_privilege(datname, 'CONNECT')
		ORDER BY datname`)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, statementError(err, ctx)
	}
	out := make([]model.Node, 0, len(names))
	for _, n := range names {
		node := model.Node{Ref: model.NewRef(model.KindDatabase, n), Label: n, HasChildren: true}
		if n == s.primary {
			node.Attrs = map[string]string{"current": "true"}
		}
		out = append(out, node)
	}
	return out, nil
}

// Children lists a node's direct children.
func (s *crdbSource) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindDatabase:
		return s.schemas(ctx, ref)
	case model.KindSchema:
		return s.folders(ctx, ref)
	case model.KindFolder:
		return s.folderContents(ctx, ref)
	case model.KindTable, model.KindView, model.KindMaterializedView:
		return s.columnNodes(ctx, ref)
	}
	return nil, nil
}

// schemas lists a database's schemas, without the four the engine keeps
// for itself. Named rather than matched: CockroachDB has exactly these
// four and no temporary schemas, so a pattern guarding against a pg_temp_1
// that cannot exist would be a line nothing could prove.
func (s *crdbSource) schemas(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 1 {
		return nil, nil
	}
	p, err := s.conn()
	if err != nil {
		return nil, err
	}
	db := ref.Path[0]
	// public sorts first because it is where most people's tables are.
	rows, err := p.Query(ctx, `
		SELECT nspname FROM `+s.catalog(db, "pg_namespace")+`
		WHERE nspname NOT IN ('pg_catalog', 'information_schema', 'crdb_internal', 'pg_extension')
		ORDER BY nspname = 'public' DESC, nspname`)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, statementError(err, ctx)
	}
	out := make([]model.Node, 0, len(names))
	for _, n := range names {
		out = append(out, model.Node{
			Ref: model.NewRef(model.KindSchema, db, n), Label: n, HasChildren: true,
		})
	}
	return out, nil
}

// folders returns only the object classes that hold something, with exact
// counts as badges — one round trip for all six, so the counts are free
// rather than a fetch per class.
func (s *crdbSource) folders(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 2 {
		return nil, nil
	}
	p, err := s.conn()
	if err != nil {
		return nil, err
	}
	db, schema := ref.Path[0], ref.Path[1]
	var tables, views, matviews, indexes, sequences, routines, types int64
	err = p.QueryRow(ctx, `
		WITH ns AS (SELECT oid FROM `+s.catalog(db, "pg_namespace")+` WHERE nspname = $1)
		SELECT
		  count(*) FILTER (WHERE c.relkind = 'r'),
		  count(*) FILTER (WHERE c.relkind = 'v'),
		  count(*) FILTER (WHERE c.relkind = 'm'),
		  count(*) FILTER (WHERE c.relkind = 'i'),
		  count(*) FILTER (WHERE c.relkind = 'S'),
		  (SELECT count(*) FROM `+s.catalog(db, "pg_proc")+` p
		     WHERE p.pronamespace = (SELECT oid FROM ns)),
		  (SELECT count(*) FROM `+s.catalog(db, "pg_type")+` t
		     WHERE t.typnamespace = (SELECT oid FROM ns) AND t.typtype = 'e')
		FROM `+s.catalog(db, "pg_class")+` c WHERE c.relnamespace = (SELECT oid FROM ns)`,
		schema).Scan(&tables, &views, &matviews, &indexes, &sequences, &routines, &types)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	out := model.ClassNodes(ref, map[model.ObjectKind]int64{
		model.KindTable: tables, model.KindView: views, model.KindMaterializedView: matviews,
		model.KindIndex: indexes, model.KindSequence: sequences,
		model.KindRoutine: routines, model.KindUserType: types,
	})
	if len(out) == 0 {
		// A schema with nothing in it still shows its tables, empty: a
		// schema node says it has children, and one that then lists none
		// draws an expander that turns and never opens (FR-2.2).
		out = []model.Node{model.ClassNode(ref, model.KindTable, 0)}
	}
	return out, nil
}

func (s *crdbSource) folderContents(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 3 {
		return nil, nil
	}
	db, schema := ref.Path[0], ref.Path[1]
	kind, _ := model.ClassOf(ref) // a folder that is no class lists nothing
	p, err := s.conn()
	if err != nil {
		return nil, err
	}

	switch kind {
	case model.KindUserType:
		rows, err := p.Query(ctx, `
			SELECT t.typname FROM `+s.catalog(db, "pg_type")+` t
			JOIN `+s.catalog(db, "pg_namespace")+` n ON n.oid = t.typnamespace
			WHERE n.nspname = $1 AND t.typtype = 'e' ORDER BY 1`, schema)
		if err != nil {
			return nil, statementError(err, ctx)
		}
		names, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, statementError(err, ctx)
		}
		out := make([]model.Node, 0, len(names))
		for _, n := range names {
			out = append(out, model.Node{Ref: model.NewRef(model.KindUserType, db, schema, n),
				Label: n, Attrs: map[string]string{"type": "enum"}})
		}
		return out, nil

	case model.KindRoutine:
		// Routines are overloaded by argument list, so the signature is
		// part of the name: two functions called f are otherwise
		// indistinguishable. The engine's own listing says what the
		// arguments are, which is what a name has to carry.
		rows, err := p.Query(ctx, `SELECT function_name || '(' || argument_data_types || ')',
			CASE function_type WHEN 'proc' THEN 'procedure' ELSE 'function' END
			FROM [SHOW FUNCTIONS FROM `+s.QuoteIdentifier(db)+"."+s.QuoteIdentifier(schema)+`]
			ORDER BY 1`)
		if err != nil {
			return nil, statementError(err, ctx)
		}
		return collectNodes(rows, func(sig, kind string) model.Node {
			return model.Node{Ref: model.NewRef(model.KindRoutine, db, schema, sig), Label: sig,
				Attrs: map[string]string{"kind": kind}}
		})
	}

	if kinds, ok := relkinds[kind]; ok {
		rows, err := p.Query(ctx, `
			SELECT c.relname, COALESCE(tc.relname, '')
			FROM `+s.catalog(db, "pg_class")+` c
			JOIN `+s.catalog(db, "pg_namespace")+` n ON n.oid = c.relnamespace
			LEFT JOIN `+s.catalog(db, "pg_index")+` i ON i.indexrelid = c.oid
			LEFT JOIN `+s.catalog(db, "pg_class")+` tc ON tc.oid = i.indrelid
			WHERE n.nspname = $1 AND c.relkind::text = ANY($2::text[])
			ORDER BY c.relname`, schema, kinds)
		if err != nil {
			return nil, statementError(err, ctx)
		}
		return collectNodes(rows, func(name, table string) model.Node {
			node := model.Node{Ref: model.NewRef(kind, db, schema, name), Label: name}
			if table != "" {
				node.Label = model.OnTable(name, table)
				node.Attrs = map[string]string{"table": table}
			}
			return node
		})
	}

	want, ok := showTypes[kind]
	if !ok {
		return nil, nil
	}
	return s.listed(ctx, ref, kind, want)
}

// listed reads a schema's tables, views and sequences from the engine's
// own listing, which carries each one's row estimate beside its name.
func (s *crdbSource) listed(ctx context.Context, ref model.ObjectRef, kind model.ObjectKind, want string) ([]model.Node, error) {
	p, err := s.conn()
	if err != nil {
		return nil, err
	}
	db, schema := ref.Path[0], ref.Path[1]
	rows, err := p.Query(ctx, `SELECT table_name, estimated_row_count FROM [SHOW TABLES FROM `+
		s.QuoteIdentifier(db)+"."+s.QuoteIdentifier(schema)+`] WHERE type = $1 ORDER BY table_name`, want)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()

	relational := kind != model.KindSequence
	var out []model.Node
	for rows.Next() {
		var name string
		var estimate int64
		if err := rows.Scan(&name, &estimate); err != nil {
			return nil, statementError(err, ctx)
		}
		node := model.Node{
			Ref: model.NewRef(kind, db, schema, name), Label: name,
			Browsable: relational, HasChildren: relational,
		}
		if b, ok := estimateBadge(estimate); ok {
			node.Badge = &b
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

// estimateBadge is the row estimate as a badge, or nothing.
//
// Nothing when the estimate is zero. CockroachDB says zero both for a
// table it has never measured and for one that is empty, and in a listing
// there is no telling the two apart. A table with no rows losing its badge
// costs an absence; a table with a million rows badged "0" would be an
// untruth in the tree, which FR-2.5 will not have.
func estimateBadge(estimate int64) (model.Badge, bool) {
	if estimate <= 0 {
		return model.Badge{}, false
	}
	return model.Badge{Text: humanCount(estimate), Exact: false}, true
}

// collectNodes reads a two-column result of name and detail into nodes.
func collectNodes(rows pgx.Rows, node func(name, detail string) model.Node) ([]model.Node, error) {
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var name, detail string
		if err := rows.Scan(&name, &detail); err != nil {
			return nil, err
		}
		out = append(out, node(name, detail))
	}
	return out, rows.Err()
}

func (s *crdbSource) columnNodes(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	cols, err := s.columns(ctx, ref)
	if err != nil {
		return nil, err
	}
	db, schema, rel := ref.Path[0], ref.Path[1], ref.Path[2]
	out := make([]model.Node, 0, len(cols))
	for _, c := range cols {
		attrs := map[string]string{"type": c.Type.Native,
			"nullable": strconv.FormatBool(c.Type.Nullable)}
		if c.Attrs["key"] != "" {
			attrs["key"] = c.Attrs["key"]
		}
		out = append(out, model.Node{
			Ref: model.NewRef(model.KindColumn, db, schema, rel, c.Name), Label: c.Name, Attrs: attrs,
		})
	}
	return out, nil
}

// columns reads what was declared, in the order it was declared.
//
// Hidden columns are left out. A table with no key of its own is given one
// by the engine — rowid, hidden — and it is not a column anybody wrote, so
// it is no part of the table's structure. It is still what addresses a row,
// and browse.go selects it for that (ADR-0145).
func (s *crdbSource) columns(ctx context.Context, ref model.ObjectRef) ([]model.Column, error) {
	if len(ref.Path) < 3 {
		return nil, fmt.Errorf("cockroach: incomplete reference %s", ref)
	}
	p, err := s.conn()
	if err != nil {
		return nil, err
	}
	db, schema, rel := ref.Path[0], ref.Path[1], ref.Path[2]
	rows, err := p.Query(ctx, `
		SELECT a.attname, a.atttypid, a.atttypmod, a.attnotnull, a.attnum,
		       COALESCE(pg_get_expr(ad.adbin, ad.adrelid), ''), ad.adbin IS NOT NULL,
		       COALESCE(d.description, ''),
		       EXISTS (SELECT 1 FROM `+s.catalog(db, "pg_index")+` i
		               WHERE i.indrelid = c.oid AND i.indisprimary AND a.attnum = ANY(i.indkey))
		FROM `+s.catalog(db, "pg_attribute")+` a
		JOIN `+s.catalog(db, "pg_class")+` c ON c.oid = a.attrelid
		JOIN `+s.catalog(db, "pg_namespace")+` n ON n.oid = c.relnamespace
		LEFT JOIN `+s.catalog(db, "pg_attrdef")+` ad ON ad.adrelid = c.oid AND ad.adnum = a.attnum
		LEFT JOIN `+s.catalog(db, "pg_description")+` d
		       ON d.objoid = c.oid AND d.objsubid = a.attnum
		WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0
		  AND NOT a.attisdropped AND a.attishidden IS NOT TRUE
		ORDER BY a.attnum`, schema, rel)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()

	var out []model.Column
	for rows.Next() {
		var name, def, comment string
		var oid uint32
		var typmod, attnum int32
		var notNull, hasDefault, pk bool
		if err := rows.Scan(&name, &oid, &typmod, &notNull, &attnum,
			&def, &hasDefault, &comment, &pk); err != nil {
			return nil, statementError(err, ctx)
		}
		dt := dataType(oid, typmod)
		dt.Nullable = !notNull
		if dt.Class == model.TypeUnknown {
			if info, ok := s.lookupType(ctx, db, oid); ok {
				dt.Class, dt.Native = info.class, info.native
			}
		}
		col := model.Column{Name: name, Type: dt, Position: int(attnum),
			Default: def, HasDefault: hasDefault, Comment: comment}
		if pk {
			col.Attrs = map[string]string{"key": "primary"}
		}
		// unique_rowid() is what the engine writes into the key it made,
		// and it is the nearest thing here to an auto-increment.
		if strings.Contains(def, "unique_rowid()") || strings.Contains(def, "nextval(") {
			col.AutoIncrement = true
		}
		out = append(out, col)
	}
	return out, rows.Err()
}

// lookupType names a type the OID table does not know — an enum, or one of
// the spatial types — so a column reads "order_status" rather than
// "oid 100123".
func (s *crdbSource) lookupType(ctx context.Context, db string, oid uint32) (typeInfo, bool) {
	p, err := s.conn()
	if err != nil {
		return typeInfo{}, false
	}
	var name, typtype string
	if err := p.QueryRow(ctx, `SELECT typname, typtype::text FROM `+
		s.catalog(db, "pg_type")+` WHERE oid = $1`, oid).Scan(&name, &typtype); err != nil {
		return typeInfo{}, false
	}
	return typeInfo{class: classOf(name, typtype), native: name}, true
}

// Badge loads a single node's annotation (FR-2.5).
//
// From the table's statistics, not from the listing. The two disagree, and
// which to ask depends on what is being asked. A listing wants one round
// trip for a whole schema and can live with a figure that catches up: the
// estimate SHOW TABLES carries is a cached one, and a table measured a
// moment ago still reads zero there for minutes afterwards. This is asked
// for one node at a time, after the tree has painted, so it can afford the
// query that gives the engine's best answer — which the statistics hold,
// and which is right the moment they are gathered.
func (s *crdbSource) Badge(ctx context.Context, ref model.ObjectRef) (model.Badge, bool, error) {
	switch ref.Kind {
	case model.KindTable, model.KindMaterializedView:
		if len(ref.Path) < 3 {
			return model.Badge{}, false, nil
		}
		p, err := s.conn()
		if err != nil {
			return model.Badge{}, false, err
		}
		var estimate int64
		err = p.QueryRow(ctx, `SELECT row_count FROM [SHOW STATISTICS FOR TABLE `+
			s.QualifyRef(ref)+`] ORDER BY created DESC LIMIT 1`).Scan(&estimate)
		if errors.Is(err, pgx.ErrNoRows) {
			// Nothing has ever measured this table.
			return model.Badge{}, false, nil
		}
		if err != nil {
			return model.Badge{}, false, statementError(err, ctx)
		}
		b, ok := estimateBadge(estimate)
		return b, ok, nil
	}
	return model.Badge{}, false, nil
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
	if len(s) > 2 && s[len(s)-2:] == ".0" {
		return s[:len(s)-2]
	}
	return s
}
