package postgres

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The explorer tree (FR-2.1, FR-2.2):
//
//	database        [db]
//	  schema        [db, schema]
//	    folder      [db, schema, "tables" | "views" | "matviews" | "functions" | "sequences"]
//	      object    [db, schema, name]          name(args) for routines
//	        column  [db, schema, table, column]
//
// Each level is one round trip, fetched only when the user expands a node.
// Every name reaches the server as a bound parameter, never as statement text.

const (
	folderTables    = "tables"
	folderViews     = "views"
	folderMatViews  = "matviews"
	folderFunctions = "functions"
	folderSequences = "sequences"
)

var folderLabels = map[string]string{
	folderTables:    "Tables",
	folderViews:     "Views",
	folderMatViews:  "Materialized Views",
	folderFunctions: "Functions",
	folderSequences: "Sequences",
}

// relkinds maps a folder to the pg_class.relkind values it lists. 'p' is a
// partitioned table's parent, which the user thinks of as the table.
var relkinds = map[string][]string{
	folderTables:    {"r", "p"},
	folderViews:     {"v"},
	folderMatViews:  {"m"},
	folderSequences: {"S"},
}

var folderKinds = map[string]model.ObjectKind{
	folderTables:    model.KindTable,
	folderViews:     model.KindView,
	folderMatViews:  model.KindMaterializedView,
	folderSequences: model.KindSequence,
}

// Root lists the databases this user may connect to.
//
// Databases without CONNECT privilege are hidden rather than listed: showing
// them only for every expansion to fail with a permission error is noise.
func (s *pgSource) Root(ctx context.Context) ([]model.Node, error) {
	p, err := s.pool(ctx, s.primary)
	if err != nil {
		return nil, err
	}
	rows, err := p.Query(ctx, `
		SELECT datname FROM pg_database
		WHERE NOT datistemplate AND datallowconn
		  AND has_database_privilege(datname, 'CONNECT')
		ORDER BY datname`)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
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
func (s *pgSource) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindDatabase:
		return s.schemas(ctx, ref)
	case model.KindSchema:
		return s.folders(ctx, ref)
	case model.KindFolder:
		return s.folderContents(ctx, ref)
	case model.KindTable, model.KindView, model.KindMaterializedView:
		return s.columns(ctx, ref)
	}
	return nil, nil
}

func (s *pgSource) schemas(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	p, err := s.poolFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	// System schemas are hidden; public sorts first because it is where most
	// users' tables are.
	rows, err := p.Query(ctx, `
		SELECT nspname FROM pg_namespace
		WHERE nspname !~ '^pg_' AND nspname <> 'information_schema'
		  AND has_schema_privilege(nspname, 'USAGE')
		ORDER BY nspname = 'public' DESC, nspname`)
	if err != nil {
		return nil, err
	}
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	db := ref.Path[0]
	out := make([]model.Node, 0, len(names))
	for _, n := range names {
		out = append(out, model.Node{
			Ref: model.NewRef(model.KindSchema, db, n), Label: n, HasChildren: true,
		})
	}
	return out, nil
}

// folders returns only the object classes that actually contain something,
// with exact counts as badges — one round trip for all five, so the counts are
// free rather than a lazy fetch per folder.
func (s *pgSource) folders(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 2 {
		return nil, nil
	}
	p, err := s.poolFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	var tables, views, matviews, sequences, functions int64
	err = p.QueryRow(ctx, `
		WITH ns AS (SELECT oid FROM pg_namespace WHERE nspname = $1)
		SELECT
		  count(*) FILTER (WHERE c.relkind IN ('r', 'p')),
		  count(*) FILTER (WHERE c.relkind = 'v'),
		  count(*) FILTER (WHERE c.relkind = 'm'),
		  count(*) FILTER (WHERE c.relkind = 'S'),
		  (SELECT count(*) FROM pg_proc WHERE pronamespace = (SELECT oid FROM ns))
		FROM pg_class c WHERE c.relnamespace = (SELECT oid FROM ns)`,
		ref.Path[1]).Scan(&tables, &views, &matviews, &sequences, &functions)
	if err != nil {
		return nil, err
	}

	counts := map[string]int64{
		folderTables: tables, folderViews: views, folderMatViews: matviews,
		folderFunctions: functions, folderSequences: sequences,
	}
	var out []model.Node
	for _, f := range []string{folderTables, folderViews, folderMatViews, folderFunctions, folderSequences} {
		if counts[f] == 0 {
			continue
		}
		out = append(out, model.Node{
			Ref:         model.NewRef(model.KindFolder, ref.Path[0], ref.Path[1], f),
			Label:       folderLabels[f],
			HasChildren: true,
			Badge:       &model.Badge{Text: strconv.FormatInt(counts[f], 10), Exact: true},
		})
	}
	return out, nil
}

func (s *pgSource) folderContents(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 3 {
		return nil, nil
	}
	db, schema, folder := ref.Path[0], ref.Path[1], ref.Path[2]
	p, err := s.poolFor(ctx, ref)
	if err != nil {
		return nil, err
	}

	if folder == folderFunctions {
		// Routines are overloaded by argument list, so the signature is part
		// of the name: two functions called f are otherwise indistinguishable.
		rows, err := p.Query(ctx, `
			SELECT p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')',
			       CASE p.prokind WHEN 'p' THEN 'procedure' WHEN 'a' THEN 'aggregate'
			                      WHEN 'w' THEN 'window' ELSE 'function' END
			FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = $1 ORDER BY 1`, schema)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []model.Node
		for rows.Next() {
			var sig, kind string
			if err := rows.Scan(&sig, &kind); err != nil {
				return nil, err
			}
			out = append(out, model.Node{
				Ref: model.NewRef(model.KindRoutine, db, schema, sig), Label: sig,
				Attrs: map[string]string{"kind": kind},
			})
		}
		return out, rows.Err()
	}

	kinds, ok := relkinds[folder]
	if !ok {
		return nil, nil
	}
	rows, err := p.Query(ctx, `
		SELECT c.relname, c.reltuples::bigint
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relkind::text = ANY($2::text[])
		ORDER BY c.relname`, schema, kinds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	kind := folderKinds[folder]
	relational := kind != model.KindSequence
	var out []model.Node
	for rows.Next() {
		var name string
		var estimate int64
		if err := rows.Scan(&name, &estimate); err != nil {
			return nil, err
		}
		node := model.Node{
			Ref:         model.NewRef(kind, db, schema, name),
			Label:       name,
			Browsable:   relational,
			HasChildren: relational,
		}
		// reltuples comes back in the same row, so the badge costs nothing
		// extra. It is -1 for a table that has never been analysed; no badge
		// beats a wrong one.
		if relational && estimate >= 0 {
			node.Badge = &model.Badge{Text: humanCount(estimate), Exact: false}
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func (s *pgSource) columns(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 3 {
		return nil, nil
	}
	db, schema, rel := ref.Path[0], ref.Path[1], ref.Path[2]
	p, err := s.poolFor(ctx, ref)
	if err != nil {
		return nil, err
	}
	rows, err := p.Query(ctx, `
		SELECT a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
		       EXISTS (SELECT 1 FROM pg_index i
		               WHERE i.indrelid = c.oid AND i.indisprimary AND a.attnum = ANY(i.indkey))
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, schema, rel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Node
	for rows.Next() {
		var name, typ string
		var notNull, pk bool
		if err := rows.Scan(&name, &typ, &notNull, &pk); err != nil {
			return nil, err
		}
		attrs := map[string]string{"type": typ, "nullable": strconv.FormatBool(!notNull)}
		if pk {
			attrs["key"] = "primary"
		}
		out = append(out, model.Node{
			Ref: model.NewRef(model.KindColumn, db, schema, rel, name), Label: name, Attrs: attrs,
		})
	}
	return out, rows.Err()
}

// Badge loads a single node's annotation (FR-2.5). Table listings already
// carry their estimate; this serves a refresh of one node.
func (s *pgSource) Badge(ctx context.Context, ref model.ObjectRef) (model.Badge, bool, error) {
	switch ref.Kind {
	case model.KindDatabase:
		p, err := s.pool(ctx, s.primary)
		if err != nil {
			return model.Badge{}, false, err
		}
		var size string
		if err := p.QueryRow(ctx, "SELECT pg_size_pretty(pg_database_size($1))", ref.Path[0]).Scan(&size); err != nil {
			return model.Badge{}, false, err
		}
		return model.Badge{Text: size, Exact: true}, true, nil

	case model.KindTable, model.KindMaterializedView:
		if len(ref.Path) < 3 {
			return model.Badge{}, false, nil
		}
		p, err := s.poolFor(ctx, ref)
		if err != nil {
			return model.Badge{}, false, err
		}
		var n int64
		err = p.QueryRow(ctx, `
			SELECT c.reltuples::bigint FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = $1 AND c.relname = $2`, ref.Path[1], ref.Path[2]).Scan(&n)
		if err != nil || n < 0 {
			return model.Badge{}, false, err
		}
		return model.Badge{Text: humanCount(n), Exact: false}, true, nil
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
