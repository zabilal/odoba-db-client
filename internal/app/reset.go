package app

import (
	"context"
	"errors"

	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// ResetOffsets moves where a consumer group will read from (FR-13.13).
//
// It is AccessAdmin, and the driver refuses it before it dials where the
// connection is read-only or wants asking (FR-13.21). Nothing here relaxes
// that: this is the call, not the guard.
func ResetOffsets(ctx context.Context, src source.Source, req source.ResetRequest) (err error) {
	defer panics.Recover(&err, "moving a group's offsets")
	r, ok := src.(source.OffsetResetter)
	if !ok {
		return errors.New("this source cannot move a consumer group's offsets")
	}
	return r.ResetOffsets(ctx, req)
}
