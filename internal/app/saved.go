package app

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// SavedQueryStore keeps scripts the user chose to keep (FR-5.9). *localdb.DB
// is one. Saved queries are stored verbatim, never redacted: the user saved
// them on purpose.
type SavedQueryStore interface {
	SaveQuery(ctx context.Context, q localdb.SavedQuery) (localdb.SavedQuery, error)
	SavedQueries(ctx context.Context) ([]localdb.SavedQuery, error)
	DeleteQuery(ctx context.Context, id string) error
}
