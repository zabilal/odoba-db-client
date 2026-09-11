package app

import (
	"context"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// fallingReferrer is a source whose Referrers panics.
type fallingReferrer struct{ *scriptSource }

func (fallingReferrer) Referrers(context.Context, model.ObjectRef) ([]model.Referrer, error) {
	panic("the driver fell over")
}

func TestReferrersAreNoneWhereTheSourceListsNone(t *testing.T) {
	ctx, ref := context.Background(), model.NewRef(model.KindTable, "t")
	if refs, err := Referrers(ctx, &scriptSource{}, ref); refs != nil || err != nil {
		t.Errorf("a source that lists no referrers has none: %v %v", refs, err)
	}
	if _, err := Referrers(ctx, fallingReferrer{&scriptSource{}}, ref); err == nil {
		t.Error("a driver that falls over listing them is an error, not a crash")
	}
}
