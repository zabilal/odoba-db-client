package view

import (
	"context"
	"fmt"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/testutil/race"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// TestGateP2ThousandObjectTree times the part of NFR-P2 the explorer does
// itself: loading a schema of 1 000 objects and drawing the tree. The
// objects come from memory, so connecting and the server's own listing are
// not in it; the budget is a quarter of NFR-P2's 2s, leaving them the rest.
func TestGateP2ThousandObjectTree(t *testing.T) {
	if testing.Short() {
		t.Skip("gate skipped in -short")
	}
	race.SkipTimingGate(t)
	newApp(t)

	const n = 1000
	l := explorer.LoaderFunc(func(_ context.Context, parent explorer.Item) ([]explorer.Item, error) {
		switch parent.ID {
		case explorer.RootID:
			return []explorer.Item{{ID: "schema", Label: "public", HasChildren: true}}, nil
		case "schema":
			out := make([]explorer.Item, n)
			for i := range out {
				out[i] = explorer.Item{ID: fmt.Sprintf("t%04d", i), Label: fmt.Sprintf("table_%04d", i)}
			}
			return out, nil
		}
		return nil, nil
	})
	q := &uithread.Queue{}
	e := New(l, q.Run, 0)
	w := test.NewTempWindow(t, e.View())
	w.Resize(fyne.NewSize(320, 960))

	start := time.Now()
	e.Model.Children(explorer.RootID)
	waitReal(t, e.Model, explorer.RootID)
	e.Tree.OpenBranch("schema")
	e.Model.Children("schema")
	kids := waitReal(t, e.Model, "schema")
	q.Flush()
	e.Tree.Refresh()
	elapsed := time.Since(start)

	if len(kids) != n {
		t.Fatalf("the schema listed %d objects, want %d", len(kids), n)
	}
	t.Logf("P2: a %d-object schema loaded and drawn in %v", n, elapsed.Round(time.Microsecond))
	const budget = 500 * time.Millisecond
	if elapsed > budget {
		t.Errorf("P2: a %d-object tree took %v, over %v", n, elapsed, budget)
	}
}
