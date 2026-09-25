package firebird

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Introspection reads the RDB$ catalogue, which is where Firebird keeps
// everything about itself: there is no information_schema, and the RDB$
// tables are ordinary tables anybody may select from.
//
// Two things about them shape every query here. A name is stored padded or
// not depending on the column, so every name is TRIMmed on the way out. And
// RDB$SYSTEM_FLAG marks the engine's own objects, but it is NULL rather than 0
// on objects made by older versions, so it is always read through COALESCE —
// without that, a database restored from an old backup shows none of its
// tables.

// userObjects excludes the engine's own. The name test is belt and braces:
// the flag is authoritative, and a database whose flag was lost would still
// not show RDB$RELATIONS as one of its tables.
const userObjects = `COALESCE(%[1]s.RDB$SYSTEM_FLAG, 0) = 0
	AND %[1]s.%[2]s NOT STARTING WITH 'RDB$'
	AND %[1]s.%[2]s NOT STARTING WITH 'MON$'
	AND %[1]s.%[2]s NOT STARTING WITH 'SEC$'`

func userIn(alias, nameColumn string) string {
	return fmt.Sprintf(userObjects, alias, nameColumn)
}

// Root is the database's object classes (FR-2.2); a connection is to one
// database, and Firebird has no schemas to put between.
func (s *firebirdSource) Root(ctx context.Context) ([]model.Node, error) {
	counts := map[model.ObjectKind]int64{}
	// One statement for every class, because five round trips to paint a tree
	// is five times the latency for the same answer (NFR-P2).
	rows, err := s.db.QueryContext(ctx, `
		SELECT 'table', COUNT(*) FROM RDB$RELATIONS r
			WHERE `+userIn("r", "RDB$RELATION_NAME")+` AND r.RDB$VIEW_BLR IS NULL
		UNION ALL SELECT 'view', COUNT(*) FROM RDB$RELATIONS r
			WHERE `+userIn("r", "RDB$RELATION_NAME")+` AND r.RDB$VIEW_BLR IS NOT NULL
		UNION ALL SELECT 'index', COUNT(*) FROM RDB$INDICES i
			WHERE `+userIn("i", "RDB$INDEX_NAME")+`
		UNION ALL SELECT 'trigger', COUNT(*) FROM RDB$TRIGGERS t
			WHERE `+userIn("t", "RDB$TRIGGER_NAME")+`
		UNION ALL SELECT 'routine', COUNT(*) FROM RDB$PROCEDURES p
			WHERE `+userIn("p", "RDB$PROCEDURE_NAME")+`
		UNION ALL SELECT 'function', COUNT(*) FROM RDB$FUNCTIONS f
			WHERE `+userIn("f", "RDB$FUNCTION_NAME")+`
		UNION ALL SELECT 'sequence', COUNT(*) FROM RDB$GENERATORS g
			WHERE `+userIn("g", "RDB$GENERATOR_NAME"))
	if err != nil {
		return nil, statementError(err, ctx, "")
	}
	defer rows.Close()
	for rows.Next() {
		var class string
		var n int64
		if err := rows.Scan(&class, &n); err != nil {
			return nil, err
		}
		// Procedures and functions are two catalogue tables and one class:
		// both are routines, and the tree groups them as every other driver
		// does rather than inventing a class Firebird's own tools do not use.
		if k, ok := classKinds[class]; ok {
			counts[k] += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	db := model.NewRef(model.KindDatabase, s.database())
	out := model.ClassNodes(db, counts)
	if len(out) == 0 {
		// An empty database still shows its Tables, so the tree does not look
		// broken. A brand new Firebird database is exactly this case.
		out = []model.Node{model.ClassNode(db, model.KindTable, 0)}
	}
	return out, nil
}

var classKinds = map[string]model.ObjectKind{
	"table": model.KindTable, "view": model.KindView, "index": model.KindIndex,
	"trigger": model.KindTrigger, "routine": model.KindRoutine,
	"function": model.KindRoutine, "sequence": model.KindSequence,
}

func (s *firebirdSource) Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error) {
	switch ref.Kind {
	case model.KindFolder:
		kind, _ := model.ClassOf(ref)
		return s.classChildren(ctx, kind)
	case model.KindTable, model.KindView:
		cols, err := s.columns(ctx, ref.Name())
		if err != nil {
			return nil, err
		}
		out := make([]model.Node, len(cols))
		for i, c := range cols {
			out[i] = model.Node{
				Ref:   model.NewRef(model.KindColumn, s.database(), ref.Name(), c.Name),
				Label: c.Name, Attrs: map[string]string{"type": c.Type.Native}}
		}
		return out, nil
	}
	return nil, nil
}

// classChildren lists one class of object.
func (s *firebirdSource) classChildren(ctx context.Context, kind model.ObjectKind) ([]model.Node, error) {
	switch kind {
	case model.KindTable, model.KindView:
		test := "IS NULL"
		if kind == model.KindView {
			test = "IS NOT NULL"
		}
		return s.nodes(ctx, kind, `SELECT TRIM(r.RDB$RELATION_NAME), ''
			FROM RDB$RELATIONS r WHERE `+userIn("r", "RDB$RELATION_NAME")+`
			AND r.RDB$VIEW_BLR `+test+` ORDER BY 1`, true)
	case model.KindIndex:
		return s.nodes(ctx, kind, `SELECT TRIM(i.RDB$INDEX_NAME), TRIM(i.RDB$RELATION_NAME)
			FROM RDB$INDICES i WHERE `+userIn("i", "RDB$INDEX_NAME")+` ORDER BY 2, 1`, false)
	case model.KindTrigger:
		return s.nodes(ctx, kind, `SELECT TRIM(t.RDB$TRIGGER_NAME), TRIM(COALESCE(t.RDB$RELATION_NAME, ''))
			FROM RDB$TRIGGERS t WHERE `+userIn("t", "RDB$TRIGGER_NAME")+` ORDER BY 2, 1`, false)
	case model.KindRoutine:
		// Both catalogue tables, in one list, ordered together: a person
		// looking for a name does not know which of the two it is in.
		return s.nodes(ctx, kind, `SELECT TRIM(p.RDB$PROCEDURE_NAME), 'procedure'
			FROM RDB$PROCEDURES p WHERE `+userIn("p", "RDB$PROCEDURE_NAME")+`
			UNION ALL SELECT TRIM(f.RDB$FUNCTION_NAME), 'function'
			FROM RDB$FUNCTIONS f WHERE `+userIn("f", "RDB$FUNCTION_NAME")+` ORDER BY 1`, false)
	case model.KindSequence:
		return s.nodes(ctx, kind, `SELECT TRIM(g.RDB$GENERATOR_NAME), ''
			FROM RDB$GENERATORS g WHERE `+userIn("g", "RDB$GENERATOR_NAME")+` ORDER BY 1`, false)
	}
	return nil, fmt.Errorf("firebird: no such class %s", kind)
}

// nodes runs a listing of (name, note) and builds the tree's nodes from it.
// A note is what the object is on or what it is, and is drawn beside the
// name; opens says whether the object has anything under it.
func (s *firebirdSource) nodes(ctx context.Context, kind model.ObjectKind, query string, opens bool) ([]model.Node, error) {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, statementError(err, ctx, query)
	}
	defer rows.Close()
	var out []model.Node
	for rows.Next() {
		var name, note string
		if err := rows.Scan(&name, &note); err != nil {
			return nil, err
		}
		n := model.Node{Ref: model.NewRef(kind, s.database(), name), Label: name,
			HasChildren: opens, Browsable: opens}
		switch {
		case note == "":
		case kind == model.KindRoutine:
			n.Attrs = map[string]string{"kind": note}
		default: // an index or a trigger, named with the table it is on
			n.Label, n.Attrs = model.OnTable(name, note), map[string]string{"table": note}
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// columns reads a table's or view's columns.
//
// A column's type lives in RDB$FIELDS, which holds one row per domain;
// RDB$RELATION_FIELDS names the domain each column uses. Every column made
// without a domain of its own gets an automatic one called RDB$n, so the join
// is always there. NOT NULL and the default may be on either — a column can
// tighten what its domain allows — so the column's own answer wins and the
// domain's is the fallback.
func (s *firebirdSource) columns(ctx context.Context, table string) ([]model.Column, error) {
	const q = `SELECT TRIM(rf.RDB$FIELD_NAME), f.RDB$FIELD_TYPE, COALESCE(f.RDB$FIELD_SUB_TYPE, 0),
			COALESCE(f.RDB$CHARACTER_LENGTH, f.RDB$FIELD_LENGTH, 0),
			COALESCE(f.RDB$FIELD_PRECISION, 0), COALESCE(f.RDB$FIELD_SCALE, 0),
			COALESCE(rf.RDB$NULL_FLAG, f.RDB$NULL_FLAG, 0), rf.RDB$FIELD_POSITION,
			CAST(COALESCE(rf.RDB$DEFAULT_SOURCE, f.RDB$DEFAULT_SOURCE) AS VARCHAR(8191)),
			CAST(rf.RDB$DESCRIPTION AS VARCHAR(8191)),
			COALESCE(rf.RDB$IDENTITY_TYPE, -1),
			CAST(f.RDB$COMPUTED_SOURCE AS VARCHAR(8191))
		FROM RDB$RELATION_FIELDS rf
		JOIN RDB$FIELDS f ON f.RDB$FIELD_NAME = rf.RDB$FIELD_SOURCE
		WHERE rf.RDB$RELATION_NAME = ? ORDER BY rf.RDB$FIELD_POSITION`
	rows, err := s.db.QueryContext(ctx, q, table)
	if err != nil {
		return nil, statementError(err, ctx, q)
	}
	defer rows.Close()
	var out []model.Column
	for rows.Next() {
		var name string
		var typ, sub, length, precision, scale, notNull, position, identity int
		var dflt, comment, computed sql.NullString
		if err := rows.Scan(&name, &typ, &sub, &length, &precision, &scale,
			&notNull, &position, &dflt, &comment, &identity, &computed); err != nil {
			return nil, err
		}
		dt := dataType(typ, sub, length, precision, scale)
		dt.Nullable = notNull == 0
		c := model.Column{Name: name, Type: dt, Position: position + 1, Comment: comment.String}
		if dflt.Valid {
			// The catalogue keeps the source text of the clause, "DEFAULT"
			// and all. What a default is, is the expression.
			c.Default = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(dflt.String), "DEFAULT"))
			c.HasDefault = c.Default != ""
		}
		if identity >= 0 {
			// 0 is GENERATED ALWAYS, 1 is BY DEFAULT. Both are an identity;
			// only the second can be written to directly, which is what
			// AutoIncrement means to the grid.
			c.Identity = true
			c.AutoIncrement = identity == 1
		}
		if computed.Valid {
			c.Generated = strings.TrimSpace(computed.String)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Describe loads an object's structure.
func (s *firebirdSource) Describe(ctx context.Context, ref model.ObjectRef) (any, error) {
	name := ref.Name()
	switch ref.Kind {
	case model.KindView:
		cols, err := s.columns(ctx, name)
		if err != nil {
			return nil, err
		}
		var def, comment sql.NullString
		if err := s.db.QueryRowContext(ctx, `SELECT CAST(RDB$VIEW_SOURCE AS VARCHAR(8191)),
				CAST(RDB$DESCRIPTION AS VARCHAR(8191))
			FROM RDB$RELATIONS WHERE RDB$RELATION_NAME = ?`, name).Scan(&def, &comment); err != nil {
			return nil, statementError(err, ctx, "")
		}
		return &model.View{Name: name, Columns: cols,
			Definition: strings.TrimSpace(def.String), Comment: comment.String}, nil
	case model.KindTable:
		return s.describeTable(ctx, name)
	}
	return nil, fmt.Errorf("firebird: cannot describe %s", ref)
}

func (s *firebirdSource) describeTable(ctx context.Context, name string) (*model.Table, error) {
	cols, err := s.columns(ctx, name)
	if err != nil {
		return nil, err
	}
	var comment sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT CAST(RDB$DESCRIPTION AS VARCHAR(8191))
		FROM RDB$RELATIONS WHERE RDB$RELATION_NAME = ?`, name).Scan(&comment); err != nil {
		return nil, statementError(err, ctx, "")
	}
	t := &model.Table{Name: name, Columns: cols, Comment: comment.String, RowsEstimate: -1}
	if t.PrimaryKey, t.Uniques, err = s.keys(ctx, name); err != nil {
		return nil, err
	}
	if t.Indexes, err = s.indexes(ctx, name); err != nil {
		return nil, err
	}
	if t.ForeignKeys, err = s.foreignKeys(ctx, name); err != nil {
		return nil, err
	}
	if t.Checks, err = s.checks(ctx, name); err != nil {
		return nil, err
	}
	if t.Triggers, err = s.triggers(ctx, name); err != nil {
		return nil, err
	}
	return t, nil
}

// keys reads a table's primary key and its unique constraints. Both are
// constraints backed by an index, and the index's segments are their columns.
func (s *firebirdSource) keys(ctx context.Context, table string) (*model.PrimaryKey, []model.UniqueConstraint, error) {
	const q = `SELECT TRIM(rc.RDB$CONSTRAINT_NAME), TRIM(rc.RDB$CONSTRAINT_TYPE), TRIM(sg.RDB$FIELD_NAME)
		FROM RDB$RELATION_CONSTRAINTS rc
		JOIN RDB$INDEX_SEGMENTS sg ON sg.RDB$INDEX_NAME = rc.RDB$INDEX_NAME
		WHERE rc.RDB$RELATION_NAME = ? AND rc.RDB$CONSTRAINT_TYPE IN ('PRIMARY KEY', 'UNIQUE')
		ORDER BY rc.RDB$CONSTRAINT_TYPE, rc.RDB$CONSTRAINT_NAME, sg.RDB$FIELD_POSITION`
	rows, err := s.db.QueryContext(ctx, q, table)
	if err != nil {
		return nil, nil, statementError(err, ctx, q)
	}
	defer rows.Close()
	var pk *model.PrimaryKey
	var uniq []model.UniqueConstraint
	for rows.Next() {
		var name, kind, column string
		if err := rows.Scan(&name, &kind, &column); err != nil {
			return nil, nil, err
		}
		if kind == "PRIMARY KEY" {
			if pk == nil {
				pk = &model.PrimaryKey{Name: name}
			}
			pk.Columns = append(pk.Columns, column)
			continue
		}
		if len(uniq) == 0 || uniq[len(uniq)-1].Name != name {
			uniq = append(uniq, model.UniqueConstraint{Name: name})
		}
		u := &uniq[len(uniq)-1]
		u.Columns = append(u.Columns, column)
	}
	return pk, uniq, rows.Err()
}

// indexes reads a table's indexes, leaving out the ones that are a
// constraint: a primary key, a unique constraint and a foreign key each have
// an index, and reporting them here would describe the same thing twice.
func (s *firebirdSource) indexes(ctx context.Context, table string) ([]model.Index, error) {
	const q = `SELECT TRIM(i.RDB$INDEX_NAME), COALESCE(i.RDB$UNIQUE_FLAG, 0),
			COALESCE(i.RDB$INDEX_TYPE, 0), COALESCE(i.RDB$INDEX_INACTIVE, 0),
			CAST(i.RDB$EXPRESSION_SOURCE AS VARCHAR(8191)), TRIM(COALESCE(sg.RDB$FIELD_NAME, ''))
		FROM RDB$INDICES i
		LEFT JOIN RDB$INDEX_SEGMENTS sg ON sg.RDB$INDEX_NAME = i.RDB$INDEX_NAME
		WHERE i.RDB$RELATION_NAME = ? AND COALESCE(i.RDB$SYSTEM_FLAG, 0) = 0
			AND i.RDB$INDEX_NAME NOT IN (
				SELECT rc.RDB$INDEX_NAME FROM RDB$RELATION_CONSTRAINTS rc
				WHERE rc.RDB$RELATION_NAME = i.RDB$RELATION_NAME AND rc.RDB$INDEX_NAME IS NOT NULL)
		ORDER BY i.RDB$INDEX_NAME, sg.RDB$FIELD_POSITION`
	rows, err := s.db.QueryContext(ctx, q, table)
	if err != nil {
		return nil, statementError(err, ctx, q)
	}
	defer rows.Close()
	var out []model.Index
	for rows.Next() {
		var name, column string
		var unique, descending, inactive int
		var expr sql.NullString
		if err := rows.Scan(&name, &unique, &descending, &inactive, &expr, &column); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Name != name {
			ix := model.Index{Name: name, Unique: unique == 1, Method: "btree"}
			if inactive == 1 {
				// An index the engine is not using. Saying so matters: a
				// query that should be fast and is not is often this.
				ix.Attrs = map[string]string{"inactive": "true"}
			}
			out = append(out, ix)
		}
		ix := &out[len(out)-1]
		// An index type of 1 is Firebird's descending index; the direction
		// belongs to the whole index rather than to each column, which is
		// where the standard puts it and where the model expects it.
		switch {
		case expr.Valid:
			ix.Columns = append(ix.Columns, model.IndexColumn{
				Expression: strings.TrimSpace(expr.String), Descending: descending == 1})
		case column != "":
			ix.Columns = append(ix.Columns, model.IndexColumn{Name: column, Descending: descending == 1})
		}
	}
	return out, rows.Err()
}

// foreignKeys reads a table's foreign keys. Firebird records the referenced
// side as the name of the unique constraint the key points at, so the columns
// on the far side are that constraint's index segments, matched position for
// position with this side's.
func (s *firebirdSource) foreignKeys(ctx context.Context, table string) ([]model.ForeignKey, error) {
	const q = `SELECT TRIM(rc.RDB$CONSTRAINT_NAME), TRIM(uq.RDB$RELATION_NAME),
			TRIM(sg.RDB$FIELD_NAME), TRIM(usg.RDB$FIELD_NAME),
			TRIM(ref.RDB$UPDATE_RULE), TRIM(ref.RDB$DELETE_RULE)
		FROM RDB$RELATION_CONSTRAINTS rc
		JOIN RDB$REF_CONSTRAINTS ref ON ref.RDB$CONSTRAINT_NAME = rc.RDB$CONSTRAINT_NAME
		JOIN RDB$RELATION_CONSTRAINTS uq ON uq.RDB$CONSTRAINT_NAME = ref.RDB$CONST_NAME_UQ
		JOIN RDB$INDEX_SEGMENTS sg ON sg.RDB$INDEX_NAME = rc.RDB$INDEX_NAME
		JOIN RDB$INDEX_SEGMENTS usg ON usg.RDB$INDEX_NAME = uq.RDB$INDEX_NAME
			AND usg.RDB$FIELD_POSITION = sg.RDB$FIELD_POSITION
		WHERE rc.RDB$RELATION_NAME = ? AND rc.RDB$CONSTRAINT_TYPE = 'FOREIGN KEY'
		ORDER BY rc.RDB$CONSTRAINT_NAME, sg.RDB$FIELD_POSITION`
	rows, err := s.db.QueryContext(ctx, q, table)
	if err != nil {
		return nil, statementError(err, ctx, q)
	}
	defer rows.Close()
	var out []model.ForeignKey
	for rows.Next() {
		var name, refTable, column, refColumn, onUpdate, onDelete string
		if err := rows.Scan(&name, &refTable, &column, &refColumn, &onUpdate, &onDelete); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Name != name {
			out = append(out, model.ForeignKey{Name: name, RefTable: refTable,
				OnUpdate: model.ReferentialAction(onUpdate),
				OnDelete: model.ReferentialAction(onDelete)})
		}
		fk := &out[len(out)-1]
		fk.Columns = append(fk.Columns, column)
		fk.RefColumns = append(fk.RefColumns, refColumn)
	}
	return out, rows.Err()
}

// checks reads a table's CHECK constraints. Firebird implements each one as a
// pair of triggers and keeps the condition in their source; the BEFORE INSERT
// one of the pair is taken, both saying the same thing.
func (s *firebirdSource) checks(ctx context.Context, table string) ([]model.CheckConstraint, error) {
	const q = `SELECT TRIM(rc.RDB$CONSTRAINT_NAME), CAST(t.RDB$TRIGGER_SOURCE AS VARCHAR(8191))
		FROM RDB$RELATION_CONSTRAINTS rc
		JOIN RDB$CHECK_CONSTRAINTS cc ON cc.RDB$CONSTRAINT_NAME = rc.RDB$CONSTRAINT_NAME
		JOIN RDB$TRIGGERS t ON t.RDB$TRIGGER_NAME = cc.RDB$TRIGGER_NAME
		WHERE rc.RDB$RELATION_NAME = ? AND rc.RDB$CONSTRAINT_TYPE = 'CHECK'
			AND t.RDB$TRIGGER_TYPE = 1
		ORDER BY rc.RDB$CONSTRAINT_NAME`
	rows, err := s.db.QueryContext(ctx, q, table)
	if err != nil {
		return nil, statementError(err, ctx, q)
	}
	defer rows.Close()
	var out []model.CheckConstraint
	for rows.Next() {
		var name string
		var expr sql.NullString
		if err := rows.Scan(&name, &expr); err != nil {
			return nil, err
		}
		out = append(out, model.CheckConstraint{Name: name, Expression: strings.TrimSpace(expr.String)})
	}
	return out, rows.Err()
}

// triggers reads a table's triggers, leaving out the ones Firebird made for a
// CHECK constraint: those are the constraint, reported as one.
func (s *firebirdSource) triggers(ctx context.Context, table string) ([]model.Trigger, error) {
	const q = `SELECT TRIM(t.RDB$TRIGGER_NAME), t.RDB$TRIGGER_TYPE,
			CAST(t.RDB$TRIGGER_SOURCE AS VARCHAR(8191))
		FROM RDB$TRIGGERS t
		WHERE t.RDB$RELATION_NAME = ? AND COALESCE(t.RDB$SYSTEM_FLAG, 0) = 0
			AND t.RDB$TRIGGER_NAME NOT IN (SELECT cc.RDB$TRIGGER_NAME FROM RDB$CHECK_CONSTRAINTS cc)
		ORDER BY t.RDB$TRIGGER_SEQUENCE, t.RDB$TRIGGER_NAME`
	rows, err := s.db.QueryContext(ctx, q, table)
	if err != nil {
		return nil, statementError(err, ctx, q)
	}
	defer rows.Close()
	var out []model.Trigger
	for rows.Next() {
		var name string
		var typ int64
		var body sql.NullString
		if err := rows.Scan(&name, &typ, &body); err != nil {
			return nil, err
		}
		timing, events := triggerTiming(typ)
		out = append(out, model.Trigger{Name: name, Timing: timing, Events: events,
			ForEachRow: true, Definition: strings.TrimSpace(body.String)})
	}
	return out, rows.Err()
}

// triggerTiming reads Firebird's trigger type number.
//
// For the ones on a table it is a bitmask: the low bit is 1 for BEFORE and 0
// for AFTER, and what follows says which events, so 1 is BEFORE INSERT, 2 is
// AFTER INSERT, 3 BEFORE UPDATE and so on up to 6. Firebird 2.1 added
// multi-event triggers, numbered from 8192 up with two bits per event, which
// is a different encoding in the same column; those are read as the events
// they are, with a timing of their own.
//
// Every Firebird trigger fires per row. There is no statement-level trigger,
// which is why nothing here decides that.
func triggerTiming(typ int64) (timing string, events []string) {
	if typ >= 8192 {
		// Bits 1 and 2 hold the first event, 3 and 4 the second, 5 and 6 the
		// third; within each pair 1 means INSERT, 2 UPDATE and 3 DELETE, and
		// the slot is empty when both bits are clear. The timing is one bit
		// for the whole trigger.
		timing = "AFTER"
		if typ&1 == 0 {
			timing = "BEFORE"
		}
		for shift := uint(1); shift <= 5; shift += 2 {
			switch (typ >> shift) & 3 {
			case 1:
				events = append(events, "INSERT")
			case 2:
				events = append(events, "UPDATE")
			case 3:
				events = append(events, "DELETE")
			}
		}
		return timing, events
	}
	if typ < 1 || typ > 6 {
		// A DATABASE or DDL trigger, which is not on a table and is never
		// reached from here; saying nothing is better than guessing.
		return "", nil
	}
	timing = "BEFORE"
	if typ%2 == 0 {
		timing = "AFTER"
	}
	switch (typ + 1) / 2 {
	case 1:
		events = []string{"INSERT"}
	case 2:
		events = []string{"UPDATE"}
	case 3:
		events = []string{"DELETE"}
	}
	return timing, events
}

// Badge has nothing to add: Firebird keeps no row estimate in its catalogue,
// and a count is a scan, which is what the grid's own count is for (FR-2.5).
func (s *firebirdSource) Badge(context.Context, model.ObjectRef) (model.Badge, bool, error) {
	return model.Badge{}, false, nil
}
