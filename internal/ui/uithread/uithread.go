// Package uithread gets work onto the UI goroutine (ARCH-6) in a way tests can
// control.
//
// Production code marshals with fyne.Do. But Fyne's headless test driver runs
// fyne.Do on the CALLER's goroutine ("tests all run on a single thread"), so in
// a test, a callback arriving from a timer or a background load touches widgets
// concurrently with the test itself. Fyne's text shaping is not goroutine-safe,
// and the race detector says so. Components therefore take a Runner: in
// production it is Fyne, and in tests it is a Queue the test flushes on its own
// goroutine.
package uithread

import (
	"sync"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
)

// Runner runs fn on the UI goroutine, eventually.
type Runner func(fn func())

// Fyne is the production Runner.
func Fyne(fn func()) { fyne.Do(fn) }

// FrameDelay is how long a coalesced refresh waits: about one frame at 60 fps,
// long enough to absorb a burst of changes into one redraw.
const FrameDelay = 16 * time.Millisecond

// Coalesce returns a trigger that arranges for fn to run once, through run,
// however many times the trigger fires before fn does. After a delay when
// delay > 0; immediately (still through run) when it is zero, which is what
// tests use with a Queue.
func Coalesce(run Runner, delay time.Duration, fn func()) (trigger func()) {
	var queued atomic.Bool
	fire := func() {
		queued.Store(false)
		fn()
	}
	return func() {
		if queued.Swap(true) {
			return
		}
		if delay <= 0 {
			run(fire)
			return
		}
		time.AfterFunc(delay, func() { run(fire) })
	}
}

// Queue is a Runner for tests: work is held until Flush runs it on the
// caller's goroutine.
type Queue struct {
	mu  sync.Mutex
	fns []func()
}

// Run holds fn. It satisfies Runner as a method value: q.Run.
func (q *Queue) Run(fn func()) {
	q.mu.Lock()
	q.fns = append(q.fns, fn)
	q.mu.Unlock()
}

// Len reports how much work is waiting.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.fns)
}

// Flush runs everything waiting, including work queued by the work it runs,
// and returns how many functions ran.
func (q *Queue) Flush() int {
	n := 0
	for {
		q.mu.Lock()
		fns := q.fns
		q.fns = nil
		q.mu.Unlock()
		if len(fns) == 0 {
			return n
		}
		for _, fn := range fns {
			fn()
			n++
		}
	}
}
