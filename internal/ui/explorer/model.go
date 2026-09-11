// Package explorer implements the object tree (FR-2.1–FR-2.6).
//
// model.go is the tree's state, free of Fyne so it can be tested without a
// window. The widget binding asks it for children and draws what it answers.
package explorer

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/fuzzy"
)

// Item is one node.
type Item struct {
	// ID is unique in the tree and stable across refreshes, so expansion
	// state and selection survive a reload.
	ID          string
	Label       string
	HasChildren bool
	// Data is whatever the loader needs to expand the item: a connection ID,
	// an object reference.
	Data any
}

// Loader supplies children. The root's parent is the zero Item.
type Loader interface {
	Load(ctx context.Context, parent Item) ([]Item, error)
}

// LoaderFunc adapts a function to Loader.
type LoaderFunc func(ctx context.Context, parent Item) ([]Item, error)

func (f LoaderFunc) Load(ctx context.Context, parent Item) ([]Item, error) { return f(ctx, parent) }

// Status is a node's loading state.
type Status uint8

const (
	Unloaded Status = iota
	Loading
	Loaded
	Failed
)

// Placeholder IDs are derived from their parent's, so they are unique, and
// they carry a separator no real ID can contain.
const (
	loadingSuffix = "\x00loading"
	errorSuffix   = "\x00error"
)

// RootID is the tree's root.
const RootID = ""

type entry struct {
	item     Item
	parent   string
	children []string
	status   Status
	err      error
	gen      int
	cancel   context.CancelFunc
}

// Model is the tree's state. Safe for concurrent use.
//
// Children never blocks. The first time a branch is asked for its children it
// answers with a "Loading…" placeholder and fetches them in the background.
// When they arrive, OnChange fires and the tree asks again. A database that
// takes two seconds to list its schemas therefore never freezes the window
// (ARCH-6, UX principle 5).
type Model struct {
	loader  Loader
	timeout time.Duration

	mu      sync.Mutex
	entries map[string]*entry

	// OnChange is called when a node's children arrive, fail, or are reset.
	// It runs off the UI goroutine; the widget marshals it (ARCH-6).
	OnChange func(id string)
}

// NewModel returns a model over a loader. timeout bounds each load, so a hung
// server turns into an error placeholder rather than an endless "Loading…".
func NewModel(l Loader, timeout time.Duration) *Model {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	m := &Model{loader: l, timeout: timeout, entries: map[string]*entry{}}
	m.entries[RootID] = &entry{item: Item{ID: RootID, HasChildren: true}}
	return m
}

// Children returns a node's child IDs, starting a load if none has happened.
func (m *Model) Children(id string) []string {
	m.mu.Lock()
	e, ok := m.entries[id]
	if !ok || !e.item.HasChildren {
		m.mu.Unlock()
		return nil
	}
	switch e.status {
	case Loaded:
		out := append([]string(nil), e.children...)
		m.mu.Unlock()
		return out
	case Failed:
		m.mu.Unlock()
		return []string{id + errorSuffix}
	case Loading:
		m.mu.Unlock()
		return []string{id + loadingSuffix}
	}

	// Unloaded: start exactly one load, however many times the tree asks.
	e.status = Loading
	e.gen++
	gen := e.gen
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	e.cancel = cancel
	parent := e.item
	m.mu.Unlock()

	go m.load(ctx, cancel, id, gen, parent)
	return []string{id + loadingSuffix}
}

func (m *Model) load(ctx context.Context, cancel context.CancelFunc, id string, gen int, parent Item) {
	defer cancel()
	items, err := m.loader.Load(ctx, parent)

	m.mu.Lock()
	e, ok := m.entries[id]
	if !ok || e.gen != gen {
		// The node was refreshed or removed while this load was in flight.
		// Its answer describes a state that no longer exists; drop it rather
		// than let an old result overwrite a newer one.
		m.mu.Unlock()
		return
	}
	e.cancel = nil
	if err != nil {
		e.status, e.err, e.children = Failed, err, nil
	} else {
		// A load only ever starts from Unloaded, and Refresh has already
		// dropped every descendant by then, so each child is a fresh entry.
		// Expansion still survives a refresh: the tree widget remembers which
		// branches were open by ID and asks for them again, and they load
		// lazily. That works only because IDs here are the loader's own,
		// stable across reloads, never rewritten.
		ids := make([]string, 0, len(items))
		for _, it := range items {
			ids = append(ids, it.ID)
			m.entries[it.ID] = &entry{item: it, parent: id}
		}
		e.status, e.err, e.children = Loaded, nil, ids
	}
	m.mu.Unlock()
	m.notify(id)
}

// Item returns a node, including the synthetic placeholders.
func (m *Model) Item(id string) (Item, Status, error) {
	if _, ok := strip(id, loadingSuffix); ok {
		return Item{ID: id, Label: "Loading…"}, Loading, nil
	}
	if base, ok := strip(id, errorSuffix); ok {
		m.mu.Lock()
		defer m.mu.Unlock()
		if e, ok := m.entries[base]; ok && e.err != nil {
			return Item{ID: id, Label: e.err.Error()}, Failed, e.err
		}
		return Item{ID: id, Label: "failed"}, Failed, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok {
		return Item{}, Unloaded, nil
	}
	return e.item, e.status, e.err
}

// IsBranch reports whether a node can be expanded.
func (m *Model) IsBranch(id string) bool {
	if isPlaceholder(id) {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	return ok && e.item.HasChildren
}

// IsPlaceholder reports whether an ID is a loading or error row.
func IsPlaceholder(id string) bool { return isPlaceholder(id) }

// Refresh discards a node's children — and everything below them — and loads
// again on next ask. A load already in flight is cancelled, and its result
// ignored if it arrives anyway.
func (m *Model) Refresh(id string) {
	m.mu.Lock()
	e, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
	m.dropDescendants(id)
	e.children, e.err, e.status = nil, nil, Unloaded
	e.gen++
	m.mu.Unlock()
	m.notify(id)
}

// dropDescendants removes every entry below id. Called with mu held.
func (m *Model) dropDescendants(id string) {
	e := m.entries[id]
	for _, c := range e.children {
		if ce, ok := m.entries[c]; ok {
			if ce.cancel != nil {
				ce.cancel()
			}
			m.dropDescendants(c)
			delete(m.entries, c)
		}
	}
}

// Path returns the labels from the root down to a node, for the path-aware
// filter (FR-2.3) and for tab titles.
func (m *Model) Path(id string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pathLocked(id)
}

// pathLocked is Path, called with mu held.
func (m *Model) pathLocked(id string) []string {
	var out []string
	for cur := id; cur != RootID; {
		e, ok := m.entries[cur]
		if !ok {
			break
		}
		out = append([]string{e.item.Label}, out...)
		cur = e.parent
	}
	return out
}

// PathSep joins a node's path, for the filter to match and to show.
const PathSep = " / "

// Hit is a node whose path matches a filter.
type Hit struct {
	ID    string
	Path  []string // labels from the top of the tree down to the node
	Score int
}

// SearchResult is what a search found. Unopened counts the top-level
// branches, the connections, not yet loaded: nothing inside them was seen.
type SearchResult struct {
	Hits     []Hit
	Unopened int
}

// Search matches query against the full path of every node loaded so far
// (FR-2.3), so that "sales cust" finds the customers table in the sales
// database. The best matches come first, at most limit of them. It loads
// nothing: a filter that connected to every server to search it would be
// slow and would surprise, so the result says what it could not see.
func (m *Model) Search(query string, limit int) SearchResult {
	var res SearchResult
	if strings.TrimSpace(query) == "" {
		return res
	}
	m.mu.Lock()
	for id, e := range m.entries {
		if id == RootID {
			continue
		}
		if e.parent == RootID && e.item.HasChildren && e.status != Loaded {
			res.Unopened++
		}
		path := m.pathLocked(id)
		if score, _, ok := fuzzy.Match(query, strings.Join(path, PathSep)); ok {
			res.Hits = append(res.Hits, Hit{ID: id, Path: path, Score: score})
		}
	}
	m.mu.Unlock()
	sort.Slice(res.Hits, func(a, b int) bool {
		ha, hb := res.Hits[a], res.Hits[b]
		if ha.Score != hb.Score {
			return ha.Score > hb.Score
		}
		if len(ha.Path) != len(hb.Path) {
			return len(ha.Path) < len(hb.Path)
		}
		return ha.ID < hb.ID
	})
	if limit > 0 && len(res.Hits) > limit {
		res.Hits = res.Hits[:limit]
	}
	return res
}

func (m *Model) notify(id string) {
	if m.OnChange != nil {
		m.OnChange(id)
	}
}

func strip(id, suffix string) (string, bool) {
	if len(id) >= len(suffix) && id[len(id)-len(suffix):] == suffix {
		return id[:len(id)-len(suffix)], true
	}
	return "", false
}

func isPlaceholder(id string) bool {
	_, a := strip(id, loadingSuffix)
	_, b := strip(id, errorSuffix)
	return a || b
}
