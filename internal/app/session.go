package app

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// SessionStore keeps the window as it was left, so that the next start puts
// it back (FR-15.2, NFR-R3).
type SessionStore interface {
	PutSession(ctx context.Context, s localdb.Session) error
	Session(ctx context.Context) (localdb.Session, bool, error)
}
