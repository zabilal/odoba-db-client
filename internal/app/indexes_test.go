package app

import (
	"context"
	"errors"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

func TestIndexChangesNeedASourceThatManagesThem(t *testing.T) {
	ctx := context.Background()
	ref := model.NewRef(model.KindCollection, "shop", "people")
	idx := model.DocumentIndex{Name: "name_1", Keys: []model.IndexColumn{{Name: "name"}}}
	src := &fakeSource{} // a source with no index management at all

	if _, err := PlanIndex(ctx, src, ref, idx, false); !errors.Is(err, ErrNoIndexes) {
		t.Errorf("planning an index on such a source: %v", err)
	}
	if _, err := PlanDropIndex(ctx, src, ref, "name_1", false); !errors.Is(err, ErrNoIndexes) {
		t.Errorf("planning a drop: %v", err)
	}
	if _, err := ApplyIndex(ctx, src, &source.WritePlan{Target: ref}); !errors.Is(err, ErrNoIndexes) {
		t.Errorf("applying one: %v", err)
	}
}
