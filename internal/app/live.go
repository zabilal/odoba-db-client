package app

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/ikigai-db/ikigai-db/internal/redact"
	"github.com/ikigai-db/ikigai-db/internal/source"
)

// State is a live connection's health (FR-1.15).
type State uint8

const (
	StateConnected State = iota
	// StateDisconnected: the server stopped answering. The monitor keeps
	// retrying with backoff; Status says when the next attempt is.
	StateDisconnected
	StateClosed
)

func (s State) String() string {
	return [...]string{"connected", "disconnected", "closed"}[s]
}

// Status is a live connection's state and why.
type Status struct {
	State State
	// Since is when this state began. For a disconnection it is the moment the
	// outage started, not the latest failed attempt.
	Since   time.Time
	Err     error
	Attempt int
	NextTry time.Time
}

// Message is the redacted, displayable form of the failure, or "".
func (s Status) Message() string {
	if s.Err == nil {
		return ""
	}
	return redact.Error(s.Err)
}

// MonitorConfig tunes health checking. Zero fields take the defaults.
type MonitorConfig struct {
	Interval    time.Duration // between checks while healthy; default 30s
	MinBackoff  time.Duration // first retry after a failure; default 1s
	MaxBackoff  time.Duration // retry ceiling; default 30s
	PingTimeout time.Duration // default 5s
}

func (m MonitorConfig) withDefaults() MonitorConfig {
	if m.Interval <= 0 {
		m.Interval = 30 * time.Second
	}
	if m.MinBackoff <= 0 {
		m.MinBackoff = time.Second
	}
	if m.MaxBackoff <= 0 {
		m.MaxBackoff = 30 * time.Second
	}
	if m.PingTimeout <= 0 {
		m.PingTimeout = 5 * time.Second
	}
	return m
}

// Live is an open connection whose health is watched.
//
// Reconnecting is not something this type does: the drivers' pools redial on
// the next use by themselves. What it adds is visibility. A lost connection
// becomes an explicit state the UI shows, and is re-checked with backoff until
// it recovers, never a silent failure the user discovers by running a query
// (FR-1.15).
type Live struct {
	ID     string
	Source source.Source

	cfg MonitorConfig

	mu     sync.Mutex
	status Status
	subs   map[int]func(Status)
	nextID int

	kick      chan struct{}
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func startLive(id string, src source.Source, cfg MonitorConfig) *Live {
	l := &Live{
		ID: id, Source: src, cfg: cfg.withDefaults(),
		status: Status{State: StateConnected, Since: time.Now()},
		subs:   map[int]func(Status){},
		kick:   make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go l.run()
	return l
}

// Status returns the current status.
func (l *Live) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.status
}

// Subscribe registers a callback for status changes. It runs on the monitor's
// goroutine; the UI must marshal it onto its own (ARCH-6).
func (l *Live) Subscribe(fn func(Status)) (unsubscribe func()) {
	l.mu.Lock()
	id := l.nextID
	l.nextID++
	l.subs[id] = fn
	l.mu.Unlock()
	return func() {
		l.mu.Lock()
		delete(l.subs, id)
		l.mu.Unlock()
	}
}

// Check asks for an immediate health check. The UI calls it when an operation
// fails in a way that suggests the connection dropped, so the state updates
// now rather than at the next scheduled check.
func (l *Live) Check() {
	select {
	case l.kick <- struct{}{}:
	default: // a check is already pending
	}
}

func (l *Live) run() {
	defer close(l.done)
	timer := time.NewTimer(l.cfg.Interval)
	defer timer.Stop()
	attempt := 0
	var outage time.Time

	for {
		select {
		case <-l.stop:
			return
		case <-l.kick:
		case <-timer.C:
		}

		ctx, cancel := context.WithTimeout(context.Background(), l.cfg.PingTimeout)
		err := l.Source.Ping(ctx)
		cancel()

		select {
		case <-l.stop:
			return // closed while the ping was in flight: report nothing
		default:
		}

		now := time.Now()
		var next time.Duration
		if err == nil {
			attempt, outage = 0, time.Time{}
			l.transition(Status{State: StateConnected, Since: now})
			next = l.cfg.Interval
		} else {
			attempt++
			if outage.IsZero() {
				outage = now
			}
			next = backoff(l.cfg, attempt)
			l.transition(Status{State: StateDisconnected, Since: outage, Err: err,
				Attempt: attempt, NextTry: now.Add(next)})
		}
		timer.Reset(next) // Go 1.23+ timers: Reset never delivers a stale tick
	}
}

// transition updates status, notifying subscribers of real changes: a change
// of state, or a new failed attempt. A healthy check that changes nothing
// notifies nobody.
func (l *Live) transition(st Status) {
	l.mu.Lock()
	prev := l.status
	if prev.State == st.State && prev.Attempt == st.Attempt {
		l.mu.Unlock()
		return
	}
	if st.State == StateConnected && prev.State == StateConnected {
		st.Since = prev.Since
	}
	l.status = st
	subs := make([]func(Status), 0, len(l.subs))
	for _, fn := range l.subs {
		subs = append(subs, fn)
	}
	l.mu.Unlock()
	for _, fn := range subs {
		fn(st)
	}
}

// backoff doubles from MinBackoff up to MaxBackoff, with ±20% jitter so that
// a server that restarts is not hit by every client in lockstep.
func backoff(cfg MonitorConfig, attempt int) time.Duration {
	d := cfg.MaxBackoff
	if attempt < 31 {
		if b := cfg.MinBackoff << (attempt - 1); b > 0 && b < cfg.MaxBackoff {
			d = b
		}
	}
	d = time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
	if d > cfg.MaxBackoff {
		d = cfg.MaxBackoff
	}
	return d
}

// Close stops monitoring and closes the connection. Safe to call more than
// once.
func (l *Live) Close() error {
	l.closeOnce.Do(func() {
		close(l.stop)
		<-l.done
		l.closeErr = l.Source.Close()
		l.mu.Lock()
		l.status = Status{State: StateClosed, Since: time.Now()}
		subs := make([]func(Status), 0, len(l.subs))
		for _, fn := range l.subs {
			subs = append(subs, fn)
		}
		st := l.status
		l.mu.Unlock()
		for _, fn := range subs {
			fn(st)
		}
	})
	return l.closeErr
}
