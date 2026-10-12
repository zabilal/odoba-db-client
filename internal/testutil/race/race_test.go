package race

import (
	"testing"
	"time"
)

// What the detector changes about a deadline.
//
// The same test compiles under both builds and asserts what that build should
// say, so neither half can quietly stop being true: without -race a deadline is
// what it was written as, and with it a deadline is longer because the work
// underneath it is.
func TestADeadlineMovesUnderTheDetector(t *testing.T) {
	const wrote = 5 * time.Second
	got := Slower(wrote)
	if Enabled {
		if got != 10*wrote {
			t.Errorf("under the detector five seconds reads as %v", got)
		}
		return
	}
	if got != wrote {
		t.Errorf("without the detector five seconds reads as %v", got)
	}
}

// Nothing is a deadline of nothing either way: a helper that invented one would
// make a test that waits for nothing wait for a while.
func TestNoDeadlineIsStillNone(t *testing.T) {
	if got := Slower(0); got != 0 {
		t.Errorf("no deadline reads as %v", got)
	}
}
