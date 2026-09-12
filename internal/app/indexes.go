package app

import (
	"context"
	"errors"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Making and unmaking indexes (FR-6.3, FR-12.1). Every change is planned
// first, so that what will be sent can be read before it is sent (FR-6.4).

// ErrNoIndexes is a source whose indexes are not managed on their own.
var ErrNoIndexes = errors.New("app: this source does not manage indexes on their own")

// PlanIndex renders making an index on an object.
func PlanIndex(ctx context.Context, src source.Source, ref model.ObjectRef,
	idx model.DocumentIndex, confirmed bool) (_ *source.WritePlan, err error) {
	defer panics.Recover(&err, "planning an index")
	im, ok := indexes(src)
	if !ok {
		return nil, ErrNoIndexes
	}
	return im.PlanIndex(ctx, ref, idx, confirmed)
}

// PlanDropIndex renders unmaking one.
func PlanDropIndex(ctx context.Context, src source.Source, ref model.ObjectRef,
	name string, confirmed bool) (_ *source.WritePlan, err error) {
	defer panics.Recover(&err, "planning an index")
	im, ok := indexes(src)
	if !ok {
		return nil, ErrNoIndexes
	}
	return im.PlanDropIndex(ctx, ref, name, confirmed)
}

// ApplyIndex runs a plan one of the two made.
func ApplyIndex(ctx context.Context, src source.Source, plan *source.WritePlan) (_ *source.WriteOutcome, err error) {
	defer panics.Recover(&err, "changing an index")
	im, ok := indexes(src)
	if !ok {
		return nil, ErrNoIndexes
	}
	return im.ApplyIndex(ctx, plan)
}

// indexes is the source's index management, where it claims to have it.
func indexes(src source.Source) (source.IndexManager, bool) {
	im, ok := src.(source.IndexManager)
	if !ok || !src.Capabilities().Schema.Indexes {
		return nil, false
	}
	return im, true
}
