package app

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// Workspace holds the connections open in the window: at most one Live per
// saved connection, opened on first use and shared by everything that needs
// it — the explorer, every grid tab and every editor tab.
//
// Sharing is the point. Without it, expanding a connection in the tree and
// opening one of its tables in a tab would dial the server twice, and a
// ten-tab session would hold ten pools to the same database.
type Workspace struct {
	conns *Connections
	mon   MonitorConfig

	mu      sync.Mutex
	live    map[string]*Live
	opening map[string]*pending
	unsub   map[string]func()

	onStatus func(id string, st Status)
}

type pending struct {
	done chan struct{}
	live *Live
	err  error
}

// NewWorkspace returns an empty workspace.
func NewWorkspace(conns *Connections, mon MonitorConfig) *Workspace {
	return &Workspace{conns: conns, mon: mon,
		live: map[string]*Live{}, opening: map[string]*pending{}, unsub: map[string]func(){}}
}

// OnStatus sets the callback for connection status changes. It runs off the UI
// goroutine; the UI marshals it (ARCH-6).
func (w *Workspace) OnStatus(fn func(id string, st Status)) {
	w.mu.Lock()
	w.onStatus = fn
	w.mu.Unlock()
}

// Connect returns the open connection for a saved connection, opening it if
// needed.
//
// Concurrent calls for one connection share a single dial. A caller that
// gives up waiting — its context cancelled — returns without cancelling the
// dial the others are waiting on. A failed open is not remembered, so the next
// Connect tries again: a server that was briefly down should not stay
// "failed" until restart.
func (w *Workspace) Connect(ctx context.Context, id string) (*Live, error) {
	w.mu.Lock()
	if l, ok := w.live[id]; ok {
		w.mu.Unlock()
		return l, nil
	}
	if p, ok := w.opening[id]; ok {
		w.mu.Unlock()
		select {
		case <-p.done:
			return p.live, p.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	p := &pending{done: make(chan struct{})}
	w.opening[id] = p
	w.mu.Unlock()

	// The dial runs under its own context: if the first caller gives up,
	// others may still be waiting for this connection.
	go w.dial(id, p)

	select {
	case <-p.done:
		return p.live, p.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (w *Workspace) dial(id string, p *pending) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	l, err := w.conns.Open(ctx, id, w.mon)

	w.mu.Lock()
	delete(w.opening, id)
	if err == nil {
		w.live[id] = l
		w.unsub[id] = l.Subscribe(func(st Status) { w.forward(id, st) })
	}
	p.live, p.err = l, err
	w.mu.Unlock()
	close(p.done)
}

func (w *Workspace) forward(id string, st Status) {
	w.mu.Lock()
	fn := w.onStatus
	w.mu.Unlock()
	if fn != nil {
		fn(id, st)
	}
}

// Get returns an already-open connection without opening one.
func (w *Workspace) Get(id string) (*Live, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	l, ok := w.live[id]
	return l, ok
}

// OpenIDs lists the connections currently open.
func (w *Workspace) OpenIDs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.live))
	for id := range w.live {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Disconnect closes one connection. Everything using it — tree nodes, grid
// tabs — must be told by the caller; the next Connect opens a fresh one.
func (w *Workspace) Disconnect(id string) error {
	w.mu.Lock()
	l, ok := w.live[id]
	delete(w.live, id)
	unsub := w.unsub[id]
	delete(w.unsub, id)
	w.mu.Unlock()
	if !ok {
		return nil
	}
	err := l.Close() // subscribers hear StateClosed before unsubscribing
	if unsub != nil {
		unsub()
	}
	return err
}

// CloseAll closes every connection, for shutdown.
func (w *Workspace) CloseAll() error {
	var errs []error
	for _, id := range w.OpenIDs() {
		errs = append(errs, w.Disconnect(id))
	}
	return errors.Join(errs...)
}
