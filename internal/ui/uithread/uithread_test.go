package uithread

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestQueueRunsOnFlushInOrderIncludingNestedWork(t *testing.T) {
	var q Queue
	var got []int
	q.Run(func() { got = append(got, 1); q.Run(func() { got = append(got, 3) }) })
	q.Run(func() { got = append(got, 2) })
	if len(got) != 0 {
		t.Fatal("work ran before Flush")
	}
	if n := q.Flush(); n != 3 || len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Errorf("flush ran %d: %v", n, got)
	}
}

func TestCoalesceAbsorbsABurst(t *testing.T) {
	var q Queue
	runs := 0
	trigger := Coalesce(q.Run, 0, func() { runs++ })
	for i := 0; i < 50; i++ {
		trigger()
	}
	if q.Len() != 1 {
		t.Fatalf("50 triggers queued %d runs, want 1", q.Len())
	}
	q.Flush()
	trigger()
	if q.Len() != 1 {
		t.Error("a trigger after the run must queue another")
	}
	q.Flush()
	if runs != 2 {
		t.Errorf("fn ran %d times, want 2", runs)
	}
}

func TestCoalesceWithDelay(t *testing.T) {
	var runs atomic.Int64
	trigger := Coalesce(func(fn func()) { fn() }, 20*time.Millisecond, func() { runs.Add(1) })
	for i := 0; i < 10; i++ {
		trigger()
	}
	time.Sleep(60 * time.Millisecond)
	if runs.Load() != 1 {
		t.Errorf("delayed coalesce ran %d times, want 1", runs.Load())
	}
}
