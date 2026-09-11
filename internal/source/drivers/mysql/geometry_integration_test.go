//go:build conformance

package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Geometry read from MySQL or MariaDB shows as WKT and, written back as
// INSERT, reads back unchanged.
func TestGeometryRoundTripOnMySQL(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		ctx := context.Background()
		db, err := sql.Open("mysql", fmt.Sprintf("root:%s@tcp(127.0.0.1:%d)/ikigai_it?multiStatements=true",
			env("IKIGAI_MYSQL_PASSWORD", "ikigai"), srv.addr()))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		for _, s := range []string{"DROP TABLE IF EXISTS geo_src, geo_dst",
			"CREATE TABLE geo_src (id INT PRIMARY KEY, shape GEOMETRY)",
			"CREATE TABLE geo_dst (id INT PRIMARY KEY, shape GEOMETRY)",
			`INSERT INTO geo_src VALUES
			  (1, ST_GeomFromText('POINT(1.5 -2.25)')),
			  (2, ST_GeomFromText('POLYGON((0 0,4 0,4 4,0 4,0 0),(1 1,2 1,2 2,1 1))', 3857)),
			  (3, ST_GeomFromText('GEOMETRYCOLLECTION(POINT(1 2),LINESTRING(0 0,3 4))')),
			  (4, NULL)`} {
			if _, err := db.Exec(s); err != nil {
				t.Fatalf("%s: %v", s, err)
			}
		}
		src, err := Driver{}.Open(ctx, srv.config("ikigai_it", source.Guard{}))
		if err != nil {
			t.Fatal(err)
		}
		defer src.Close()
		ref := func(table string) model.ObjectRef { return model.NewRef(model.KindTable, "ikigai_it", table) }
		read := func(table string) ([]model.Row, []model.ColumnDef) {
			t.Helper()
			rs, err := src.Browse(ctx, ref(table), source.BrowseOptions{Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			cols := rs.Columns()
			return drain(t, rs), cols
		}
		want, cols := read("geo_src")
		for i, text := range []string{"POINT(1.5 -2.25)", "SRID=3857;POLYGON((0 0,4 0,4 4,0 4,0 0),(1 1,2 1,2 2,1 1))",
			"GEOMETRYCOLLECTION(POINT(1 2),LINESTRING(0 0,3 4))"} {
			g, ok := want[i][1].(model.Geometry)
			if !ok || g.String() != text {
				t.Errorf("row %d: %#v, shown %q; want %q", i+1, want[i][1], g.String(), text)
			}
		}
		stmt, err := src.(source.RowScripter).InsertRows(ref("geo_dst"), cols, want)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
		if got, _ := read("geo_dst"); !reflect.DeepEqual(got, want) {
			t.Errorf("read back\n%#v\nwant\n%#v", got, want)
		}
	})
}
