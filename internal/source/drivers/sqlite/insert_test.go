package sqlite

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func TestInsertLiteralsReadBackAsTheirValues(t *testing.T) {
	when := time.Date(2024, 5, 6, 7, 8, 9, 123000000, time.UTC)
	day := time.Date(2000, 1, 2, 0, 0, 0, 0, time.UTC)
	date := model.DataType{Class: model.TypeDate}
	ts := model.DataType{Class: model.TypeTimestamp}
	for _, c := range []struct {
		v    any
		t    model.DataType
		want string
	}{
		{nil, ts, "NULL"},
		{true, model.DataType{}, "1"},
		{2.0, model.DataType{}, "2.0"},
		{math.Inf(1), model.DataType{}, "9e999"},
		{math.NaN(), model.DataType{}, "NULL"},
		{"it's", model.DataType{}, "'it''s'"},
		{"a\x00b", model.DataType{}, "CAST(X'610062' AS TEXT)"},
		{[]byte{0xab}, model.DataType{}, "X'ab'"},
		{day, date, "'2000-01-02'"},
		{day, ts, "'2000-01-02 00:00:00'"},
		{when, ts, "'2024-05-06 07:08:09.123'"},
		{when.In(time.FixedZone("", 7200)), ts, "'2024-05-06 09:08:09.123+02:00'"},
	} {
		got, err := insertLiteral(c.v, c.t)
		if err != nil || got != c.want {
			t.Errorf("%#v: %s, %v; want %s", c.v, got, err, c.want)
		}
	}
}

// Rows read from one table, written as INSERT into a twin and read back,
// must come back exactly as they were.
func TestInsertRowsReadBackAsTheyWere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rt.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const shape = `(id INTEGER PRIMARY KEY, name TEXT, score REAL, whole REAL, amount NUMERIC,
		born DATE, seen DATETIME, meta JSON, pic BLOB, flag BOOLEAN)`
	for _, s := range []string{"CREATE TABLE src " + shape, "CREATE TABLE dst " + shape} {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO src VALUES (?,?,?,?,?,?,?,?,?,?), (?,?,?,?,?,?,?,?,?,?)`,
		1, "it's \"odd\" \\ fine\nsecond line", 1.5, 2.0, "12.50", "2000-01-02", "2024-05-06 07:08:09.123",
		`{"a":[1,"x\ny"]}`, []byte{0, 255}, 1,
		2, "", -0.25, 1e300, nil, nil, nil, nil, nil, 0); err != nil {
		t.Fatal(err)
	}
	s := open(t, path, source.Guard{})
	read := func(table string) ([]model.Row, []model.ColumnDef) {
		t.Helper()
		rs, err := s.Browse(context.Background(), model.NewRef(model.KindTable, "main", table), source.BrowseOptions{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		cols := rs.Columns()
		return drain(t, rs), cols
	}
	want, cols := read("src")
	stmt, err := s.InsertRows(model.NewRef(model.KindTable, "main", "dst"), cols, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(stmt); err != nil {
		t.Fatalf("%v\n%s", err, stmt)
	}
	if got, _ := read("dst"); !reflect.DeepEqual(got, want) {
		t.Errorf("read back\n%#v\nwant\n%#v\nfrom\n%s", got, want, stmt)
	}
}
