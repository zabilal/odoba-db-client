package transfer

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestColumnsArePairedByName(t *testing.T) {
	from := named([]string{"Order ID", "Customer-Name", "note", "extra"})
	to := []model.ColumnDef{{Name: "order_id"}, {Name: "customer_name"}, {Name: "notes"}}
	if got := Suggest(from, to); !reflect.DeepEqual(got, []Pair{{From: 0, To: "order_id"}, {From: 1, To: "customer_name"}}) {
		t.Errorf("a name is the same whatever its case, spaces, underscores and hyphens: %v", got)
	}
	if got := Suggest(named([]string{"id", "ID"}), []model.ColumnDef{{Name: "id"}}); !reflect.DeepEqual(got, []Pair{{From: 0, To: "id"}}) {
		t.Errorf("a table column is filled once, by the first file column with its name: %v", got)
	}
	if got := Suggest(named([]string{"id"}), []model.ColumnDef{{Name: "ID"}, {Name: "id"}}); !reflect.DeepEqual(got, []Pair{{From: 0, To: "ID"}}) {
		t.Errorf("of two table columns with one name, the first is filled: %v", got)
	}
	if got := Suggest(from, nil); got != nil {
		t.Errorf("nothing to pair with: %v", got)
	}
}

// columns is a table's columns by name.
func columns(cols ...model.ColumnDef) map[string]model.ColumnDef {
	out := map[string]model.ColumnDef{}
	for _, c := range cols {
		out[c.Name] = c
	}
	return out
}

func col(name string, class model.TypeClass, nullable bool) model.ColumnDef {
	return model.ColumnDef{Name: name, Type: model.DataType{Class: class, Nullable: nullable}}
}

func TestValuesAreMadeTheirColumnsType(t *testing.T) {
	to := columns(col("id", model.TypeInteger, false), col("price", model.TypeDecimal, true),
		col("when", model.TypeTimestamp, true), col("ok", model.TypeBool, true), col("name", model.TypeString, false))
	pairs := []Pair{{0, "id"}, {1, "price"}, {2, "when"}, {3, "ok"}, {4, "name"}}
	from := named(make([]string, 5))
	at := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	vals, errs := Coerce(model.Row{"7", model.Decimal("12.50"), at, true, "x"}, from, pairs, to)
	if len(errs) != 0 || !reflect.DeepEqual(vals, []any{int64(7), model.Decimal("12.50"), at, true, "x"}) {
		t.Errorf("text read as its column's type, a typed value as the same, a date as it is: %v %v", vals, errs)
	}
	vals, errs = Coerce(model.Row{model.Decimal("3"), nil, "", "yes"}, from, pairs, to)
	if !reflect.DeepEqual(vals, []any{int64(3), nil, nil, true, nil}) {
		t.Errorf("a JSON number into whole numbers; NULL and an empty field where NULL can be: %v", vals)
	}
	if len(errs) != 1 || errs[0].Column != "name" || !errors.Is(errs[0].Err, errNeedsValue) || errs[0].Value != "NULL" {
		t.Errorf("a row too short for a column that cannot be NULL: %v", errs)
	}
}

func TestEveryValueThatCannotBeMadeIsSaid(t *testing.T) {
	to := columns(col("id", model.TypeInteger, false), col("price", model.TypeDecimal, true),
		col("when", model.TypeTimestamp, true), col("ok", model.TypeBool, true), col("name", model.TypeString, false))
	pairs := []Pair{{0, "id"}, {1, "price"}, {2, "when"}, {3, "ok"}, {4, "name"}}
	_, errs := Coerce(model.Row{"seven", "abc", "not a date", "maybe", nil}, named(make([]string, 5)), pairs, to)
	var got []string
	for _, e := range errs {
		got = append(got, e.Column)
	}
	if !reflect.DeepEqual(got, []string{"id", "price", "when", "ok", "name"}) {
		t.Fatalf("each column's, in order: %v", got)
	}
	if s := errs[0].Error(); s != "id: not a whole number (seven)" {
		t.Errorf("the column, why, and the value: %q", s)
	}
}

func TestAnExcelDateIsTextInAColumnOfText(t *testing.T) {
	at := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
	vals, errs := Coerce(model.Row{at}, named(make([]string, 1)), []Pair{{0, "note"}}, columns(col("note", model.TypeString, true)))
	if s, ok := vals[0].(string); len(errs) != 0 || !ok || !strings.Contains(s, "2023-01-01") {
		t.Errorf("%v %v", vals, errs)
	}
}
