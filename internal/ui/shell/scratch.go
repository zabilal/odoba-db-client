package shell

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// autosaveDelay is how long after an edit a query tab's text is kept, at the
// latest (NFR-R2). Writes are throttled, not debounced: text typed without a
// pause is still kept every autosaveDelay, not only once the typing stops.
const autosaveDelay = time.Second

// scratchTimeout bounds one write of a scratch buffer.
const scratchTimeout = 5 * time.Second

// scratchWriter keeps unsaved query text in the local database, off the UI
// goroutine. The latest word on a buffer replaces any not yet written, and
// one goroutine writes them in turn, so a buffer forgotten after a save is
// never written back by an earlier, slower write.
type scratchWriter struct {
	store  app.ScratchStore
	failed func(error) // called off the UI goroutine

	mu      sync.Mutex
	pending map[string]*localdb.Scratch // nil forgets the buffer
	order   []string
	closed  bool
	wake    chan struct{}
	done    chan struct{}
}

func newScratchWriter(store app.ScratchStore, failed func(error)) *scratchWriter {
	w := &scratchWriter{store: store, failed: failed, pending: map[string]*localdb.Scratch{},
		wake: make(chan struct{}, 1), done: make(chan struct{})}
	go w.run()
	return w
}

func (w *scratchWriter) put(sc localdb.Scratch) { w.queue(sc.ID, &sc) }
func (w *scratchWriter) forget(id string)       { w.queue(id, nil) }

func (w *scratchWriter) queue(id string, sc *localdb.Scratch) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if _, ok := w.pending[id]; !ok {
		w.order = append(w.order, id)
	}
	w.pending[id] = sc
	w.signal()
}

func (w *scratchWriter) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *scratchWriter) run() {
	defer close(w.done)
	for {
		w.mu.Lock()
		if len(w.order) == 0 {
			closed := w.closed
			w.mu.Unlock()
			if closed {
				return
			}
			<-w.wake
			continue
		}
		id := w.order[0]
		w.order = w.order[1:]
		sc := w.pending[id]
		delete(w.pending, id)
		w.mu.Unlock()
		w.write(id, sc)
	}
}

func (w *scratchWriter) write(id string, sc *localdb.Scratch) {
	ctx, cancel := context.WithTimeout(context.Background(), scratchTimeout)
	defer cancel()
	var err error
	if sc == nil {
		err = w.store.DeleteScratch(ctx, id)
	} else {
		err = w.store.PutScratch(ctx, *sc)
	}
	if err != nil {
		w.failed(err)
	}
}

// close writes what is pending, waiting at most wait, and takes nothing more.
// It reports whether everything was written in time.
func (w *scratchWriter) close(wait time.Duration) bool {
	w.mu.Lock()
	w.closed = true
	w.signal()
	w.mu.Unlock()
	select {
	case <-w.done:
		return true
	case <-time.After(wait):
		return false
	}
}

// edited keeps a query tab's text within the autosave delay of an edit.
func (s *Shell) edited(t *tab) {
	q := t.query
	if s.scratch == nil || q.keeping {
		return
	}
	q.keeping = true
	time.AfterFunc(s.autosave, func() {
		s.d.Run(func() {
			q.keeping = false
			if t.ctx.Err() == nil {
				s.keep(t)
			}
		})
	})
}

// keep writes a query tab's text to the scratch store while it has edits
// saved nowhere else, and forgets it once it has none.
func (s *Shell) keep(t *tab) {
	q := t.query
	if s.scratch == nil || q == nil {
		return
	}
	text := q.editor.Document().Text()
	if !q.dirty || strings.TrimSpace(text) == "" {
		s.scratch.forget(q.scratchID)
		return
	}
	s.scratch.put(localdb.Scratch{ID: q.scratchID, ConnectionID: t.connID, SavedID: q.saved.ID,
		Body: text, Opened: q.opened})
}

// forgetScratch drops a query tab's text from the scratch store: its tab was
// closed on purpose.
func (s *Shell) forgetScratch(t *tab) {
	if s.scratch != nil && t.query != nil {
		s.scratch.forget(t.query.scratchID)
	}
}

// autosaveFailed says, once a session, that unsaved text is not being kept.
func (s *Shell) autosaveFailed(err error) {
	if s.autosaveWarned {
		return
	}
	s.autosaveWarned = true
	s.showError(fmt.Errorf("unsaved query text could not be kept safe; save it to keep it: %w", err))
}

// reopenScratches reopens the query tabs a crash or a quit left with unsaved
// text, each on its connection and still marked unsaved. It reads off the UI
// goroutine and opens the tabs on it.
func (s *Shell) reopenScratches() {
	ctx, cancel := context.WithTimeout(s.ctx, scratchTimeout)
	defer cancel()
	list, err := s.d.Scratch.Scratches(ctx)
	saved := map[string]localdb.SavedQuery{}
	if len(list) > 0 && s.d.Saved != nil {
		all, serr := s.d.Saved.SavedQueries(ctx)
		for _, sq := range all {
			saved[sq.ID] = sq
		}
		err = errors.Join(err, serr)
	}
	s.d.Run(func() {
		if s.ctx.Err() != nil {
			return
		}
		for _, sc := range list {
			s.reopenScratch(sc, saved[sc.SavedID])
		}
		if err != nil {
			s.showError(fmt.Errorf("some unsaved query text could not be read back; it is still in the local database: %w", err))
		}
	})
}

// reopenScratch reopens one buffer. Text whose connection no longer exists
// opens in a tab that cannot run it, rather than being thrown away: it can
// still be read, copied, saved or closed.
func (s *Shell) reopenScratch(sc localdb.Scratch, sq localdb.SavedQuery) {
	t := s.newQueryTab(sc.ConnectionID)
	q := t.query
	q.scratchID, q.opened = sc.ID, sc.Opened
	q.editor.Document().SetText(sc.Body)
	q.editor.Refresh()
	if sq.ID != "" {
		q.saved, q.title = sq, sq.Name
	}
	q.dirty = true
	s.retitle(t)
	if _, ok := s.d.Conns.Get(sc.ConnectionID); ok {
		s.connectQuery(t)
		return
	}
	t.footer.SetText("")
	s.note(q, "The connection this query was written for no longer exists. "+
		"Its text is kept here: copy it, save it or close the tab.")
}

// newScratchID names a query tab's buffer: unique across restarts.
func newScratchID() string { return rand.Text() }
