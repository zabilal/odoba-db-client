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
