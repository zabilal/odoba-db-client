package app

import (
	"context"
	"errors"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Produce writes one record to a topic and says where it landed (FR-13.11).
//
// It is a write, and the driver refuses it before anything is dialled where
// the connection is read-only or is tagged production and no consent came
// with this record (FR-13.21, FR-4.9). Nothing here relaxes that: this is the
// call, not the guard.
func Produce(ctx context.Context, src source.Source, rec source.ProduceRequest) (_ model.TopicPartition, _ int64, err error) {
	defer panics.Recover(&err, "writing a record")
	p, ok := src.(source.StreamProducer)
	if !ok {
		return model.TopicPartition{}, 0, errors.New("this source cannot write records")
	}
	return p.Produce(ctx, rec)
}
