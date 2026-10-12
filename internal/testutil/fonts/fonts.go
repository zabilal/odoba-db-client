// Package fonts lets a test suite give back the fonts its earlier tests used.
//
// Fyne shapes text with one shaper for the life of the process, and that
// shaper's buffer keeps a plan for every font face it has ever shaped, by the
// face, for good (harfbuzz.Buffer's planCache has nothing that takes an entry
// out). An application has a handful of faces and never notices. A test suite
// that makes a new Fyne application for each test parses the theme's fonts
// again each time — test.NewApp empties Fyne's own font cache, so they are
// new faces — and every one of them stays reachable from the shaper after
// its test has gone. The window's suite is eight hundred tests at about five
// megabytes each: four gigabytes held at the end, eight at the peak, and
// under the race detector on a hosted Windows runner more than the machine
// would commit, which is how it was found — ThreadSanitizer failing to
// allocate, nine minutes into a suite with nothing wrong in it.
//
// The shaper is not exported, so this reaches it by name. A Fyne that renames
// it fails the build of the tests that use this, at the link.
package fonts

import (
	_ "unsafe" // for go:linkname, and for nothing else

	"github.com/go-text/typesetting/shaping"
)

//go:linkname shaper fyne.io/fyne/v2/internal/painter.shaper
var shaper *shaping.HarfbuzzShaper

// Forget replaces Fyne's shaper with a new one, and with the old one go the
// fonts it was holding. Call it where a test makes its application, before
// anything draws: the shaper is not guarded, in Fyne or here, so this is for
// the goroutine that draws and for a moment when nothing else is.
func Forget() {
	shaper = &shaping.HarfbuzzShaper{}
}
