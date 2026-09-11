package app

import (
	"context"
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// describer answers Describe and nothing else; the rest of Source is nil.
type describer struct {
	source.Source
	panics bool
}

func (d describer) Describe(context.Context, model.ObjectRef) (any, error) {
	if d.panics {
		panic("the driver fell over")
	}
	return &model.Table{Name: "t"}, nil
}

func TestDescribePassesThroughAndContainsAPanic(t *testing.T) {
	ref := model.NewRef(model.KindTable, "db", "t")
	got, err := Describe(context.Background(), describer{}, ref)
	if tb, ok := got.(*model.Table); err != nil || !ok || tb.Name != "t" {
		t.Errorf("describe: %v, %v", got, err)
	}
	_, err = Describe(context.Background(), describer{panics: true}, ref)
	if err == nil || !strings.Contains(err.Error(), "the driver fell over") {
		t.Errorf("a panicking describe should come back as an error, got %v", err)
	}
}
