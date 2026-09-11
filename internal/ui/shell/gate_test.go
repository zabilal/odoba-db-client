package shell

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
	"github.com/ikigai-db/ikigai-db/internal/testutil/race"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
	"github.com/ikigai-db/ikigai-db/internal/ui/uithread"
)

// TestGateP1ColdStart times the part of NFR-P1 the application does
// itself: building the window from its stores, the last session read back
// included. Starting the process and the graphics are not in it, so the
// budget is a quarter of NFR-P1's 800ms, leaving them the rest.
func TestGateP1ColdStart(t *testing.T) {
	if testing.Short() {
		t.Skip("gate skipped in -short")
	}
	race.SkipTimingGate(t)
	a := test.NewTempApp(t)
	dir := t.TempDir()
	db, err := localdb.Open(context.Background(), filepath.Join(dir, "ikigai.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sf, _, err := store.OpenSettings(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	conns := app.NewConnections(sf, app.NewVault(secrets.NewMemory(), nil), nil)
	ws := app.NewWorkspace(conns, app.MonitorConfig{Interval: time.Hour})

	start := time.Now()
	s := New(a, Deps{Conns: conns, WS: ws, Settings: sf, History: db, Saved: db, Scratch: db, Session: db,
		Run: (&uithread.Queue{}).Run, GOOS: "darwin"})
	elapsed := time.Since(start)
	t.Cleanup(s.shutdown)

	t.Logf("P1: the window built in %v", elapsed.Round(time.Microsecond))
	const budget = 200 * time.Millisecond
	if elapsed > budget {
		t.Errorf("P1: building the window took %v, over %v", elapsed, budget)
	}
}

// TestGateP6IdleHeap measures what of NFR-P6 a test can see: the Go heap
// that five connections add, each with a table open, more than the
// requirement's three result sets. It holds the difference, read after a
// collection before and after they open, because the heap of a test binary
// also holds what every earlier test left behind: read whole, it measured
// 254 MB after the rest of the package had run and 35 MB alone. The
// process's other memory, the graphics and fonts, is not visible to it, so
// the budget is a third of NFR-P6's 300MB.
func TestGateP6IdleHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("gate skipped in -short")
	}
	race.SkipTimingGate(t)
	fx := newFixture(t)
	heap := func() uint64 {
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.HeapInuse
	}
	before := heap()
	var tabs []*tab
	for i := range 5 {
		c := fx.create(t, fmt.Sprintf("db%d", i), nil)
		fx.s.OpenObject(c.ID, itemsNode)
		tabs = append(tabs, fx.s.tabFor(view.NodeID(c.ID, itemsNode.Ref)))
	}
	pump(t, fx.q, func() bool {
		for _, tb := range tabs {
			if tb == nil || tb.browse == nil {
				return false
			}
		}
		return true
	})
	after := heap()
	var added uint64
	if after > before {
		added = after - before
	}
	t.Logf("P6: five connections with a table each add %d MB of heap (%d MB in all)", added>>20, after>>20)
	const budget = 100 << 20
	if added > budget {
		t.Errorf("P6: five connections with a table each add %d MB of heap, over %d MB", added>>20, budget>>20)
	}
}
