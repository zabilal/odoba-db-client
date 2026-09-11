package export

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestWriteFormatsRowsInHand(t *testing.T) {
	cols := []model.ColumnDef{{Name: "id", Type: model.DataType{Class: model.TypeInteger}}, {Name: "name"}}
	rows := []model.Row{{int64(1), "a\tb"}, {nil, "x"}}
	var b strings.Builder
	if err := Write(&b, cols, rows, Options{Format: TSV}); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "1\t\"a\tb\"\n\tx\n"; got != want {
		t.Errorf("TSV %q, want %q", got, want)
	}
	b.Reset()
	if err := Write(&b, cols, rows[:1], Options{Format: CSV, Header: true}); err != nil {
		t.Fatal(err)
	}
	if got, want := b.String(), "id,name\n1,a\tb\n"; got != want {
		t.Errorf("CSV %q, want %q", got, want)
	}
}

func TestMarkdownKeepsEachValueInItsCell(t *testing.T) {
	cols := []model.ColumnDef{{Name: "n", Type: model.DataType{Class: model.TypeDecimal}}, {Name: "a|b"}}
	rows := []model.Row{{model.Decimal("1.50"), "x|y\nz"}, {nil, "NULL"}}
	var b strings.Builder
	if err := Write(&b, cols, rows, Options{Format: Markdown}); err != nil {
		t.Fatal(err)
	}
	want := "| n | a\\|b |\n| ---: | --- |\n| 1.50 | x\\|y<br>z |\n| _NULL_ | NULL |\n"
	if got := b.String(); got != want {
		t.Errorf("markdown\n%s\nwant\n%s", got, want)
	}
}
