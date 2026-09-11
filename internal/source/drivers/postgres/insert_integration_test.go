//go:build conformance

package postgres

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Rows read from one table, written as INSERT into a twin and read back,
// must come back exactly as they were (source.RowScripter).
func TestInsertRowsRoundTripOnPostgreSQL(t *testing.T) {
	src := openSource(t, false)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	const shape = `(id int PRIMARY KEY, name text, amount numeric(30,10), ratio float8, pic bytea,
		born date, seen timestamp, at timestamptz, meta jsonb, tags text[], days date[], uid uuid,
		span interval, clock time, ok boolean)`
	for _, s := range []string{"DROP TABLE IF EXISTS ikigai_it.rt_src, ikigai_it.rt_dst",
		"CREATE TABLE ikigai_it.rt_src " + shape, "CREATE TABLE ikigai_it.rt_dst " + shape} {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { conn.Exec(context.Background(), "DROP TABLE IF EXISTS ikigai_it.rt_src, ikigai_it.rt_dst") })
	if _, err := conn.Exec(ctx, `INSERT INTO ikigai_it.rt_src VALUES
		(1, $$it's "odd" \ fine$$ || E'\nsecond line', 12345678901234567890.1234567890, 1e300, '\x00ff',
		 '2000-01-02', '2024-05-06 07:08:09.123456', '2024-05-06 07:08:09.123456+02', '{"a": [1, "x\ny"]}',
		 ARRAY['a', NULL, 'b''c'], ARRAY['2024-01-01'::date], 'a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11',
		 '1 day 02:03:04.5', '07:08:09.25', true),
		(2, '', -0.25, 'Infinity', '', NULL, NULL, NULL, NULL, '{}', NULL, NULL, NULL, NULL, false)`); err != nil {
		t.Fatal(err)
	}
	ref := func(table string) model.ObjectRef {
		return model.NewRef(model.KindTable, ordersRef.Path[0], "ikigai_it", table)
	}
	read := func(table string) ([]model.Row, []model.ColumnDef) {
		t.Helper()
		rs, err := src.Browse(ctx, ref(table), source.BrowseOptions{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		cols := rs.Columns()
		return drain(t, rs), cols
	}
	want, cols := read("rt_src")
	stmt, err := src.InsertRows(ref("rt_dst"), cols, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, stmt); err != nil {
		t.Fatalf("%v\n%s", err, stmt)
	}
	if got, _ := read("rt_dst"); !reflect.DeepEqual(got, want) {
		t.Errorf("read back\n%#v\nwant\n%#v\nfrom\n%s", got, want, stmt)
	}
}
