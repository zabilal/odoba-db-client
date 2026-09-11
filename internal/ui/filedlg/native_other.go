//go:build !(darwin && cgo) && !windows && !((linux && !android) || freebsd || openbsd || netbsd)

package filedlg

import "fyne.io/fyne/v2"

// show is Fyne's dialog, on a platform with none of its own to ask.
func show(w fyne.Window, o Options, save bool, done Done) { fallback(w, o, save, done) }
