package app

import (
	"context"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// Keeping a diagram as somebody arranged it (FR-8.2).
//
// A diagram is laid out afresh every time it is opened, and what somebody
// moved is put back on top. The alternative — keeping every position — means
// a diagram never lays itself out again: a table added to the schema lands
// wherever nothing else is, and the rest stay as they were read weeks ago.
//
// Only where it is kept is here. Putting an arrangement back on a graph, and
// reading one off it, are the window's: they take the canvas's own types, and
// this layer may not name them (ARCH-1). They live in internal/ui/shell beside
// the diagram that uses them.

// LayoutStore keeps where the boxes were put.
type LayoutStore interface {
	Layout(ctx context.Context, connID, diagram string) (localdb.DiagramLayout, bool, error)
	PutLayout(ctx context.Context, connID, diagram string, l localdb.DiagramLayout) error
	ForgetLayout(ctx context.Context, connID, diagram string) error
}
