package explorer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tree is a loader over a fixed shape: each node "p" has children "p/0".."p/2"
// down to depth 3, with controllable latency and failures.
type tree struct {
	calls   atomic.Int64
	delay   time.Duration
	failFor sync.Map // parent ID -> error
	label   atomic.Value
}

func (tr *tree) Load(ctx context.Context, parent Item) ([]Item, error) {
	tr.calls.Add(1)
	if tr.delay > 0 {
		select {
		case <-time.After(tr.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if v, ok := tr.failFor.Load(parent.ID); ok {
		return nil, v.(error)
	}
	depth := 0
	for _, c := range parent.ID {
		if c == '/' {
			depth++
		}
	}
	suffix := ""
	if l, ok := tr.label.Load().(string); ok {
		suffix = l
	}
	var out []Item
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("%s/%d", parent.ID, i)
		out = append(out, Item{ID: id, Label: fmt.Sprintf("n%d%s", i, suffix), HasChildren: depth < 3})
	}
	return out, nil
}

func waitLoaded(t *testing.T, m *Model, id string) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		kids := m.Children(id)
		if len(kids) > 0 && !IsPlaceholder(kids[0]) {
			return kids
		}
		if _, st, _ := m.Item(id); st == Failed {
			return kids
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%q never loaded", id)
	return nil
}

func TestChildrenNeverBlock(t *testing.T) {
	tr := &tree{delay: 300 * time.Millisecond}
	m := NewModel(tr, 0)

	start := time.Now()
	kids := m.Children(RootID)
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Fatalf("Children blocked for %v on a slow loader", elapsed)
	}
	if len(kids) != 1 || !IsPlaceholder(kids[0]) {
		t.Fatalf("want a loading placeholder, got %q", kids)
	}
	if it, st, _ := m.Item(kids[0]); st != Loading || it.Label != "Loading…" {
		t.Errorf("placeholder = %+v %v", it, st)
	}
	if m.IsBranch(kids[0]) {
		t.Error("a placeholder must not be expandable")
	}
	if got := waitLoaded(t, m, RootID); len(got) != 3 {
		t.Errorf("loaded %q", got)
	}
}

func TestOneLoadPerExpansionHoweverOftenAsked(t *testing.T) {
	tr := &tree{delay: 50 * time.Millisecond}
	m := NewModel(tr, 0)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); m.Children(RootID) }()
	}
	wg.Wait()
	waitLoaded(t, m, RootID)
	if n := tr.calls.Load(); n != 1 {
		t.Errorf("loader called %d times, want 1", n)
	}
}

func TestOnChangeFiresWhenChildrenArrive(t *testing.T) {
	m := NewModel(&tree{}, 0)
	changed := make(chan string, 4)
	m.OnChange = func(id string) { changed <- id }
	m.Children(RootID)
	select {
	case id := <-changed:
		if id != RootID {
			t.Errorf("changed %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnChange never fired")
	}
}

func TestFailureBecomesAVisibleErrorRow(t *testing.T) {
	tr := &tree{}
	tr.failFor.Store(RootID, errors.New("permission denied for database sales"))
	m := NewModel(tr, 0)
	m.Children(RootID)
	kids := waitLoaded(t, m, RootID)
	if len(kids) != 1 {
		t.Fatalf("want one error row, got %q", kids)
	}
	it, st, err := m.Item(kids[0])
	if st != Failed || err == nil || it.Label != "permission denied for database sales" {
		t.Errorf("error row = %+v %v %v", it, st, err)
	}

	// Refresh retries.
	tr.failFor.Delete(RootID)
	m.Refresh(RootID)
	m.Children(RootID)
	if kids := waitLoaded(t, m, RootID); len(kids) != 3 {
		t.Errorf("retry after refresh: %q", kids)
	}
}

func TestSlowServerBecomesAnErrorNotEndlessLoading(t *testing.T) {
	m := NewModel(&tree{delay: time.Hour}, 30*time.Millisecond)
	m.Children(RootID)
	kids := waitLoaded(t, m, RootID)
	if _, st, err := m.Item(kids[0]); st != Failed || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("want a timeout error row, got %v %v", st, err)
	}
}

func TestRefreshDropsDescendantsAndStaleResults(t *testing.T) {
	tr := &tree{}
	m := NewModel(tr, 0)
	m.Children(RootID)
	kids := waitLoaded(t, m, RootID)
	m.Children(kids[0])
	grand := waitLoaded(t, m, kids[0])

	m.Refresh(RootID)
	if _, st, _ := m.Item(grand[0]); st != Unloaded {
		t.Error("a grandchild survived refreshing its grandparent")
	}

	// A load that finishes after a refresh must not overwrite the newer state.
	slow := &tree{delay: 80 * time.Millisecond}
	m2 := NewModel(slow, 0)
	m2.Children(RootID) // generation 1, slow
	slow.label.Store("-new")
	m2.Refresh(RootID) // cancels generation 1
	m2.Children(RootID)
	got := waitLoaded(t, m2, RootID)
	time.Sleep(120 * time.Millisecond) // let any straggler land
	if it, _, _ := m2.Item(got[0]); it.Label != "n0-new" {
		t.Errorf("a stale load overwrote the refreshed one: %q", it.Label)
	}
}

// TestRefreshPreservesLoaderIDs checks the one thing expansion-after-refresh
// depends on in the model: IDs pass through verbatim, so the tree widget's own
// record of open branches still names nodes that exist after a reload.
func TestRefreshPreservesLoaderIDs(t *testing.T) {
	m := NewModel(&tree{}, 0)
	m.Children(RootID)
	before := waitLoaded(t, m, RootID)
	m.Refresh(RootID)
	m.Children(RootID)
	after := waitLoaded(t, m, RootID)
	if len(before) != len(after) {
		t.Fatalf("child count changed across refresh: %q vs %q", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("ID %d changed across refresh: %q -> %q", i, before[i], after[i])
		}
	}
}

func TestPathGivesLabelsFromTheRoot(t *testing.T) {
	m := NewModel(&tree{}, 0)
	m.Children(RootID)
	kids := waitLoaded(t, m, RootID)
	m.Children(kids[0])
	waitLoaded(t, m, kids[0])
	if got := m.Path(kids[0] + "/1"); len(got) != 2 || got[0] != "n0" || got[1] != "n1" {
		t.Errorf("path = %q", got)
	}
}

func TestSearchMatchesThePathAndLoadsNothing(t *testing.T) {
	tr := &tree{}
	m := NewModel(tr, 0)
	m.Children(RootID)
	top := waitLoaded(t, m, RootID)
	m.Children(top[0])
	waitLoaded(t, m, top[0])
	calls := tr.calls.Load()
	// Every label is n0 to n2: only the path tells one n1 from another.
	res := m.Search("n0 / n1", 0)
	if len(res.Hits) == 0 || res.Hits[0].ID != top[0]+"/1" {
		t.Fatalf("hits %+v, want %s first", res.Hits, top[0]+"/1")
	}
	for _, h := range res.Hits {
		if len(h.Path) > 2 {
			t.Errorf("hit %v lies inside a branch that was never loaded", h.Path)
		}
	}
	if res.Unopened != 2 {
		t.Errorf("unopened %d, want the 2 top-level branches never loaded", res.Unopened)
	}
	if tr.calls.Load() != calls {
		t.Error("a search loaded something")
	}
	if got := m.Search("  ", 0); len(got.Hits) != 0 {
		t.Errorf("a blank filter matched %d", len(got.Hits))
	}
}
