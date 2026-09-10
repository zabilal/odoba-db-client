package app

import (
	"errors"
	"sync"
	"testing"
	"time"
)

var fast = MonitorConfig{Interval: 15 * time.Millisecond, MinBackoff: 5 * time.Millisecond,
	MaxBackoff: 40 * time.Millisecond, PingTimeout: 50 * time.Millisecond}

func waitState(t *testing.T, l *Live, want State) Status {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st := l.Status(); st.State == want {
			return st
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("never reached %s; stuck at %s", want, l.Status().State)
	return Status{}
}

func TestLostConnectionIsVisibleAndRecovers(t *testing.T) {
	src := &fakeSource{}
	l := startLive("c1", src, fast)
	defer l.Close()

	var mu sync.Mutex
	var seen []State
	l.Subscribe(func(st Status) {
		mu.Lock()
		seen = append(seen, st.State)
		mu.Unlock()
	})

	src.failPing(errors.New("connection reset: password=hunter2"))
	st := waitState(t, l, StateDisconnected)
	if st.Attempt < 1 || st.NextTry.IsZero() {
		t.Errorf("disconnected status lacks retry information: %+v", st)
	}
	if st.Message() == "" || contains(st.Message(), "hunter2") {
		t.Errorf("message must explain, redacted: %q", st.Message())
	}

	// The outage start is kept across attempts.
	outage := st.Since
	time.Sleep(60 * time.Millisecond)
	if later := l.Status(); later.Attempt <= st.Attempt || !later.Since.Equal(outage) {
		t.Errorf("attempts should grow with Since fixed: %+v then %+v", st, later)
	}

	src.failPing(nil)
	waitState(t, l, StateConnected)

	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 2 || seen[0] != StateDisconnected || seen[len(seen)-1] != StateConnected {
		t.Errorf("subscriber saw %v", seen)
	}
}

func TestHealthyChecksNotifyNobody(t *testing.T) {
	src := &fakeSource{}
	l := startLive("c1", src, fast)
	defer l.Close()
	calls := 0
	var mu sync.Mutex
	l.Subscribe(func(Status) { mu.Lock(); calls++; mu.Unlock() })
	time.Sleep(80 * time.Millisecond) // several healthy checks
	mu.Lock()
	defer mu.Unlock()
	if src.pings.Load() < 2 {
		t.Fatal("the monitor is not checking")
	}
	if calls != 0 {
		t.Errorf("%d notifications for a connection that never changed", calls)
	}
}

func TestCheckRunsImmediately(t *testing.T) {
	src := &fakeSource{}
	l := startLive("c1", src, MonitorConfig{Interval: time.Hour})
	defer l.Close()
	src.failPing(errors.New("gone"))
	l.Check()
	waitState(t, l, StateDisconnected) // an hour-long interval would never get here
}

func TestCloseStopsMonitoringAndClosesSource(t *testing.T) {
	src := &fakeSource{}
	l := startLive("c1", src, fast)
	var last State
	var mu sync.Mutex
	l.Subscribe(func(st Status) { mu.Lock(); last = st.State; mu.Unlock() })

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if !src.closed.Load() {
		t.Error("source not closed")
	}
	n := src.pings.Load()
	time.Sleep(60 * time.Millisecond)
	if src.pings.Load() != n {
		t.Error("the monitor kept pinging after Close")
	}
	mu.Lock()
	if last != StateClosed {
		t.Errorf("subscribers not told of close: %v", last)
	}
	mu.Unlock()
	if err := l.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestBackoffGrowsAndIsCapped(t *testing.T) {
	cfg := MonitorConfig{MinBackoff: time.Second, MaxBackoff: 30 * time.Second}.withDefaults()
	prevMax := time.Duration(0)
	for attempt := 1; attempt <= 40; attempt++ {
		for i := 0; i < 20; i++ {
			d := backoff(cfg, attempt)
			if d <= 0 || d > cfg.MaxBackoff {
				t.Fatalf("attempt %d: %v outside (0, %v]", attempt, d, cfg.MaxBackoff)
			}
			if attempt == 1 && (d < 800*time.Millisecond || d > 1200*time.Millisecond) {
				t.Fatalf("first retry %v, want 1s ±20%%", d)
			}
			if d > prevMax {
				prevMax = d
			}
		}
	}
	if prevMax < 24*time.Second {
		t.Errorf("backoff never approached the ceiling: max %v", prevMax)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (s == sub || index(s, sub) >= 0)
}

func index(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
