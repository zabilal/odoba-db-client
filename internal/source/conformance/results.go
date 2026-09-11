package conformance

import (
	"context"
	"slices"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// checkEditableResults queries the Writable table, a column renamed, and
// expects the result to say where each column came from and to be known by
// the table's key (FR-4.8, ADR-0035). A result with a computed column, or
// without the key, is known by none. A result keeps its key read on a
// connection of its own, and held in memory while the next statement of its
// script runs (ADR-0036).
func checkEditableResults(t *testing.T, target Target) {
	if target.Writable.IsZero() {
		t.Skip("no Writable target configured")
	}
	ctx := context.Background()
	src := target.Open(ctx, t)
	defer src.Close()
	if !src.Capabilities().Query.EditableResults {
		t.Skip("source does not claim EditableResults")
	}
	d, dialect := src.(source.Dialect)
	sn, sessions := src.(source.Sessioner)
	if !dialect || !sessions {
		t.Fatal("claims EditableResults with no dialect and no sessions to query with")
	}
	ss, err := sn.Session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	q := d.QuoteIdentifier
	query := func(sel string) (model.RowIdentity, []model.ColumnDef) {
		t.Helper()
		res, err := ss.Query(ctx, source.Statement{SQL: "SELECT " + sel + " FROM " + d.QualifyRef(target.Writable)})
		if err != nil || res.Rows == nil {
			t.Fatalf("%v, rows %v", err, res)
		}
		defer res.Rows.Close()
		return model.IdentityOf(res.Rows), res.Rows.Columns()
	}

	id, cols := query(q("id") + ", " + q("name") + " AS " + q("label"))
	if id.Kind == model.IdentityNone || !slices.Equal(id.Columns, []string{"id"}) || !id.Target.Equal(target.Writable) {
		t.Errorf("a query of one table, its key among the columns, is known by that key: %+v", id)
	}
	if len(cols) != 2 || cols[1].Name != "label" || cols[1].OriginColumn != "name" || !cols[1].Origin.Equal(target.Writable) {
		t.Errorf("a column renamed says its name in the table: %+v", cols)
	}
	if id, _ := query(q("id") + ", 1 + 1 AS " + q("two")); id.Kind != model.IdentityNone {
		t.Errorf("a computed column makes the result no one table's: %+v", id)
	}
	if id, cols := query(q("name")); id.Kind != model.IdentityNone || len(cols) != 1 || cols[0].OriginColumn != "name" {
		t.Errorf("without the key a result is known by none, though its column says where it is from: %+v %+v", id, cols)
	}

	sel := "SELECT " + q("id") + ", " + q("name") + " FROM " + d.QualifyRef(target.Writable)
	if qr, ok := src.(source.Queryer); ok {
		res, err := qr.Query(ctx, source.Statement{SQL: sel})
		if err != nil || res.Rows == nil {
			t.Fatalf("%v, rows %v", err, res)
		}
		if id := model.IdentityOf(res.Rows); !id.Editable() {
			t.Errorf("a result read on a connection of its own keeps its key: %+v", id)
		}
		res.Rows.Close()
	}
	ch, err := ss.QueryMulti(ctx, sel+"; "+sel, source.ScriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []model.RowIdentity
	for r := range ch {
		if r.Err != nil || r.Result == nil || r.Result.Rows == nil {
			t.Fatalf("statement %d: %v", r.Index, r.Err)
		}
		ids = append(ids, model.IdentityOf(r.Result.Rows))
		r.Result.Rows.Close()
	}
	if len(ids) != 2 || !ids[0].Editable() || !ids[1].Editable() {
		t.Errorf("each result of a script keeps its key, the first held in memory while the second runs: %+v", ids)
	}
}
