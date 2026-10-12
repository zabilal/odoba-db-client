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
//
// It builds the window twice and holds the second, as the P3 gate does, because
// the first one in a process pays for the toolkit as well: fonts read and
// measured, a theme resolved, icons rasterised, each once for the process and
// none of it this code. On a machine with nothing else to do the difference is
// small — 130ms against 230ms here — and on a hosted runner with neighbours it
// is not: the first build measured 523ms against a 200ms budget on a commit
// that changed nothing about it, and the commit before, which measures the same
// distribution, passed. A gate that fails on somebody else's load teaches
// people to ignore the gate.
//
// What the first build costs is reported, not held. NFR-P1's whole number needs
// a process actually starting and a display actually drawing, which is GATE
// G0-1 on an attended machine.
//
// Holding the second build against the same 200ms would be a looser gate than
// the one it replaces, because the number it holds is now four times smaller:
// a doubling of this code's cost would pass. So the budget here is half the
// requirement's share, which still leaves a slower machine twice what this one
// needs while failing a window that has become twice the work to build.
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
	t.Cleanup(func() { _ = db.Close() })
	sf, _, err := store.OpenSettings(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	conns := app.NewConnections(sf, app.NewVault(secrets.NewMemory(), nil), nil)
	ws := app.NewWorkspace(conns, app.MonitorConfig{Interval: time.Hour})

	build := func() time.Duration {
		start := time.Now()
		s := New(a, Deps{Conns: conns, WS: ws, Settings: sf, History: db, Saved: db, Scratch: db, Session: db,
			Run: (&uithread.Queue{}).Run, GOOS: "darwin"})
		elapsed := time.Since(start)
		t.Cleanup(s.shutdown)
		return elapsed
	}
	cold := build()
	elapsed := build()

	t.Logf("P1: the process's first window built in %v (the toolkit's share, not held here)",
		cold.Round(time.Microsecond))
	t.Logf("P1: the window built in %v", elapsed.Round(time.Microsecond))
	const share = 200 * time.Millisecond // NFR-P1's 800ms, less the process and the graphics
	const budget = share / 2
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

// NFR-P9: stopping takes effect in under 200ms, whatever the server is
// doing.
//
// This is the window's half of the budget, and it is the half the person
// waiting can see: Stop cancels and lets go, rather than waiting for the
// source to notice. A window that waited would be a Stop button that does
// nothing for as long as the thing it is stopping.
const p9Budget = 200 * time.Millisecond

func TestGateP9StoppingAQueryIsPrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("gate skipped in -short")
	}
	race.SkipTimingGate(t)

	fx := newFixture(t)
	_, q := openQuery(t, fx, "")
	q.editor.Document().SetText("slow 1000000;")
	fx.s.run(cmdQueryRun)
	pump(t, fx.q, func() bool { return len(q.sets) == 1 })

	start := time.Now()
	fx.s.run(cmdQueryStop)
	took := time.Since(start)
	if took > p9Budget {
		t.Errorf("stopping took %v; NFR-P9 asks for %v", took, p9Budget)
	}
	t.Logf("stopped in %v", took)
}
