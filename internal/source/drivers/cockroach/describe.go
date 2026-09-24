package cockroach

import (
	"context"
	"fmt"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Describe loads an object's whole structure (FR-2.4).
//
// What was declared is read from pg_catalog, which answers about any
// database in the cluster when its tables are named with one. What renders
// a definition is asked for by name instead — SHOW CREATE, SHOW INDEXES,
// SHOW CONSTRAINTS — because the catalogue functions that render one from
// an OID look it up in the database the connection is attached to, and
// answer nothing at all for an OID from another (ADR-0145). Nothing is the
// worst way for an answer to be wrong: a check constraint would simply
// lose its expression, with no sign that it had one.
func (s *crdbSource) Describe(ctx context.Context, ref model.ObjectRef) (any, error) {
	if len(ref.Path) < 3 {
		return nil, fmt.Errorf("cockroach: incomplete reference %s", ref)
	}
	switch ref.Kind {
	case model.KindTable:
		return s.describeTable(ctx, ref)
	case model.KindView, model.KindMaterializedView:
		return s.describeView(ctx, ref)
	}
	return nil, fmt.Errorf("cockroach: %s cannot be described", ref.Kind)
}

func (s *crdbSource) describeTable(ctx context.Context, ref model.ObjectRef) (*model.Table, error) {
	cols, err := s.columns(ctx, ref)
	if err != nil {
		return nil, err
	}
	t := &model.Table{Name: ref.Path[2], Columns: cols, RowsEstimate: -1}
	if t.Comment, err = s.comment(ctx, ref); err != nil {
		return nil, err
	}
	if err := s.constraints(ctx, ref, t); err != nil {
		return nil, err
	}
	if t.Indexes, err = s.indexes(ctx, ref); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *crdbSource) describeView(ctx context.Context, ref model.ObjectRef) (*model.View, error) {
	cols, err := s.columns(ctx, ref)
	if err != nil {
		return nil, err
	}
	v := &model.View{Name: ref.Path[2], Columns: cols,
		Materialized: ref.Kind == model.KindMaterializedView}
	if v.Comment, err = s.comment(ctx, ref); err != nil {
		return nil, err
	}
	if v.Definition, err = s.createStatement(ctx, ref); err != nil {
		return nil, err
	}
	return v, nil
}

// comment is what somebody wrote about the object itself.
func (s *crdbSource) comment(ctx context.Context, ref model.ObjectRef) (string, error) {
	p, err := s.conn()
	if err != nil {
		return "", err
	}
	db := ref.Path[0]
	var comment string
	err = p.QueryRow(ctx, `
		SELECT COALESCE(d.description, '')
		FROM `+s.catalog(db, "pg_class")+` c
		JOIN `+s.catalog(db, "pg_namespace")+` n ON n.oid = c.relnamespace
		LEFT JOIN `+s.catalog(db, "pg_description")+` d
		       ON d.objoid = c.oid AND d.objsubid = 0
		WHERE n.nspname = $1 AND c.relname = $2`, ref.Path[1], ref.Path[2]).Scan(&comment)
	if err != nil {
		return "", statementError(err, ctx)
	}
	return comment, nil
}

// createStatement is the object as the engine would write it.
func (s *crdbSource) createStatement(ctx context.Context, ref model.ObjectRef) (string, error) {
	p, err := s.conn()
	if err != nil {
		return "", err
	}
	var stmt string
	if err := p.QueryRow(ctx, `SELECT create_statement FROM [SHOW CREATE `+
		s.QualifyRef(ref)+`]`).Scan(&stmt); err != nil {
		return "", statementError(err, ctx)
	}
	return stmt, nil
}

// constraints fills in the key, the foreign keys, the uniques and the
// checks.
//
// Their shape comes from pg_constraint, which is read rather than parsed;
// a check's expression comes from the engine's own listing, which is the
// only place it can be had for a table in another database.
func (s *crdbSource) constraints(ctx context.Context, ref model.ObjectRef, t *model.Table) error {
	p, err := s.conn()
	if err != nil {
		return err
	}
	db, schema, rel := ref.Path[0], ref.Path[1], ref.Path[2]
	rows, err := p.Query(ctx, `
		SELECT con.conname, con.contype::text,
		       COALESCE((SELECT array_agg(a.attname ORDER BY k.ord)
		                 FROM unnest(con.conkey) WITH ORDINALITY AS k(attnum, ord)
		                 JOIN `+s.catalog(db, "pg_attribute")+` a
		                   ON a.attrelid = con.conrelid AND a.attnum = k.attnum), '{}'),
		       COALESCE(fn.nspname, ''), COALESCE(fc.relname, ''),
		       COALESCE((SELECT array_agg(a.attname ORDER BY k.ord)
		                 FROM unnest(con.confkey) WITH ORDINALITY AS k(attnum, ord)
		                 JOIN `+s.catalog(db, "pg_attribute")+` a
		                   ON a.attrelid = con.confrelid AND a.attnum = k.attnum), '{}'),
		       COALESCE(con.confdeltype::text, ''), COALESCE(con.confupdtype::text, '')
		FROM `+s.catalog(db, "pg_constraint")+` con
		JOIN `+s.catalog(db, "pg_class")+` c ON c.oid = con.conrelid
		JOIN `+s.catalog(db, "pg_namespace")+` n ON n.oid = c.relnamespace
		LEFT JOIN `+s.catalog(db, "pg_class")+` fc ON fc.oid = con.confrelid
		LEFT JOIN `+s.catalog(db, "pg_namespace")+` fn ON fn.oid = fc.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2 AND con.contype IN ('p', 'f', 'u', 'c')
		ORDER BY con.conname`, schema, rel)
	if err != nil {
		return statementError(err, ctx)
	}
	defer rows.Close()

	type check struct{ name string }
	var checks []check
	for rows.Next() {
		var name, ctype, refSchema, refTable, onDelete, onUpdate string
		var cols, refCols []string
		if err := rows.Scan(&name, &ctype, &cols, &refSchema, &refTable,
			&refCols, &onDelete, &onUpdate); err != nil {
			return statementError(err, ctx)
		}
		// The query above says which kinds are wanted; this sorts what it
		// asked for. Naming the four again here would be a second filter
		// for the same thing, and then neither could be shown to matter.
		switch ctype {
		case "p":
			t.PrimaryKey = &model.PrimaryKey{Name: name, Columns: cols}
		case "u":
			t.Uniques = append(t.Uniques, model.UniqueConstraint{Name: name, Columns: cols})
		case "f":
			t.ForeignKeys = append(t.ForeignKeys, model.ForeignKey{
				Name: name, Columns: cols, RefSchema: refSchema, RefTable: refTable,
				RefColumns: refCols, OnDelete: action(onDelete), OnUpdate: action(onUpdate),
			})
		default:
			checks = append(checks, check{name: name})
		}
	}
	if err := rows.Err(); err != nil {
		return statementError(err, ctx)
	}
	if len(checks) == 0 {
		return nil
	}
	expressions, err := s.checkExpressions(ctx, ref)
	if err != nil {
		return err
	}
	for _, c := range checks {
		t.Checks = append(t.Checks, model.CheckConstraint{
			Name: c.name, Expression: expressions[c.name]})
	}
	return nil
}

// checkExpressions reads each check's predicate from the engine's listing,
// which names the table rather than looking an OID up in whichever
// database this connection happens to be attached to.
func (s *crdbSource) checkExpressions(ctx context.Context, ref model.ObjectRef) (map[string]string, error) {
	p, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := p.Query(ctx, `SELECT constraint_name, details FROM [SHOW CONSTRAINTS FROM `+
		s.QualifyRef(ref)+`] WHERE constraint_type = 'CHECK'`)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, details string
		if err := rows.Scan(&name, &details); err != nil {
			return nil, statementError(err, ctx)
		}
		out[name] = strings.TrimSpace(strings.TrimPrefix(details, "CHECK"))
	}
	return out, rows.Err()
}

// action reads pg_constraint's one-letter referential action.
func action(code string) model.ReferentialAction {
	switch code {
	case "c":
		return model.ActionCascade
	case "n":
		return model.ActionSetNull
	case "d":
		return model.ActionSetDefault
	case "r":
		return model.ActionRestrict
	}
	return model.ActionNoAction
}

// indexes reads a table's indexes from the engine's own listing.
//
// Two of its columns are the reason it is read rather than pg_index. A
// secondary index here carries the primary key's columns at its end so
// that it can find the row, and those are marked implicit: they are the
// engine's doing and not part of what anybody declared, so an index shown
// with them would be an index nobody wrote. The other, storing, is the
// payload an index carries without ordering by it, which is what
// model.Index calls Include.
func (s *crdbSource) indexes(ctx context.Context, ref model.ObjectRef) ([]model.Index, error) {
	p, err := s.conn()
	if err != nil {
		return nil, err
	}
	rows, err := p.Query(ctx, `SELECT index_name, non_unique, column_name, direction, storing, implicit
		FROM [SHOW INDEXES FROM `+s.QualifyRef(ref)+`] ORDER BY index_name, seq_in_index`)
	if err != nil {
		return nil, statementError(err, ctx)
	}
	defer rows.Close()

	var out []model.Index
	at := map[string]int{}
	for rows.Next() {
		var name, column, direction string
		var nonUnique, storing, implicit bool
		if err := rows.Scan(&name, &nonUnique, &column, &direction, &storing, &implicit); err != nil {
			return nil, statementError(err, ctx)
		}
		i, ok := at[name]
		if !ok {
			i = len(out)
			at[name] = i
			out = append(out, model.Index{Name: name, Unique: !nonUnique, Method: "btree"})
		}
		switch {
		case storing:
			out[i].Include = append(out[i].Include, column)
		case implicit:
			// The key the engine appended so the index can find its row.
		default:
			out[i].Columns = append(out[i].Columns,
				model.IndexColumn{Name: column, Descending: direction == "DESC"})
		}
	}
	return out, rows.Err()
}
