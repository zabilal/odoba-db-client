package app

import (
	"context"
	"errors"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Describe loads an object's full structure for the structure tab
// (FR-2.4): a *model.Table for a table, a *model.View for a view. A driver's
// panic comes back as an error (ADR-0017).
func Describe(ctx context.Context, src source.Source, ref model.ObjectRef) (_ any, err error) {
	defer panics.Recover(&err, "describing an object")
	return src.Describe(ctx, ref)
}

// InferShape samples an object's documents and reports the fields they hold
// (FR-12.4). A source whose structure the server declares has none to infer,
// and says so rather than answering with an empty shape.
func InferShape(ctx context.Context, src source.Source, ref model.ObjectRef, n int) (_ *model.DocumentShape, err error) {
	defer panics.Recover(&err, "sampling an object's documents")
	inf, ok := src.(source.ShapeInferrer)
	if !ok {
		return nil, errors.New("this source's structure is the server's own, not sampled from its data")
	}
	return inf.InferShape(ctx, ref, n)
}

// Referrers lists the foreign keys of other tables that refer to a table,
// where the source lists them; none where it does not (FR-3.11).
func Referrers(ctx context.Context, src source.Source, ref model.ObjectRef) (_ []model.Referrer, err error) {
	defer panics.Recover(&err, "listing what refers to a table")
	r, ok := src.(source.Referrer)
	if !ok {
		return nil, nil
	}
	return r.Referrers(ctx, ref)
}
