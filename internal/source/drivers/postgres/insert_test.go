package postgres

import (
	"math"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestInsertLiteralsReadBackAsTheirValues(t *testing.T) {
	when := time.Date(2024, 5, 6, 7, 8, 9, 123000000, time.UTC)
	date := model.DataType{Class: model.TypeDate}
	ts := model.DataType{Class: model.TypeTimestamp}
	tz := model.DataType{Class: model.TypeTimestamp, TimeZone: true}
	text := model.DataType{Class: model.TypeString}
	for _, c := range []struct {
		v    any
		t    model.DataType
		want string
	}{
		{nil, text, "NULL"},
		{true, model.DataType{}, "TRUE"},
		{int64(-7), model.DataType{}, "-7"},
		{1.5, model.DataType{}, "1.5"},
		{math.NaN(), model.DataType{}, "'NaN'::float8"},
		{math.Inf(-1), model.DataType{}, "'-Infinity'::float8"},
		{model.Decimal("12.3400"), model.DataType{}, "12.3400"},
		{model.Decimal("NaN"), model.DataType{}, "'NaN'"},
		{`it's \ fine`, text, `'it''s \ fine'`},
		{[]byte{0, 255}, model.DataType{}, `'\x00ff'::bytea`},
		{when, date, "'2024-05-06'"},
		{when, ts, "'2024-05-06 07:08:09.123'"},
		{when, tz, "'2024-05-06 07:08:09.123+00:00'"},
		{model.JSON(`{"a": 1}`), model.DataType{}, `'{"a": 1}'`},
		{[]any{"a", nil}, model.DataType{Native: "text[]", Element: &text}, "ARRAY['a', NULL]::text[]"},
		{[]any{}, model.DataType{Native: "int8[]"}, "'{}'::int8[]"},
	} {
		got, err := insertLiteral(c.v, c.t)
		if err != nil || got != c.want {
			t.Errorf("%#v: %s, %v; want %s", c.v, got, err, c.want)
		}
	}
	if _, err := insertLiteral("a\x00b", text); err == nil {
		t.Error("text holding NUL cannot be written for PostgreSQL")
	}
	if _, err := insertLiteral(map[string]any{"k": "v"}, model.DataType{Native: "hstore"}); err == nil {
		t.Error("a value with no literal must be an error, not a guess")
	}
}

func TestInsertRowsWritesOneStatementARow(t *testing.T) {
	cols := []model.ColumnDef{{Name: "id"}, {Name: "odd \"name\""}}
	got, err := d.InsertRows(model.NewRef(model.KindTable, "db", "public", "t"), cols,
		[]model.Row{{int64(1), "x"}, {int64(2), nil}})
	want := `INSERT INTO "public"."t" ("id", "odd ""name""") VALUES (1, 'x');` + "\n" +
		`INSERT INTO "public"."t" ("id", "odd ""name""") VALUES (2, NULL);` + "\n"
	if err != nil || got != want {
		t.Errorf("%q, %v\nwant %q", got, err, want)
	}
}

func TestGeometryIsWrittenAsExtendedWKB(t *testing.T) {
	point := []byte{1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xf0, 0x3f, 0, 0, 0, 0, 0, 0, 0, 0x40} // POINT(1 2)
	got, err := insertLiteral(model.Geometry{SRID: 4326, WKB: point}, model.DataType{})
	want := `ST_GeomFromEWKB('\x0101000020e6100000000000000000f03f0000000000000040'::bytea)`
	if err != nil || got != want {
		t.Errorf("%s, %v\nwant %s", got, err, want)
	}
	if got, _ := insertLiteral(model.Geometry{WKB: point}, model.DataType{}); got != `ST_GeomFromEWKB('\x0101000000000000000000f03f0000000000000040'::bytea)` {
		t.Errorf("SRID 0 needs no flag: %s", got)
	}
}
