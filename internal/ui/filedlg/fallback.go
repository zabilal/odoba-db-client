package filedlg

import (
	"fmt"
	"net/url"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
)

// fallback shows Fyne's own dialog, for a platform or a window with none of
// its own.
func fallback(w fyne.Window, o Options, save bool, done Done) {
	var d *dialog.FileDialog
	if save {
		d = dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) { done(closed(wc, err)) }, w)
		d.SetFileName(o.Name)
	} else {
		d = dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) { done(closed(rc, err)) }, w)
	}
	if ex := o.exts(); len(ex) > 0 {
		dotted := make([]string, len(ex))
		for i, e := range ex {
			dotted[i] = "." + e
		}
		d.SetFilter(storage.NewExtensionFileFilter(dotted))
	}
	if o.Directory != "" {
		if dir, err := storage.ListerForURI(storage.NewFileURI(o.Directory)); err == nil {
			d.SetLocation(dir)
		}
	}
	if o.Accept != "" {
		d.SetConfirmText(o.Accept)
	}
	if o.Message != "" {
		d.SetTitleText(o.Message)
	}
	d.Show()
}

// uriCloser is what Fyne's file dialogs hand back.
type uriCloser interface {
	URI() fyne.URI
	Close() error
}

// closed is the path of the file Fyne's dialog handed back, or "" for a
// cancel. The file is closed at once: the caller opens it as it needs to, as
// it does a path from a native dialog.
func closed(c uriCloser, err error) (string, error) {
	if err != nil || c == nil {
		return "", err
	}
	path := c.URI().Path()
	return path, c.Close()
}

// pathOf is the path a file: URI names, as the Linux portal returns them.
// url.Parse decodes it exactly: a + stays a +, where a query's unescaping
// would make it a space and name another file.
func pathOf(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return "", fmt.Errorf("the chosen file is not on this computer (%s)", uri)
	}
	return u.Path, nil
}

// winFilter is a Windows file dialog's filter: each kind's name and its
// patterns, each ended by a NUL, and the list by another. Empty for none.
func winFilter(fs []filter) string {
	var b strings.Builder
	for _, f := range fs {
		pats := strings.Join(f.patterns, ";")
		if pats == "*" {
			pats = "*.*" // "*" matches only names without a dot there
		}
		fmt.Fprintf(&b, "%s (%s)\x00%s\x00", f.name, pats, pats)
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String() + "\x00"
}
