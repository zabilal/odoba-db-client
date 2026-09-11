package app

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// ScratchStore keeps query text that is saved nowhere else, so that neither
// a crash nor quitting loses it (NFR-R2, NFR-R3).
type ScratchStore interface {
	PutScratch(ctx context.Context, sc localdb.Scratch) error
	Scratches(ctx context.Context) ([]localdb.Scratch, error)
	DeleteScratch(ctx context.Context, id string) error
}
