package app

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// ViewStore keeps a table's rows as somebody arranged them, under a name
// (FR-3.16). A store of its own rather than a corner of the saved queries,
// because a view is of an object and a query is of nothing.
type ViewStore interface {
	PutView(ctx context.Context, v localdb.View) error
	Views(ctx context.Context, connID, refKind string, refPath []string) ([]localdb.View, error)
	DeleteView(ctx context.Context, v localdb.View) error
}
