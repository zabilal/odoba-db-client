// Package app holds the use cases: what the application does, independent of
// how it is drawn. The UI calls into it; it calls into store and source.
//
// It must not import Fyne. ARCH-7: UI components take their data through
// interfaces and types defined here, so each use case can be tested without a
// window.
//
// Callbacks from this package (connection status changes, for example) arrive
// on background goroutines. The UI must marshal them onto the Fyne main
// goroutine itself (ARCH-6).
package app
