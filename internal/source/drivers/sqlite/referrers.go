package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Referrers lists the foreign keys of other tables that refer to a table,
// each with the table it is in (FR-3.11, ADR-0041): every table's foreign
// keys, in one statement, kept where they name this table, as SQLite does,
// without regard to case.
func (s *sqliteSource) Referrers(ctx context.Context, ref model.ObjectRef) ([]model.Referrer, error) {
	if ref.Kind != model.KindTable {
		return nil, fmt.Errorf("sqlite: cannot list what refers to %s", ref)
	}
	name := ref.Name()
	rows, err := s.db.QueryContext(ctx, `SELECT m.name, f.id, f."from", f."to", f.on_update, f.on_delete
		FROM sqlite_schema m JOIN pragma_foreign_key_list(m.name) f
		WHERE m.type = 'table' AND f."table" = ? COLLATE NOCASE
		ORDER BY m.name, f.id, f.seq`, name)
	if err != nil {
		return nil, statementError(err)
	}
	var out []model.Referrer
	lastTable, lastID := "", -1
	for rows.Next() {
		var table, from, onUpdate, onDelete string
		var id int
		var to sql.NullString // NULL where the key refers to the table's primary key without naming it
		if err := rows.Scan(&table, &id, &from, &to, &onUpdate, &onDelete); err != nil {
			rows.Close()
			return nil, err
		}
		if table != lastTable || id != lastID {
			out = append(out, model.Referrer{From: model.NewRef(model.KindTable, "main", table),
				Key: model.ForeignKey{RefSchema: "main", RefTable: name,
					OnUpdate: model.ReferentialAction(onUpdate), OnDelete: model.ReferentialAction(onDelete)}})
			lastTable, lastID = table, id
		}
		k := &out[len(out)-1].Key
		k.Columns = append(k.Columns, from)
		k.RefColumns = append(k.RefColumns, to.String)
	}
	err = rows.Err()
	rows.Close() // before the next statement: a database may hold one connection
	if err != nil {
		return nil, err
	}
	for i := range out {
		if err := s.implicitKey(ctx, &out[i].Key); err != nil {
			return nil, statementError(err)
		}
	}
	return out, nil
}

// implicitKey names the columns a foreign key refers to where it names none:
// the primary key of the table it refers to, which is what SQLite takes.
func (s *sqliteSource) implicitKey(ctx context.Context, k *model.ForeignKey) error {
	if !slices.Contains(k.RefColumns, "") {
		return nil
	}
	pk, err := s.primaryKey(ctx, k.RefTable)
	if err != nil {
		return err
	}
	if len(pk) == len(k.Columns) {
		k.RefColumns = pk
	}
	return nil
}
