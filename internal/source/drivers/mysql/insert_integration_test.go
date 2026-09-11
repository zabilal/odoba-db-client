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

// Rows read from one table, written as INSERT into a twin and read back,
// must come back exactly as they were (source.RowScripter).
func TestInsertRowsRoundTripOnMySQL(t *testing.T) {
	each(t, func(t *testing.T, srv server) {
		ctx := context.Background()
		db, err := sql.Open("mysql", fmt.Sprintf("root:%s@tcp(127.0.0.1:%d)/ikigai_it?multiStatements=true",
			env("IKIGAI_MYSQL_PASSWORD", "ikigai"), srv.addr()))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		const shape = `(id INT PRIMARY KEY, name VARCHAR(100), body TEXT, amount DECIMAL(30,10), ratio DOUBLE,
			pic BLOB, born DATE, seen DATETIME(6), meta JSON, flags BIT(8), kind ENUM('a','b'), yr YEAR)`
		for _, s := range []string{"DROP TABLE IF EXISTS rt_src, rt_dst", "CREATE TABLE rt_src " + shape, "CREATE TABLE rt_dst " + shape} {
			if _, err := db.Exec(s); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(`INSERT INTO rt_src VALUES (?,?,?,?,?,?,?,?,?,?,?,?), (?,?,?,?,?,?,?,?,?,?,?,?)`,
			1, `it's "odd" \ fine`, "line one\nline two\ttab", "12345678901234567890.1234567890", 1e300, []byte{0, 255},
			"2000-01-02", "2024-05-06 07:08:09.123456", `{"a": [1, "x\ny"], "b": "back\\slash"}`, []byte{5}, "b", 2024,
			2, "", nil, "-0.25", -0.25, []byte{}, nil, nil, nil, nil, nil, nil); err != nil {
			t.Fatal(err)
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
		want, cols := read("rt_src")
		stmt, err := src.(source.RowScripter).InsertRows(ref("rt_dst"), cols, want)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
		if got, _ := read("rt_dst"); !reflect.DeepEqual(got, want) {
			t.Errorf("read back\n%#v\nwant\n%#v\nfrom\n%s", got, want, stmt)
		}
	})
}
