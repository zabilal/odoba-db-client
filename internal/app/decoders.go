package app

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// DecoderStore remembers which decoder a topic's key and value are read with,
// by connection, so that a topic opened again reads as it did before
// (FR-13.7). Nil keeps the choice for as long as the view lives and no
// longer.
type DecoderStore interface {
	Decoder(ctx context.Context, connID, field, topic string) (localdb.DecoderChoice, bool, error)
	PutDecoder(ctx context.Context, connID, field, topic string, c localdb.DecoderChoice) error
}

// The fields of a record that are decoded, which are the two that carry bytes.
const (
	DecodeKey   = "key"
	DecodeValue = "value"
)
