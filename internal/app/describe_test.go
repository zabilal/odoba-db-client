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

func TestInferShapeNeedsASourceThatSamples(t *testing.T) {
	// A source whose structure the server declares has none to sample, and
	// says so rather than panicking on a nil interface.
	_, err := InferShape(context.Background(), &fakeSource{}, model.NewRef(model.KindTable, "t"), 10)
	if err == nil {
		t.Fatal("a source that cannot sample was asked to")
	}
	if !strings.Contains(err.Error(), "sampled") {
		t.Errorf("error %q, want it to say why", err)
	}
}
