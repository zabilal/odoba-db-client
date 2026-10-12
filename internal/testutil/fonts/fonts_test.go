package fonts

import (
	"runtime"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

func heap() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}

// keptPerApp is how many megabytes each of n applications that measured some
// text left behind.
func keptPerApp(n int, before func()) float64 {
	one := func() {
		before()
		test.NewApp()
		fyne.MeasureText("text", 14, fyne.TextStyle{})
	}
	one()
	start := heap()
	for range n {
		one()
	}
	return float64(heap()-start) / float64(n) / 1e6
}

// The thing this package exists for, and the thing it is for: left alone, each
// application's fonts stay; forgotten, they go. The first half is here so that
// a Fyne which stops keeping them shows up as this package having nothing left
// to do, rather than as a test that passes whatever Forget does.
func TestForgettingGivesBackWhatEachApplicationParsed(t *testing.T) {
	t.Cleanup(func() { test.NewApp(); Forget() })
	if kept := keptPerApp(20, func() {}); kept < 0.5 {
		t.Errorf("left alone, each application kept %.2f MB: Fyne no longer holds its fonts, and this package can go", kept)
	}
	if kept := keptPerApp(20, Forget); kept > 0.1 {
		t.Errorf("forgotten, each application still kept %.2f MB", kept)
	}
}

// And text is still measured afterwards, which a shaper put back wrongly would
// not manage.
func TestTextIsStillMeasuredAfterwards(t *testing.T) {
	test.NewApp()
	before := fyne.MeasureText("text", 14, fyne.TextStyle{})
	Forget()
	if after := fyne.MeasureText("text", 14, fyne.TextStyle{}); after != before || after.Width == 0 {
		t.Errorf("measured %v before and %v after", before, after)
	}
}
