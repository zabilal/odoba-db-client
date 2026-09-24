//go:build duckdb

package duckdb

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The explorer tree (FR-2.1, FR-2.2):
//
//	database        [db]            the file, and any other attached to it
//	  schema        [db, schema]
//	    class       [db, schema, kind]
//	      object    [db, schema, name]
//	        column  [db, schema, table, column]
//
// The catalogue is DuckDB's own table functions — duckdb_databases(),
// duckdb_schemas(), duckdb_tables() and the rest — rather than the
// pg_catalog it also keeps. pg_catalog here is a compatibility layer over
// the same descriptors; the native one answers more and answers it
// plainly, and every name reaches it as a bound parameter (ADR-0146).

// Root lists the databases this connection holds.
//
// The file it was opened on, and any other somebody has attached. system
// and temp are the engine's own and are not somebody's data: temp holds
// what a session made for itself, and system holds the catalogue that is
// being read to draw this tree.
func (s *duckSource) Root(ctx context.Context) ([]model.Node, error) {
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT database_name FROM duckdb_databases()
		WHERE database_name NOT IN ('system', 'temp')
		ORDER BY database_name`)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, statementError(err, ctx)
		}
		node := model.Node{Ref: model.NewRef(model.KindDatabase, name), Label: name, HasChildren: true}
		if name == s.primary {
			node.Attrs = map[string]string{"current": "true"}
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

// Children lists a node's direct children.
func (s *duckSource) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindDatabase:
		return s.schemas(ctx, ref)
	case model.KindSchema:
		return s.folders(ctx, ref)
	case model.KindFolder:
		return s.folderContents(ctx, ref)
	case model.KindTable, model.KindView:
		return s.columnNodes(ctx, ref)
	}
	return nil, nil
}

// schemas lists a database's schemas.
//
// All of them, without reading duckdb_schemas().internal. That column
// does not mean what its name suggests: main — the schema every database
// starts with and where most people's tables are — is flagged internal,
// so a tree that believed it would hide the only schema most files have.
// What is left out is decided a level up, by which databases are listed.
func (s *duckSource) schemas(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 1 {
		return nil, nil
	}
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	name := ref.Path[0]
	rows, err := db.QueryContext(ctx, `
		SELECT schema_name FROM duckdb_schemas() WHERE database_name = ?
		ORDER BY schema_name = 'main' DESC, schema_name`, name)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var schema string
		if err := rows.Scan(&schema); err != nil {
			return nil, statementError(err, ctx)
		}
		out = append(out, model.Node{
			Ref: model.NewRef(model.KindSchema, name, schema), Label: schema, HasChildren: true,
		})
	}
	return out, rows.Err()
}

// folders returns only the classes that hold something, with exact counts
// as badges — one round trip for all four.
func (s *duckSource) folders(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 2 {
		return nil, nil
	}
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	name, schema := ref.Path[0], ref.Path[1]
	var tables, views, indexes, sequences int64
	err = db.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM duckdb_tables() WHERE database_name = ? AND schema_name = ?),
		  (SELECT count(*) FROM duckdb_views() WHERE database_name = ? AND schema_name = ? AND NOT internal),
		  (SELECT count(*) FROM duckdb_indexes() WHERE database_name = ? AND schema_name = ?),
		  (SELECT count(*) FROM duckdb_sequences() WHERE database_name = ? AND schema_name = ?)`,
		name, schema, name, schema, name, schema, name, schema).
		Scan(&tables, &views, &indexes, &sequences)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	out := model.ClassNodes(ref, map[model.ObjectKind]int64{
		model.KindTable: tables, model.KindView: views,
		model.KindIndex: indexes, model.KindSequence: sequences,
	})
	if len(out) == 0 {
		// A schema with nothing in it still shows its tables, empty: a
		// schema node says it has children, and one that then lists none
		// draws an expander that turns and never opens (FR-2.2).
		out = []model.Node{model.ClassNode(ref, model.KindTable, 0)}
	}
	return out, nil
}

func (s *duckSource) folderContents(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	if len(ref.Path) < 3 {
		return nil, nil
	}
	db, err := s.conn()
	if err != nil {
		return nil, err
	}
	name, schema := ref.Path[0], ref.Path[1]
	kind, _ := model.ClassOf(ref) // a folder that is no class lists nothing

	var q string
	switch kind {
	case model.KindTable:
		// The estimate comes in the same row as the name, so a badge costs
		// nothing extra.
		q = `SELECT table_name, estimated_size FROM duckdb_tables()
		     WHERE database_name = ? AND schema_name = ? ORDER BY table_name`
	case model.KindView:
		q = `SELECT view_name, -1 FROM duckdb_views()
		     WHERE database_name = ? AND schema_name = ? AND NOT internal ORDER BY view_name`
	case model.KindIndex:
		q = `SELECT index_name, -1 FROM duckdb_indexes()
		     WHERE database_name = ? AND schema_name = ? ORDER BY index_name`
	case model.KindSequence:
		q = `SELECT sequence_name, -1 FROM duckdb_sequences()
		     WHERE database_name = ? AND schema_name = ? ORDER BY sequence_name`
	default:
		return nil, nil
	}

	rows, err := db.QueryContext(ctx, q, name, schema)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()

	relational := kind == model.KindTable || kind == model.KindView
	var out []model.Node
	for rows.Next() {
		var label string
		var estimate sql.NullInt64
		if err := rows.Scan(&label, &estimate); err != nil {
			return nil, statementError(err, ctx)
		}
		node := model.Node{
			Ref: model.NewRef(kind, name, schema, label), Label: label,
			Browsable: relational, HasChildren: relational,
		}
		if b, ok := estimateBadge(estimate); ok {
			node.Badge = &b
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

// estimateBadge is a table's row estimate as a badge, or nothing.
//
// Nothing when the engine has not measured it, which it says by answering
// nothing at all rather than by answering zero — so unlike the engines
// that cannot tell the two apart, an empty table here is badged 0 and
// means it (FR-2.5).
func estimateBadge(estimate sql.NullInt64) (model.Badge, bool) {
	if !estimate.Valid || estimate.Int64 < 0 {
		return model.Badge{}, false
	}
	return model.Badge{Text: humanCount(estimate.Int64), Exact: false}, true
}

func (s *duckSource) columnNodes(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	info, err := s.tableInfo(ctx, ref)
	if err != nil {
		return nil, err
	}
	name, schema, rel := ref.Path[0], ref.Path[1], ref.Path[2]
	key := map[string]bool{}
	if !info.rowID {
		for _, c := range info.key {
			key[c] = true
		}
	}
	out := make([]model.Node, 0, len(info.columns))
	for _, c := range info.columns {
		attrs := map[string]string{"type": c.Type.Native,
			"nullable": strconv.FormatBool(c.Type.Nullable)}
		if key[c.Name] {
			attrs["key"] = "primary"
		}
		out = append(out, model.Node{
			Ref: model.NewRef(model.KindColumn, name, schema, rel, c.Name), Label: c.Name, Attrs: attrs,
		})
	}
	return out, nil
}

// Badge loads a single node's annotation (FR-2.5). A listing already
// carries its estimate; this serves a refresh of one node.
func (s *duckSource) Badge(ctx context.Context, ref model.ObjectRef) (model.Badge, bool, error) {
	if ref.Kind != model.KindTable || len(ref.Path) < 3 {
		return model.Badge{}, false, nil
	}
	db, err := s.conn()
	if err != nil {
		return model.Badge{}, false, err
	}
	var estimate sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT estimated_size FROM duckdb_tables()
		WHERE database_name = ? AND schema_name = ? AND table_name = ?`,
		ref.Path[0], ref.Path[1], ref.Path[2]).Scan(&estimate)
	if err == sql.ErrNoRows {
		return model.Badge{}, false, nil
	}
	if err != nil {
		return model.Badge{}, false, statementError(err, ctx)
	}
	b, ok := estimateBadge(estimate)
	return b, ok, nil
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
