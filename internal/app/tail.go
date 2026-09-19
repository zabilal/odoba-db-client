package app

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/panics"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// Following a log into a window that cannot grow (FR-13.6, NFR-P10).
//
// Every other read here is paged: the grid asks for a window and the source
// answers it. A tail is the opposite — the source answers when it feels like
// it, for as long as somebody is writing — so it is held rather than fetched,
// and what holds it must have a size that does not depend on how busy the
// topic is.

// tailKeeps is how many records a tail holds when nobody says otherwise.
// Enough to scroll back through what just happened, few enough that a topic
// nobody is watching cannot cost anything much.
const tailKeeps = 2000

// Tail is a following read, kept in a window of fixed size.
//
// Records arrive on a goroutine of its own and are added to a ring; when the
// ring is full the oldest goes, because a tail is about what is happening now
// rather than what happened first. What went is counted, so that nothing is
// lost quietly.
type Tail struct {
	rs   model.RowStream
	cols []model.ColumnDef

	mu      sync.Mutex
	cond    *sync.Cond
	ring    []model.Row
	first   int // where the oldest record is
	n       int
	dropped int64
	paused  bool
	err     error
	closed  bool

	cancel context.CancelFunc
	done   chan struct{}
}

// NewTail follows an object, keeping the last keeps records.
//
// The read is a following one whatever the caller asked for: a tail that
// stopped at the end of the log would not be one.
func NewTail(ctx context.Context, src source.Source, ref model.ObjectRef, opt source.BrowseOptions, keeps int) (_ *Tail, err error) {
	defer panics.Recover(&err, "following the records")

	if keeps <= 0 {
		keeps = tailKeeps
	}
	opt.Follow = true
	ctx, cancel := context.WithCancel(ctx)
	rs, err := src.Browse(ctx, ref, opt)
	if err != nil {
		cancel()
		return nil, err
	}
	t := &Tail{rs: rs, cols: rs.Columns(), ring: make([]model.Row, keeps),
		cancel: cancel, done: make(chan struct{})}
	t.cond = sync.NewCond(&t.mu)
	go t.follow(ctx)
	return t, nil
}

// Columns is the shape of every row a tail holds.
func (t *Tail) Columns() []model.ColumnDef { return t.cols }

// follow reads until the stream fails, the reader gives up, or it is closed.
func (t *Tail) follow(ctx context.Context) {
	defer close(t.done)
	for {
		t.mu.Lock()
		for t.paused && !t.closed {
			// Paused reads nothing at all, so nothing is dropped while it is:
			// the source stops being asked, and what it has waits for us.
			t.cond.Wait()
		}
		stop := t.closed
		t.mu.Unlock()
		if stop {
			return
		}

		row, err := t.rs.Next(ctx)
		if err != nil {
			t.mu.Lock()
			// Being closed or given up on is how a tail ends, not a fault in
			// it. Anything else is worth telling somebody about.
			if !t.closed && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
				t.err = err
			}
			t.mu.Unlock()
			return
		}
		t.keep(row)
	}
}

// keep puts a record in the window, dropping the oldest if it is full.
func (t *Tail) keep(row model.Row) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.n == len(t.ring) {
		t.ring[t.first] = row
		t.first = (t.first + 1) % len(t.ring)
		t.dropped++
		return
	}
	t.ring[(t.first+t.n)%len(t.ring)] = row
	t.n++
}

// Rows are the records held now, oldest first. It is a copy: what is held
// changes under the reader, and a grid drawing a row should not.
func (t *Tail) Rows() []model.Row {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]model.Row, 0, t.n)
	for i := 0; i < t.n; i++ {
		out = append(out, t.ring[(t.first+i)%len(t.ring)])
	}
	return out
}

// Dropped is how many records fell out of the window: what a person is not
// seeing, which is worth saying rather than hiding.
func (t *Tail) Dropped() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dropped
}

// Pause stops reading. Nothing is dropped while paused — the records wait
// where they are, and resuming carries on from them.
func (t *Tail) Pause() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paused = true
}

// Resume starts reading again, from where it stopped.
func (t *Tail) Resume() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.paused = false
	t.cond.Broadcast()
}

// Paused reports whether records are being read.
func (t *Tail) Paused() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.paused
}

// Err is what went wrong, if anything did. A tail that was closed or given up
// on has nothing to report: that is how one ends.
func (t *Tail) Err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// Close stops following and lets the stream go. It is safe to call twice.
func (t *Tail) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	// Wake it if it is paused, and give up on the read if it is waiting.
	t.cond.Broadcast()
	t.mu.Unlock()

	t.cancel()
	<-t.done
	return t.rs.Close()
}
