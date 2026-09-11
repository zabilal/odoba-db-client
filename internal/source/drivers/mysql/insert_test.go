package mysql

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
	text := model.DataType{Class: model.TypeString}
	for _, c := range []struct {
		v    any
		t    model.DataType
		want string
	}{
		{nil, text, "NULL"},
		{true, model.DataType{}, "TRUE"},
		{int64(-7), model.DataType{}, "-7"},
		{model.Decimal("12345678901234567890.1234567890"), model.DataType{}, "12345678901234567890.1234567890"},
		{"it's", text, "'it''s'"},
		{`back\slash`, text, "_utf8mb4 X'6261636b5c736c617368'"},
		{[]byte{1, 2}, model.DataType{}, "X'0102'"},
		{when, date, "'2024-05-06'"},
		{when, ts, "'2024-05-06 07:08:09.123'"},
		{time.Time{}, date, "'0000-00-00'"},
		{model.JSON(`{"a":"x\ny"}`), model.DataType{}, "_utf8mb4 X'7b2261223a22785c6e79227d'"}, // JSON's \n is a backslash
		{model.JSON(`{"a":1}`), model.DataType{}, `'{"a":1}'`},
	} {
		got, err := insertLiteral(c.v, c.t)
		if err != nil || got != c.want {
			t.Errorf("%#v: %s, %v; want %s", c.v, got, err, c.want)
		}
	}
	if _, err := insertLiteral(math.NaN(), model.DataType{}); err == nil {
		t.Error("MySQL has no NaN to write")
	}
}

func TestInsertRowsWritesOneStatementARow(t *testing.T) {
	cols := []model.ColumnDef{{Name: "id"}, {Name: "na`me"}}
	got, err := dialect{}.InsertRows(model.NewRef(model.KindTable, "shop", "t"), cols, []model.Row{{int64(1), "x"}})
	want := "INSERT INTO `shop`.`t` (`id`, `na``me`) VALUES (1, 'x');\n"
	if err != nil || got != want {
		t.Errorf("%q, %v\nwant %q", got, err, want)
	}
}
