package filedlg

import (
	"fmt"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver"
)

var (
	comdlg32         = syscall.NewLazyDLL("comdlg32.dll")
	procGetSaveName  = comdlg32.NewProc("GetSaveFileNameW")
	procGetOpenName  = comdlg32.NewProc("GetOpenFileNameW")
	procExtendedErr  = comdlg32.NewProc("CommDlgExtendedError")
	ole32            = syscall.NewLazyDLL("ole32.dll")
	procCoInitialize = ole32.NewProc("CoInitializeEx")
	procCoUninit     = ole32.NewProc("CoUninitialize")
)

// openFileName is Win32's OPENFILENAMEW. Go lays it out as C does.
type openFileName struct {
	structSize      uint32
	owner           uintptr
	instance        uintptr
	filter          *uint16
	customFilter    *uint16
	maxCustomFilter uint32
	filterIndex     uint32
	file            *uint16
	maxFile         uint32
	fileTitle       *uint16
	maxFileTitle    uint32
	initialDir      *uint16
	title           *uint16
	flags           uint32
	fileOffset      uint16
	fileExtension   uint16
	defExt          *uint16
	custData        uintptr
	hook            uintptr
	templateName    *uint16
	reserved        uintptr
	reserved2       uint32
	flagsEx         uint32
}

const (
	ofnOverwritePrompt = 0x2
	ofnHideReadOnly    = 0x4
	ofnNoChangeDir     = 0x8
	ofnPathMustExist   = 0x800
	ofnFileMustExist   = 0x1000
	ofnExplorer        = 0x80000

	coinitApartmentThreaded = 0x2
	coinitDisableOLE1DDE    = 0x4
)

// show opens the common file dialog, owned by the window, on a thread of
// its own: it runs its own message loop until it closes, and the window
// keeps drawing meanwhile. Without a hook or template it is the Explorer
// dialog of the Windows it runs on.
func show(w fyne.Window, o Options, save bool, done Done) {
	var owner uintptr
	if nw, ok := w.(driver.NativeWindow); ok {
		nw.RunNative(func(ctx any) {
			if c, ok := ctx.(driver.WindowsWindowContext); ok {
				owner = c.HWND
			}
		})
	}
	if owner == 0 {
		fallback(w, o, save, done)
		return
	}
	go func() {
		path, err := winDialog(owner, o, save)
		fyne.Do(func() { done(path, err) })
	}()
}

func winDialog(owner uintptr, o Options, save bool) (string, error) {
	// The dialog is a window of this thread, and the shell extensions it
	// hosts need COM set up on it.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, _ := procCoInitialize.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE); int32(r) >= 0 {
		defer procCoUninit.Call()
	}
	buf := make([]uint16, 32768)
	if name, err := syscall.UTF16FromString(o.Name); err == nil && len(name) < len(buf) {
		copy(buf, name)
	}
	ofn := openFileName{owner: owner, file: &buf[0], maxFile: uint32(len(buf)),
		flags: ofnExplorer | ofnPathMustExist | ofnNoChangeDir | ofnHideReadOnly}
	ofn.structSize = uint32(unsafe.Sizeof(ofn))
	if f := winFilter(o.filters()); f != "" {
		enc := utf16.Encode([]rune(f)) // the NULs inside are the format
		ofn.filter = &enc[0]
	}
	ofn.title = utf16Ptr(o.Message)
	ofn.initialDir = utf16Ptr(o.Directory)
	proc := procGetOpenName
	if save {
		proc = procGetSaveName
		ofn.flags |= ofnOverwritePrompt
		if ex := o.exts(); len(ex) > 0 {
			ofn.defExt = utf16Ptr(ex[0])
		}
	} else {
		ofn.flags |= ofnFileMustExist
	}
	ok, _, _ := proc.Call(uintptr(unsafe.Pointer(&ofn)))
	runtime.KeepAlive(buf)
	if ok == 0 {
		if code, _, _ := procExtendedErr.Call(); code != 0 {
			return "", fmt.Errorf("the file dialog failed (code %#x)", code)
		}
		return "", nil // cancelled
	}
	return syscall.UTF16ToString(buf), nil
}

// utf16Ptr is s for Windows, or nil for an empty or impossible one.
func utf16Ptr(s string) *uint16 {
	if s == "" {
		return nil
	}
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}
