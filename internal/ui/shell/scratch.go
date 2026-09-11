package shell

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/store/localdb"
)

// autosaveDelay is how long after an edit a query tab's text is kept, at the
// latest (NFR-R2). Writes are throttled, not debounced: text typed without a
// pause is still kept every autosaveDelay, not only once the typing stops.
// The session is saved on the same rhythm.
const autosaveDelay = time.Second

// storeTimeout bounds one write to the local database.
const storeTimeout = 5 * time.Second

// writer keeps state in the local database off the UI goroutine: unsaved
// query text and the session. The latest word on a key replaces any not yet
// written, and one goroutine writes them in turn, so a buffer forgotten
// after a save is never written back by an earlier, slower write.
type writer struct {
	failed func(error) // called off the UI goroutine

	mu      sync.Mutex
	pending map[string]func(context.Context) error
	order   []string
	closed  bool
	wake    chan struct{}
	done    chan struct{}
}

func newWriter(failed func(error)) *writer {
	w := &writer{failed: failed, pending: map[string]func(context.Context) error{},
		wake: make(chan struct{}, 1), done: make(chan struct{})}
	go w.run()
	return w
}

// queue makes op the next write under key, replacing any not yet written.
// Its error is said to the user as it stands, so it says what was not kept.
func (w *writer) queue(key string, op func(context.Context) error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if _, ok := w.pending[key]; !ok {
		w.order = append(w.order, key)
	}
	w.pending[key] = op
	w.signal()
}

func (w *writer) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *writer) run() {
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
		key := w.order[0]
		w.order = w.order[1:]
		op := w.pending[key]
		delete(w.pending, key)
		w.mu.Unlock()
		w.write(op)
	}
}

func (w *writer) write(op func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
	defer cancel()
	if err := op(ctx); err != nil {
		w.failed(err)
	}
}

// close writes what is pending, waiting at most wait, and takes nothing more.
// It reports whether everything was written in time.
func (w *writer) close(wait time.Duration) bool {
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
	if s.writer == nil || s.d.Scratch == nil || q.keeping {
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
	if s.writer == nil || s.d.Scratch == nil || q == nil {
		return
	}
	text := q.editor.Document().Text()
	if !q.dirty || strings.TrimSpace(text) == "" {
		s.dropScratch(q.scratchID)
		return
	}
	s.putScratch(localdb.Scratch{ID: q.scratchID, ConnectionID: t.connID, SavedID: q.saved.ID,
		Body: text, Opened: q.opened})
}

// forgetScratch drops a query tab's text from the scratch store: its tab was
// closed on purpose.
func (s *Shell) forgetScratch(t *tab) {
	if s.writer != nil && s.d.Scratch != nil && t.query != nil {
		s.dropScratch(t.query.scratchID)
	}
}

func (s *Shell) putScratch(sc localdb.Scratch) {
	st := s.d.Scratch
	s.writer.queue("scratch/"+sc.ID, func(ctx context.Context) error {
		if err := st.PutScratch(ctx, sc); err != nil {
			return fmt.Errorf("unsaved query text could not be kept safe; save it to keep it: %w", err)
		}
		return nil
	})
}

func (s *Shell) dropScratch(id string) {
	st := s.d.Scratch
	s.writer.queue("scratch/"+id, func(ctx context.Context) error {
		if err := st.DeleteScratch(ctx, id); err != nil {
			return fmt.Errorf("query text that needs no keeping could not be cleared, and may reopen at the next start: %w", err)
		}
		return nil
	})
}

// autosaveFailed says, once a session, that something is not being kept.
func (s *Shell) autosaveFailed(err error) {
	if s.autosaveWarned {
		return
	}
	s.autosaveWarned = true
	s.showError(err)
}

// reopenScratch reopens one buffer. Text whose connection no longer exists
// opens in a tab that cannot run it, rather than being thrown away: it can
// still be read, copied, saved or closed.
func (s *Shell) reopenScratch(sc localdb.Scratch, sq localdb.SavedQuery) *tab {
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
		return t
	}
	t.footer.SetText("")
	s.note(q, "The connection this query was written for no longer exists. "+
		"Its text is kept here: copy it, save it or close the tab.")
	return t
}

// newScratchID names a query tab's buffer: unique across restarts.
func newScratchID() string { return rand.Text() }
