// Package filedlg asks where to save a file, or which file to open, in the
// platform's own dialog (FR-15.5, ADR-0023): a sheet on the window on macOS,
// the common file dialog on Windows, and the desktop's file chooser portal on
// Linux. Where there is none to be had, Fyne's own dialog stands in, so a
// choice can always be made.
package filedlg

import (
	"strings"

	"fyne.io/fyne/v2"
)

// Options describe a file dialog. Every field may be left empty.
type Options struct {
	// Message says what the file is for, as "Export “items” as CSV". It is
	// the title on Windows and Linux, and the line above a macOS sheet.
	Message string
	// Name is the file name a save dialog suggests.
	Name string
	// Extensions, without their dots, are the kinds of file offered; none
	// offers any file. A save dialog gives a name the first when it has none.
	Extensions []string
	// Kind names the files the extensions describe, as "CSV".
	Kind string
	// Directory is where the dialog starts. Empty leaves it to the platform,
	// which starts where the last one ended.
	Directory string
	// Accept is the confirming button's label, where the platform has one to
	// set.
	Accept string
}

// Done receives the path chosen, or "" when the dialog was cancelled. It is
// called on the UI goroutine.
type Done func(path string, err error)

// Chooser shows file dialogs over a window.
type Chooser interface {
	Save(w fyne.Window, o Options, done Done)
	Open(w fyne.Window, o Options, done Done)
}

// Native is the platform's own dialog, and Fyne's where there is none.
type Native struct{}

// Save asks where to save a file. The dialog asks before replacing one.
func (Native) Save(w fyne.Window, o Options, done Done) { show(w, o, true, done) }

// Open asks which existing file to open.
func (Native) Open(w fyne.Window, o Options, done Done) { show(w, o, false, done) }

// exts is o's extensions without dots or spaces, the empty ones dropped.
func (o Options) exts() []string {
	var out []string
	for _, e := range o.Extensions {
		if e = strings.TrimLeft(strings.TrimSpace(e), "."); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// filter is one kind of file a dialog offers: a name and its patterns.
type filter struct {
	name     string
	patterns []string
}

// filters is the kinds of file o offers: its own, then any file. None when
// it names no extensions, since a dialog offers any file already.
func (o Options) filters() []filter {
	ex := o.exts()
	if len(ex) == 0 {
		return nil
	}
	pats := make([]string, len(ex))
	for i, e := range ex {
		pats[i] = "*." + e
	}
	kind := o.Kind
	if kind == "" {
		kind = strings.ToUpper(strings.Join(ex, ", "))
	}
	return []filter{{kind + " Files", pats}, {"All Files", []string{"*"}}}
}
