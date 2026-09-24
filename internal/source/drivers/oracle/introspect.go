package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// The explorer tree (FR-2.1, FR-2.2):
//
//	schema          [schema]
//	  class         [schema, kind]            model.ClassRef: "table", "view", …
//	    object      [schema, name]
//	      column    [schema, table, column]
//
// A schema here is a user: Oracle has no separate idea of one, and a
// connection is to a service rather than to a database, so the top of the
// tree is schemas.
//
// The queries read ALL_*, which is what this login may see, rather than
// DBA_*, which needs a privilege most logins do not have.

// systemSchemas are the ones the database keeps for itself. They are shown,
// because somebody connecting as an administrator has business in them, but
// marked so they sort out of the way.
var systemSchemas = map[string]bool{
	"SYS": true, "SYSTEM": true, "XDB": true, "OUTLN": true, "DBSNMP": true,
	"APPQOSSYS": true, "AUDSYS": true, "CTXSYS": true, "GSMADMIN_INTERNAL": true,
	"LBACSYS": true, "MDSYS": true, "OJVMSYS": true, "ORDDATA": true, "ORDSYS": true,
	"DVSYS": true, "DBSFWUSER": true, "REMOTE_SCHEDULER_AGENT": true, "SYS$UMF": true,
	"GGSYS": true, "ANONYMOUS": true, "WMSYS": true, "OLAPSYS": true, "SI_INFORMTN_SCHEMA": true,
	"ORDPLUGINS": true, "DGPDB_INT": true, "PUBLIC": true,
}

// Root is the schemas this login can see.
func (s *oracleSource) Root(ctx context.Context) ([]model.Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT username FROM all_users ORDER BY username`)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		node := model.Node{Ref: model.NewRef(model.KindSchema, name), Label: name, HasChildren: true}
		switch {
		case systemSchemas[name]:
			node.Attrs = map[string]string{"system": "true"}
		case name == s.primary:
			node.Attrs = map[string]string{"current": "true"}
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func (s *oracleSource) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindSchema:
		return s.folders(ctx, ref)
	case model.KindFolder:
		kind, ok := model.ClassOf(ref)
		if !ok || len(ref.Path) < 1 {
			return nil, fmt.Errorf("oracle: no such class %s", ref)
		}
		return s.objects(ctx, ref.Path[0], kind)
	case model.KindTable, model.KindView, model.KindMaterializedView:
		if len(ref.Path) < 2 {
			return nil, fmt.Errorf("oracle: incomplete reference %s", ref)
		}
		cols, err := s.columns(ctx, ref)
		if err != nil {
			return nil, err
		}
		out := make([]model.Node, len(cols))
		for i, c := range cols {
			attrs := map[string]string{"type": c.Type.Native, "nullable": strconv.FormatBool(c.Type.Nullable)}
			if c.Attrs["key"] != "" {
				attrs["key"] = c.Attrs["key"]
			}
			out[i] = model.Node{Ref: model.NewRef(model.KindColumn, ref.Path[0], ref.Path[1], c.Name),
				Label: c.Name, Attrs: attrs}
		}
		return out, nil
	}
	return nil, nil
}

// folders is a schema's object classes that hold something (FR-2.2), with
// exact counts, in one round trip.
func (s *oracleSource) folders(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	owner := ref.Path[0]
	var tables, views, matviews, indexes, routines, triggers, sequences, types int64
	err := s.db.QueryRowContext(ctx, `SELECT
		  COUNT(CASE WHEN object_type = 'TABLE' THEN 1 END),
		  COUNT(CASE WHEN object_type = 'VIEW' THEN 1 END),
		  COUNT(CASE WHEN object_type = 'MATERIALIZED VIEW' THEN 1 END),
		  COUNT(CASE WHEN object_type = 'INDEX' THEN 1 END),
		  COUNT(CASE WHEN object_type IN ('PROCEDURE', 'FUNCTION', 'PACKAGE') THEN 1 END),
		  COUNT(CASE WHEN object_type = 'TRIGGER' THEN 1 END),
		  COUNT(CASE WHEN object_type = 'SEQUENCE' THEN 1 END),
		  COUNT(CASE WHEN object_type = 'TYPE' THEN 1 END)
		FROM all_objects WHERE owner = :1`, owner).
		Scan(&tables, &views, &matviews, &indexes, &routines, &triggers, &sequences, &types)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	out := model.ClassNodes(ref, map[model.ObjectKind]int64{
		model.KindTable: tables, model.KindView: views, model.KindMaterializedView: matviews,
		model.KindIndex: indexes, model.KindRoutine: routines, model.KindTrigger: triggers,
		model.KindSequence: sequences, model.KindUserType: types,
	})
	if len(out) == 0 {
		// A schema holding nothing still shows its tables, empty: a schema
		// node says it has children, and one that then lists none draws an
		// expander that turns and never opens (FR-2.2).
		out = []model.Node{model.ClassNode(ref, model.KindTable, 0)}
	}
	return out, nil
}

// objectTypes is what ALL_OBJECTS calls each class.
var objectTypes = map[model.ObjectKind][]string{
	model.KindTable:            {"TABLE"},
	model.KindView:             {"VIEW"},
	model.KindMaterializedView: {"MATERIALIZED VIEW"},
	model.KindIndex:            {"INDEX"},
	model.KindRoutine:          {"PROCEDURE", "FUNCTION", "PACKAGE"},
	model.KindTrigger:          {"TRIGGER"},
	model.KindSequence:         {"SEQUENCE"},
	model.KindUserType:         {"TYPE"},
}

func (s *oracleSource) objects(ctx context.Context, owner string, kind model.ObjectKind) ([]model.Node, error) {
	types, ok := objectTypes[kind]
	if !ok {
		return nil, fmt.Errorf("oracle: no %s class", kind)
	}
	// The types are this program's own words, not anybody's input, and the
	// list is as long as the class has kinds.
	marks := make([]string, len(types))
	args := []any{owner}
	for i, t := range types {
		marks[i] = ":" + strconv.Itoa(i+2)
		args = append(args, t)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT object_name, object_type FROM all_objects
		WHERE owner = :1 AND object_type IN (`+strings.Join(marks, ", ")+`)
		ORDER BY object_name`, args...)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, err
		}
		n := model.Node{Ref: model.NewRef(kind, owner, name), Label: name}
		switch kind {
		case model.KindTable, model.KindView, model.KindMaterializedView:
			n.HasChildren, n.Browsable = true, true
		case model.KindRoutine:
			n.Attrs = map[string]string{"kind": strings.ToLower(typ)}
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// columns reads a table's or view's columns, with the type as somebody
// would declare it and whether the column is part of the primary key.
func (s *oracleSource) columns(ctx context.Context, ref model.ObjectRef) ([]model.Column, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.column_name, c.data_type, c.char_length,
		  c.data_length, c.data_precision, c.data_scale, c.nullable, c.data_default, c.column_id,
		  NVL(m.comments, ' '),
		  CASE WHEN EXISTS (
		    SELECT 1 FROM all_constraints k
		    JOIN all_cons_columns kc ON kc.owner = k.owner AND kc.constraint_name = k.constraint_name
		    WHERE k.owner = c.owner AND k.table_name = c.table_name
		      AND k.constraint_type = 'P' AND kc.column_name = c.column_name)
		  THEN 1 ELSE 0 END
		FROM all_tab_columns c
		LEFT JOIN all_col_comments m ON m.owner = c.owner AND m.table_name = c.table_name
		  AND m.column_name = c.column_name
		WHERE c.owner = :1 AND c.table_name = :2
		ORDER BY c.column_id`, ref.Path[0], ref.Path[1])
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.Column
	for rows.Next() {
		var name, typ, nullable, comment string
		var charLen, dataLen sql.NullInt64
		var precision, scale sql.NullInt64
		var deflt sql.NullString
		var position sql.NullInt64
		var primary int
		if err := rows.Scan(&name, &typ, &charLen, &dataLen, &precision, &scale, &nullable,
			&deflt, &position, &comment, &primary); err != nil {
			return nil, err
		}
		dt := declaredType(typ, charLen.Int64, precision, scale)
		dt.Nullable = nullable == "Y"
		c := model.Column{Name: name, Type: dt, Position: int(position.Int64),
			Comment: strings.TrimSpace(comment),
			Default: strings.TrimSpace(deflt.String), HasDefault: deflt.Valid}
		if primary == 1 {
			c.Attrs = map[string]string{"key": "primary"}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// declaredType reads what the catalogue says a column holds, which is more
// than the wire says: a VARCHAR2 and an NVARCHAR2 are the same on the wire,
// and a BOOLEAN travels as a number.
func declaredType(typ string, charLen int64, precision, scale sql.NullInt64) model.DataType {
	name := strings.ToUpper(strings.TrimSpace(typ))
	dt := model.DataType{Native: name, Length: -1}
	switch {
	case name == "NUMBER":
		dt = numberType(precision.Int64, scale.Int64)
		if !precision.Valid {
			dt = model.DataType{Class: model.TypeDecimal, Length: -1}
		}
		dt.Native = name
		if precision.Valid && scale.Valid && scale.Int64 > 0 {
			dt.Native = fmt.Sprintf("NUMBER(%d,%d)", precision.Int64, scale.Int64)
		} else if precision.Valid {
			dt.Native = fmt.Sprintf("NUMBER(%d)", precision.Int64)
		}
	case name == "FLOAT", name == "BINARY_FLOAT", name == "BINARY_DOUBLE":
		dt.Class = model.TypeFloat
	case name == "VARCHAR2", name == "NVARCHAR2", name == "CHAR", name == "NCHAR":
		dt.Class = model.TypeString
		dt.Length = charLen
		dt.Native = fmt.Sprintf("%s(%d)", name, charLen)
	case name == "CLOB", name == "NCLOB", name == "LONG":
		dt.Class = model.TypeString
	case name == "BLOB", name == "BFILE", name == "LONG RAW":
		dt.Class = model.TypeBytes
	case name == "RAW":
		dt.Class = model.TypeBytes
		dt.Length = charLen
		dt.Native = fmt.Sprintf("RAW(%d)", charLen)
	case name == "DATE":
		// Oracle's DATE carries a time of day as well as a day.
		dt.Class = model.TypeTimestamp
	case strings.HasPrefix(name, "TIMESTAMP"):
		dt.Class = model.TypeTimestamp
		dt.TimeZone = strings.Contains(name, "TIME ZONE")
	case strings.HasPrefix(name, "INTERVAL"):
		dt.Class = model.TypeInterval
	case name == "BOOLEAN":
		dt.Class = model.TypeBool
	case name == "JSON":
		dt.Class = model.TypeJSON
	case name == "XMLTYPE":
		dt.Class = model.TypeXML
	case name == "ROWID", name == "UROWID":
		dt.Class = model.TypeString
	default:
		dt.Class = model.TypeUnknown
	}
	return dt
}

// Describe loads a table's structure (*model.Table) or a view's
// (*model.View).
func (s *oracleSource) Describe(ctx context.Context, ref model.ObjectRef) (any, error) {
	if !browsableKinds[ref.Kind] || len(ref.Path) < 2 {
		return nil, fmt.Errorf("oracle: cannot describe %s", ref)
	}
	cols, err := s.columns(ctx, ref)
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("oracle: no such object %s", ref)
	}
	var comment sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT comments FROM all_tab_comments
		WHERE owner = :1 AND table_name = :2`, ref.Path[0], ref.Path[1]).Scan(&comment); err != nil &&
		err != sql.ErrNoRows {
		return nil, statementError(err, ctx)
	}
	if ref.Kind != model.KindTable {
		var text sql.NullString
		_ = s.db.QueryRowContext(ctx, `SELECT text FROM all_views
			WHERE owner = :1 AND view_name = :2`, ref.Path[0], ref.Path[1]).Scan(&text)
		return &model.View{Name: ref.Path[1], Columns: cols, Definition: text.String,
			Comment: comment.String, Materialized: ref.Kind == model.KindMaterializedView}, nil
	}
	t := &model.Table{Name: ref.Path[1], Columns: cols, Comment: comment.String, RowsEstimate: -1}
	var est sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT num_rows FROM all_tables
		WHERE owner = :1 AND table_name = :2`, ref.Path[0], ref.Path[1]).Scan(&est); err == nil && est.Valid {
		t.RowsEstimate = est.Int64
	}
	if t.Indexes, t.PrimaryKey, t.Uniques, err = s.indexes(ctx, ref); err != nil {
		return nil, err
	}
	if t.ForeignKeys, err = s.foreignKeys(ctx, ref); err != nil {
		return nil, err
	}
	if t.Checks, err = s.checks(ctx, ref); err != nil {
		return nil, err
	}
	return t, nil
}

// indexes reads a table's indexes and the constraints they carry.
func (s *oracleSource) indexes(ctx context.Context, ref model.ObjectRef) ([]model.Index, *model.PrimaryKey, []model.UniqueConstraint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT i.index_name, i.uniqueness, LOWER(i.index_type),
		  c.column_name, c.descend, NVL(k.constraint_type, ' '), NVL(k.constraint_name, ' ')
		FROM all_indexes i
		JOIN all_ind_columns c ON c.index_owner = i.owner AND c.index_name = i.index_name
		LEFT JOIN all_constraints k ON k.owner = i.owner AND k.index_name = i.index_name
		  AND k.constraint_type IN ('P', 'U')
		WHERE i.table_owner = :1 AND i.table_name = :2
		ORDER BY i.index_name, c.column_position`, ref.Path[0], ref.Path[1])
	if err != nil {
		return nil, nil, nil, statementError(err, ctx)
	}
	defer rows.Close()
	var ixs []model.Index
	var pk *model.PrimaryKey
	var uniques []model.UniqueConstraint
	for rows.Next() {
		var name, uniqueness, method, col, descend, kind, constraint string
		if err := rows.Scan(&name, &uniqueness, &method, &col, &descend, &kind, &constraint); err != nil {
			return nil, nil, nil, err
		}
		switch strings.TrimSpace(kind) {
		case "P":
			if pk == nil {
				pk = &model.PrimaryKey{Name: strings.TrimSpace(constraint)}
			}
			pk.Columns = append(pk.Columns, col)
		case "U":
			if len(uniques) == 0 || uniques[len(uniques)-1].Name != strings.TrimSpace(constraint) {
				uniques = append(uniques, model.UniqueConstraint{Name: strings.TrimSpace(constraint)})
			}
			u := &uniques[len(uniques)-1]
			u.Columns = append(u.Columns, col)
		default:
			if len(ixs) == 0 || ixs[len(ixs)-1].Name != name {
				ixs = append(ixs, model.Index{Name: name, Unique: uniqueness == "UNIQUE", Method: method})
			}
			ix := &ixs[len(ixs)-1]
			ix.Columns = append(ix.Columns, model.IndexColumn{Name: col, Descending: descend == "DESC"})
		}
	}
	return ixs, pk, uniques, rows.Err()
}

func (s *oracleSource) foreignKeys(ctx context.Context, ref model.ObjectRef) ([]model.ForeignKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT k.constraint_name, c.column_name,
		  r.owner, r.table_name, rc.column_name, k.delete_rule
		FROM all_constraints k
		JOIN all_cons_columns c ON c.owner = k.owner AND c.constraint_name = k.constraint_name
		JOIN all_constraints r ON r.owner = k.r_owner AND r.constraint_name = k.r_constraint_name
		JOIN all_cons_columns rc ON rc.owner = r.owner AND rc.constraint_name = r.constraint_name
		  AND rc.position = c.position
		WHERE k.owner = :1 AND k.table_name = :2 AND k.constraint_type = 'R'
		ORDER BY k.constraint_name, c.position`, ref.Path[0], ref.Path[1])
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.ForeignKey
	for rows.Next() {
		var name, col, refOwner, refTable, refCol, onDelete string
		if err := rows.Scan(&name, &col, &refOwner, &refTable, &refCol, &onDelete); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Name != name {
			// Oracle has no ON UPDATE at all: a key's parent cannot change
			// under it, so there is nothing to report but NO ACTION.
			out = append(out, model.ForeignKey{Name: name, RefSchema: refOwner, RefTable: refTable,
				OnDelete: referentialAction(onDelete), OnUpdate: model.ActionNoAction})
		}
		fk := &out[len(out)-1]
		fk.Columns = append(fk.Columns, col)
		fk.RefColumns = append(fk.RefColumns, refCol)
	}
	return out, rows.Err()
}

// referentialAction reads the rule as the catalogue spells it.
func referentialAction(rule string) model.ReferentialAction {
	switch strings.ToUpper(strings.TrimSpace(rule)) {
	case "CASCADE":
		return model.ActionCascade
	case "SET NULL":
		return model.ActionSetNull
	}
	return model.ActionNoAction
}

func (s *oracleSource) checks(ctx context.Context, ref model.ObjectRef) ([]model.CheckConstraint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT constraint_name, search_condition_vc
		FROM all_constraints
		WHERE owner = :1 AND table_name = :2 AND constraint_type = 'C'
		  AND NVL(generated, ' ') <> 'GENERATED NAME'
		ORDER BY constraint_name`, ref.Path[0], ref.Path[1])
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	var out []model.CheckConstraint
	for rows.Next() {
		var name string
		var cond sql.NullString
		if err := rows.Scan(&name, &cond); err != nil {
			return nil, err
		}
		out = append(out, model.CheckConstraint{Name: name, Expression: strings.TrimSpace(cond.String)})
	}
	return out, rows.Err()
}

// Badge is the row count the optimiser last counted (FR-2.5). It is an
// estimate: it is as old as the last time anybody gathered statistics, and
// is marked as one.
func (s *oracleSource) Badge(ctx context.Context, ref model.ObjectRef) (model.Badge, bool, error) {
	if ref.Kind != model.KindTable || len(ref.Path) < 2 {
		return model.Badge{}, false, nil
	}
	var est sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT num_rows FROM all_tables
		WHERE owner = :1 AND table_name = :2`, ref.Path[0], ref.Path[1]).Scan(&est)
	if err != nil || !est.Valid {
		if err == sql.ErrNoRows {
			err = nil
		}
		return model.Badge{}, false, statementError(err, ctx)
	}
	return model.Badge{Text: humanCount(est.Int64)}, true, nil
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
