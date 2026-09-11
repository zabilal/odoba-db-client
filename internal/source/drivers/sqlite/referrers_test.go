package sqlite

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func TestReferrersListTheKeysThatReferToATable(t *testing.T) {
	ctx := context.Background()
	path := fixture(t)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// A key that names no column refers to the table's primary key, and a
	// table is named in whatever case.
	if _, err := db.ExecContext(ctx, `CREATE TABLE notes (person_id INTEGER REFERENCES People, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s := open(t, path, source.Guard{})
	r, ok := any(s).(source.Referrer)
	if !ok {
		t.Fatal("SQLite lists what refers to a table")
	}
	refs, err := r.Referrers(ctx, model.NewRef(model.KindTable, "main", "people"))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("notes and orders refer to people: %+v", refs)
	}
	notes, orders := refs[0], refs[1] // in the order of their tables
	if !orders.From.Equal(model.NewRef(model.KindTable, "main", "orders")) || !slices.Equal(orders.Key.Columns, []string{"person_id"}) ||
		!slices.Equal(orders.Key.RefColumns, []string{"id"}) || orders.Key.RefTable != "people" || orders.Key.OnDelete != "CASCADE" {
		t.Errorf("orders' key: %+v", orders)
	}
	if !notes.From.Equal(model.NewRef(model.KindTable, "main", "notes")) || !slices.Equal(notes.Key.RefColumns, []string{"id"}) {
		t.Errorf("a key naming no column refers to the primary key: %+v", notes)
	}
	v, err := s.Describe(ctx, model.NewRef(model.KindTable, "main", "notes"))
	if err != nil {
		t.Fatal(err)
	}
	if fk := v.(*model.Table).ForeignKeys; len(fk) != 1 || !slices.Equal(fk[0].RefColumns, []string{"id"}) {
		t.Errorf("described, the same key names the primary key too: %+v", fk)
	}
	if refs, err := r.Referrers(ctx, model.NewRef(model.KindTable, "main", "orders")); err != nil || len(refs) != 0 {
		t.Errorf("nothing refers to orders: %+v %v", refs, err)
	}
	if _, err := r.Referrers(ctx, model.NewRef(model.KindView, "main", "adults")); err == nil {
		t.Error("a view is refused: no key refers to one")
	}
}
