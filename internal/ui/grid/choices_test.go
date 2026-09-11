package grid

import (
	"reflect"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

func TestABoolIsPickedAndTextTyped(t *testing.T) {
	col := func(class model.TypeClass, nullable bool) model.ColumnDef {
		return model.ColumnDef{Name: "c", Type: model.DataType{Class: class, Nullable: nullable}}
	}
	if got := choices(col(model.TypeBool, true)); !reflect.DeepEqual(got, []any{true, false, nil}) {
		t.Errorf("a nullable bool is picked from true, false and NULL: %v", got)
	}
	if choices(col(model.TypeEnum, false)) != nil || choices(col(model.TypeString, true)) != nil {
		t.Error("an enum whose labels are not known, and text, are typed")
	}
}
