package trino

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The explorer tree (FR-2.1, FR-2.2):
//
//	catalog         [catalog]
//	  schema        [catalog, schema]
//	    class       [catalog, schema, kind]      model.ClassRef: "table", "view"
//	      object    [catalog, schema, name]
//	        column  [catalog, schema, name, column]
//
// A catalog is shown as a database, which is the nearest word the tree has:
// it is the outermost thing a name begins with. It is somebody else's store,
// and each has an information_schema of its own, so everything below the
// root is read from the catalog it is about — "tpch".information_schema — and
// the catalog's name is the one name that cannot be bound. It is quoted.

// Root is the catalogs this server has.
func (s *trinoSource) Root(ctx context.Context) ([]model.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT catalog_name FROM system.metadata.catalogs ORDER BY catalog_name`)
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
		switch {
		case name == "system":
			// Trino's own: its nodes, its queries, its catalogs. Shown,
			// because that is where somebody looks when something is slow,
			// and marked so it sorts out of the way.
			node.Attrs = map[string]string{"system": "true"}
		case name == s.catalog:
			node.Attrs = map[string]string{"current": "true"}
		}
		out = append(out, node)
	}
	return out, statementError(rows.Err(), ctx)
}

func (s *trinoSource) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindDatabase:
		if len(ref.Path) != 1 {
			return nil, fmt.Errorf("trino: incomplete reference %s", ref)
		}
		return s.schemas(ctx, ref.Path[0])
	case model.KindSchema:
		if len(ref.Path) != 2 {
			return nil, fmt.Errorf("trino: incomplete reference %s", ref)
		}
		return s.folders(ctx, ref)
	case model.KindFolder:
		kind, ok := model.ClassOf(ref)
		if !ok || len(ref.Path) < 2 {
			return nil, fmt.Errorf("trino: no such class %s", ref)
		}
		return s.objects(ctx, ref.Path[0], ref.Path[1], kind)
	case model.KindTable, model.KindView:
		if !browsable(ref) {
			return nil, fmt.Errorf("trino: incomplete reference %s", ref)
		}
		cols, err := s.columns(ctx, ref)
		if err != nil {
			return nil, err
		}
		out := make([]model.Node, len(cols))
		for i, c := range cols {
			out[i] = model.Node{Ref: model.NewRef(model.KindColumn, ref.Path[0], ref.Path[1], ref.Path[2], c.Name),
				Label: c.Name,
				Attrs: map[string]string{"type": c.Type.Native, "nullable": strconv.FormatBool(c.Type.Nullable)}}
		}
		return out, nil
	}
	return nil, nil
}

// information is a catalog's own information_schema table, the catalog's name
// quoted: it is a name and not a value, and the only one in these queries
// that a parameter cannot stand for.
func (s *trinoSource) information(catalog, table string) string {
	return s.QuoteIdentifier(catalog) + ".information_schema." + table
}

func (s *trinoSource) schemas(ctx context.Context, catalog string) ([]model.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT schema_name FROM `+s.information(catalog, "schemata")+` ORDER BY schema_name`)
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
		node := model.Node{Ref: model.NewRef(model.KindSchema, catalog, name), Label: name, HasChildren: true}
		if name == "information_schema" {
			node.Attrs = map[string]string{"system": "true"}
		}
		out = append(out, node)
	}
	return out, statementError(rows.Err(), ctx)
}

// tableTypes is what information_schema calls each class.
var tableTypes = map[model.ObjectKind]string{
	model.KindTable: "BASE TABLE", model.KindView: "VIEW",
}

// folders is a schema's object classes that hold something (FR-2.2), with
// exact counts, in one round trip.
func (s *trinoSource) folders(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT table_type, count(*) FROM `+s.information(ref.Path[0], "tables")+
		` WHERE table_schema = ? GROUP BY table_type`, ref.Path[1])
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	counts := map[model.ObjectKind]int64{}
	for rows.Next() {
		var kind string
		var n int64
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, statementError(err, ctx)
		}
		for k, name := range tableTypes {
			if name == kind {
				counts[k] += n
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, statementError(err, ctx)
	}
	out := model.ClassNodes(ref, counts)
	if len(out) == 0 {
		// A schema holding nothing still shows its tables, empty: a schema
		// node says it has children, and one that then lists none draws an
		// expander that turns and never opens (FR-2.2).
		out = []model.Node{model.ClassNode(ref, model.KindTable, 0)}
	}
	return out, nil
}

func (s *trinoSource) objects(ctx context.Context, catalog, schema string, kind model.ObjectKind) ([]model.Node, error) {
	typ, ok := tableTypes[kind]
	if !ok {
		return nil, fmt.Errorf("trino: no %s class", kind)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT table_name FROM `+s.information(catalog, "tables")+
		` WHERE table_schema = ? AND table_type = ? ORDER BY table_name`, schema, typ)
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
		out = append(out, model.Node{Ref: model.NewRef(kind, catalog, schema, name), Label: name,
			HasChildren: true, Browsable: true, Describable: true})
	}
	return out, statementError(rows.Err(), ctx)
}

// columns reads a table's columns, with the type as Trino declares it.
// Remembered, because paging orders by them and asks on every page.
func (s *trinoSource) columns(ctx context.Context, ref model.ObjectRef) ([]model.Column, error) {
	at := strings.Join(ref.Path, "\x00")
	s.mu.Lock()
	cols, ok := s.cols[at]
	s.mu.Unlock()
	if ok {
		return cols, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT column_name, data_type, is_nullable, column_default, ordinal_position FROM `+
		s.information(ref.Path[0], "columns")+` WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position`,
		ref.Path[1], ref.Path[2])
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ, nullable string
		var def sql.NullString
		var position int64
		if err := rows.Scan(&name, &typ, &nullable, &def, &position); err != nil {
			return nil, statementError(err, ctx)
		}
		c := model.Column{Name: name, Type: dataType(typ), Position: int(position)}
		c.Type.Nullable = nullable != "NO"
		if def.Valid {
			c.Default, c.HasDefault = def.String, true
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, statementError(err, ctx)
	}
	if len(cols) == 0 {
		// Not remembered: a table with no columns is a table that is not
		// there, and it may be by the next time somebody asks.
		return nil, fmt.Errorf("trino: %s does not exist, or has no columns this user may see", s.QualifyRef(ref))
	}
	s.mu.Lock()
	s.cols[at] = cols
	s.mu.Unlock()
	return cols, nil
}

// Describe loads a table's structure (*model.Table) or a view's
// (*model.View).
func (s *trinoSource) Describe(ctx context.Context, ref model.ObjectRef) (any, error) {
	if !browsable(ref) {
		return nil, fmt.Errorf("trino: cannot describe %s", ref)
	}
	cols, err := s.columns(ctx, ref)
	if err != nil {
		return nil, err
	}
	var comment sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT comment FROM system.metadata.table_comments
		WHERE catalog_name = ? AND schema_name = ? AND table_name = ?`, ref.Path[0], ref.Path[1], ref.Path[2]).Scan(&comment)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, statementError(err, ctx)
	}
	if ref.Kind == model.KindView {
		var definition sql.NullString
		err := s.db.QueryRowContext(ctx, `SELECT view_definition FROM `+s.information(ref.Path[0], "views")+
			` WHERE table_schema = ? AND table_name = ?`, ref.Path[1], ref.Path[2]).Scan(&definition)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, statementError(err, ctx)
		}
		return &model.View{Name: ref.Path[2], Columns: cols, Definition: definition.String, Comment: comment.String}, nil
	}
	t := &model.Table{Name: ref.Path[2], Columns: cols, Comment: comment.String, RowsEstimate: -1}
	if n, ok, err := s.estimate(ctx, ref); err != nil {
		return nil, err
	} else if ok {
		t.RowsEstimate = n
	}
	return t, nil
}

// estimate is how many rows the catalog's connector says a table holds, where
// it keeps a figure at all.
//
// SHOW STATS and not a count: a count is a read of the whole table, in an
// engine that exists for tables too large to read. The figure is the
// connector's own statistic — exact for some, days old for others, absent for
// many — and is offered as an estimate for that reason (FR-2.5).
func (s *trinoSource) estimate(ctx context.Context, ref model.ObjectRef) (int64, bool, error) {
	// The statement stands alone — it cannot be selected from — and answers a
	// line for each column and one more, with no column named, for the table.
	rows, err := s.db.QueryContext(ctx, `SHOW STATS FOR `+s.QualifyRef(ref))
	if err != nil {
		return 0, false, statementError(err, ctx)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return 0, false, statementError(err, ctx)
	}
	column, count := -1, -1
	for i, n := range names {
		switch n {
		case "column_name":
			column = i
		case "row_count":
			count = i
		}
	}
	if column < 0 || count < 0 {
		return 0, false, nil // a Trino that answers this some other way
	}
	vals := make([]any, len(names))
	ptrs := make([]any, len(names))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return 0, false, statementError(err, ctx)
		}
		if vals[column] != nil {
			continue
		}
		if n, ok := vals[count].(float64); ok && n >= 0 {
			return int64(n + 0.5), true, nil
		}
		return 0, false, nil
	}
	return 0, false, statementError(rows.Err(), ctx)
}

// Badge is the row estimate beside a table (FR-2.5). A view has none: its
// rows are whatever its query answers when it is asked.
func (s *trinoSource) Badge(ctx context.Context, ref model.ObjectRef) (model.Badge, bool, error) {
	if ref.Kind != model.KindTable || !browsable(ref) {
		return model.Badge{}, false, nil
	}
	n, ok, err := s.estimate(ctx, ref)
	if err != nil || !ok {
		return model.Badge{}, false, err
	}
	return model.Badge{Text: humanCount(n), Exact: false}, true, nil
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
	return strings.TrimSuffix(strconv.FormatFloat(f, 'f', 1, 64), ".0")
}
