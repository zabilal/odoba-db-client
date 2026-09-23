package sqlserver

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Rows written as the INSERT statements that would add them (FR-3.7).
//
// Each literal must read back, on the same server, as the value it was
// written from. Values are literals here and not parameters because the text
// is for a person to keep, read or run elsewhere.

func TestAValueIsWrittenSoItReadsBackTheSame(t *testing.T) {
	cases := []struct {
		v    any
		dt   model.DataType
		want string
	}{
		{nil, dataType("int"), "NULL"},
		{true, dataType("bit"), "1"},
		{false, dataType("bit"), "0"},
		{int64(-7), dataType("int"), "-7"},
		{1.5, dataType("float"), "1.5"},
		{model.Decimal("12345678901234567890.1234567890"), dataType("decimal"),
			"12345678901234567890.1234567890"},
		{model.Decimal("1e3"), dataType("decimal"), "1e3"},
		{model.Decimal("not a number"), dataType("decimal"), "N'not a number'"},
		{"hello", dataType("nvarchar"), "N'hello'"},
		{"it's", dataType("nvarchar"), "N'it''s'"},
		{"a\nb", dataType("nvarchar"), "N'a\nb'"},
		{"naïve ✓", dataType("nvarchar"), "N'naïve ✓'"},
		{model.JSON(`{"a":1}`), dataType("json"), `N'{"a":1}'`},
		{[]byte{0xde, 0xad}, dataType("varbinary"), "0xdead"},
		{[]byte{}, dataType("varbinary"), "0x"},
		{map[string]any{"a": float64(1)}, dataType("nvarchar"), `N'{"a":1}'`},
		{[]any{int64(1), "x"}, dataType("nvarchar"), `N'[1,"x"]'`},
	}
	for _, c := range cases {
		got, err := insertLiteral(c.v, c.dt)
		if err != nil {
			t.Errorf("insertLiteral(%#v): %v", c.v, err)
			continue
		}
		if got != c.want {
			t.Errorf("insertLiteral(%#v) = %s, want %s", c.v, got, c.want)
		}
	}
}

// A time is written as the column holds it: a date has no clock, a time no
// day, and only a datetimeoffset carries its offset.
func TestATimeIsWrittenAsTheColumnHoldsIt(t *testing.T) {
	when := time.Date(2024, 3, 9, 14, 5, 6, 123456700, time.FixedZone("here", 2*3600))
	cases := []struct {
		dt   model.DataType
		want string
	}{
		{dataType("date"), "'2024-03-09'"},
		{dataType("time"), "'14:05:06.1234567'"},
		{dataType("datetime2"), "'2024-03-09 14:05:06.1234567'"},
		{dataType("datetimeoffset"), "'2024-03-09 14:05:06.1234567 +02:00'"},
	}
	for _, c := range cases {
		got, err := insertLiteral(when, c.dt)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("a %s is written %s, want %s", c.dt.Native, got, c.want)
		}
	}
}

// A NUL cannot be inside a literal: the parser reads to the quote, and there
// is no escape to write one with. The text goes as the bytes it is made of.
func TestTextHoldingANulIsWrittenAsItsBytes(t *testing.T) {
	got, err := insertLiteral("a\x00b", dataType("nvarchar"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "CONVERT(nvarchar(max), 0x610000006200)" {
		t.Errorf("it is written %s", got)
	}
}

// What SQL Server has no value for is refused rather than written as
// something else.
func TestAValueTheServerCannotHoldIsRefused(t *testing.T) {
	for _, v := range []any{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got, err := insertLiteral(v, dataType("float")); err == nil {
			t.Errorf("%v was written as %s", v, got)
		}
	}
	if _, err := insertLiteral(struct{}{}, dataType("sql_variant")); err == nil {
		t.Error("a value of no known kind was written")
	}
}

// The statements name the table and its columns, one statement a row.
func TestRowsAreWrittenAsInserts(t *testing.T) {
	cols := []model.ColumnDef{
		{Name: "id", Type: dataType("int")},
		{Name: "full name", Type: dataType("nvarchar")},
	}
	got, err := tsql.InsertRows(tbl, cols, []model.Row{{int64(1), "a"}, {int64(2), nil}})
	if err != nil {
		t.Fatal(err)
	}
	want := "INSERT INTO [dbo].[people] ([id], [full name]) VALUES (1, N'a');\n" +
		"INSERT INTO [dbo].[people] ([id], [full name]) VALUES (2, NULL);\n"
	if got != want {
		t.Errorf("it wrote\n%s\nwant\n%s", got, want)
	}
	if _, err := tsql.InsertRows(tbl, cols, []model.Row{{int64(1), struct{}{}}}); err == nil ||
		!strings.Contains(err.Error(), "sqlserver") {
		t.Errorf("a value that cannot be written said %v", err)
	}
}
