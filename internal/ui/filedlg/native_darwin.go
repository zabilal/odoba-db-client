//go:build cgo

package filedlg

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework UniformTypeIdentifiers
#include <stdint.h>
#include <stdlib.h>

void filedlgShow(uintptr_t h, uintptr_t win, int save, const char *message, const char *name,
	const char *exts, const char *dir, const char *accept);
*/
import "C"

import (
	"runtime/cgo"
	"strings"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
)

// show opens an NSSavePanel or NSOpenPanel as a sheet on the window, as a
// Mac app's save and open panels are (native_darwin.m).
func show(w fyne.Window, o Options, save bool, done Done) {
	var win uintptr
	if nw, ok := w.(driver.NativeWindow); ok {
		nw.RunNative(func(ctx any) {
			if c, ok := ctx.(driver.MacWindowContext); ok {
				win = c.NSWindow
			}
		})
	}
	if win == 0 {
		fallback(w, o, save, done) // a window with no NSWindow, as a test's
		return
	}
	// The sheet answers later, on the main thread, inside the event loop:
	// fyne.Do queues the answer for the loop rather than run it there.
	h := cgo.NewHandle(func(path string) { fyne.Do(func() { done(path, nil) }) })
	strs := []*C.char{C.CString(o.Message), C.CString(o.Name), C.CString(strings.Join(o.exts(), "\n")),
		C.CString(o.Directory), C.CString(o.Accept)}
	defer func() {
		for _, s := range strs {
			C.free(unsafe.Pointer(s))
		}
	}()
	flag := C.int(0)
	if save {
		flag = 1
	}
	C.filedlgShow(C.uintptr_t(h), C.uintptr_t(win), flag, strs[0], strs[1], strs[2], strs[3], strs[4])
}

//export filedlgDone
func filedlgDone(h C.uintptr_t, path *C.char) {
	handle := cgo.Handle(h)
	done := handle.Value().(func(string))
	handle.Delete()
	p := ""
	if path != nil {
		p = C.GoString(path)
	}
	done(p)
}
