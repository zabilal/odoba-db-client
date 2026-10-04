// Package race reports whether the race detector is compiled in, so that
// timing-budget tests can decline to run under it.
//
// The Phase 0 gates (TestGateG0_*) assert time budgets. The race detector
// instruments every memory access and slows execution 5-20x, so a budget
// measured under it measures the detector, not the code. This was found when
// `go test -race ./...` — exactly what CI's build job runs — failed
// TestGateG0_1 with an 11.7ms table refresh against an 8ms budget; the same
// test measures 1.4ms without -race. The other gates passed only because the
// development machine had headroom a CI runner does not.
//
// The gates are not dropped from CI: they run in the performance job, without
// -race. Correctness checks that happen to live inside a gate — such as G0-4's
// agreement with a brute-force oracle — should keep running under the
// detector and skip only their timing assertions; use Enabled for that.
package race

import (
	"testing"
	"time"
)

// SkipTimingGate skips a test whose assertions are time budgets.
func SkipTimingGate(tb testing.TB) {
	tb.Helper()
	if Enabled {
		tb.Skip("timing gate skipped under -race: the detector slows execution " +
			"5-20x, so a budget measured under it measures the detector. " +
			"CI runs the gates in the performance job without -race.")
	}
}

// Slower scales a deadline for the detector.
//
// A deadline in a test is a statement that something should have happened by
// now — a plugin answered, a queue drained, a goroutine came back. It is not a
// budget: the budgets are the gates above, and they decline to run at all. Work
// under the detector takes 5-20x longer, so a deadline measured against the
// same wall clock stops meaning what it said and starts failing on the
// instrumentation. This moves it, and leaves it alone otherwise.
func Slower(d time.Duration) time.Duration {
	if Enabled {
		return 10 * d
	}
	return d
}
