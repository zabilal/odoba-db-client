package source

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/model"
)

// Introspector enumerates a source's objects.
//
// Enumeration is lazy and level-by-level: Children is called as the user
// expands the tree, never recursively up front. A schema with a thousand
// objects must not cost a thousand round trips before the first node paints
// (FR-2.1, NFR-P2).
type Introspector interface {
	// Root returns the top-level nodes for this connection.
	Root(ctx context.Context) ([]model.Node, error)

	// Children returns the direct children of a node.
	Children(ctx context.Context, ref model.ObjectRef) ([]model.Node, error)

	// Describe loads an object's full structure — the detail the structure
	// tab, DDL generator and diff engine need, which is more than the tree
	// shows. The concrete type depends on the object kind: *model.Table for
	// KindTable, *model.Topic for KindTopic, and so on.
	Describe(ctx context.Context, ref model.ObjectRef) (any, error)

	// Badge loads a node's count or size annotation (FR-2.5).
	//
	// Separate from Children because it is the expensive part: it is fetched
	// after the tree has painted, per visible node, and is always cancellable.
	// Sources without an affordable count return false.
	Badge(ctx context.Context, ref model.ObjectRef) (model.Badge, bool, error)
}

// Snapshotter is an optional Introspector refinement for sources that can
// produce a whole schema in one pass.
//
// Schema comparison (FR-7) needs the complete structure, and doing that
// through per-object Describe calls is unacceptably slow. Sources that can
// bulk-load a schema implement this; internal/diff falls back to Describe when
// they cannot.
type Snapshotter interface {
	// Snapshot loads a complete database structure for comparison.
	Snapshot(ctx context.Context, database string) (*model.Database, error)
}

// Searcher is an optional Introspector refinement for full-text search across
// object definitions (FR-2.7) — finding a column by name, or a string inside a
// stored procedure body.
type Searcher interface {
	SearchObjects(ctx context.Context, query string, limit int) ([]model.Node, error)
}
