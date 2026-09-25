package app

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// WorkspaceStore keeps the named groupings of connections, tabs and saved
// queries that a person works in (FR-15.9).
//
// The name is unfortunate next to [Workspace], which is the live
// connections, but both words are already in use: this one is the user's
// word for a piece of work, and that one is the application's word for
// what it is connected to.
type WorkspaceStore interface {
	PutWorkspace(ctx context.Context, w localdb.Workspace) error
	Workspaces(ctx context.Context) ([]localdb.Workspace, error)
	DeleteWorkspace(ctx context.Context, id string) error
}
