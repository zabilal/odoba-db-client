package filedlg

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestFiltersNameTheKindThenAnyFile(t *testing.T) {
	got := Options{Extensions: []string{".csv", " ", "tsv"}, Kind: "Text"}.filters()
	want := []filter{{"Text Files", []string{"*.csv", "*.tsv"}}, {"All Files", []string{"*"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filters = %v, want %v", got, want)
	}
	if got := (Options{Extensions: []string{"json"}}).filters(); got[0].name != "JSON Files" {
		t.Errorf("with no kind, the extensions name it: %v", got)
	}
	if got := (Options{Extensions: []string{" ", "."}}).filters(); got != nil {
		t.Errorf("no extension offers any file already, with no filter: %v", got)
	}
}

func TestAWindowsFilterIsNamesAndPatternsEndedByNULs(t *testing.T) {
	got := winFilter(Options{Extensions: []string{"csv"}, Kind: "CSV"}.filters())
	want := "CSV Files (*.csv)\x00*.csv\x00All Files (*.*)\x00*.*\x00\x00"
	if got != want {
		t.Errorf("winFilter = %q, want %q", got, want)
	}
	if got := winFilter(nil); got != "" {
		t.Errorf("no filter is %q, want empty", got)
	}
}

func TestAPortalsURIKeepsAPlusInTheName(t *testing.T) {
	for uri, want := range map[string]string{
		"file:///home/ann/c++.sql":             "/home/ann/c++.sql",
		"file:///home/ann/a%20b%2Bc.csv":       "/home/ann/a b+c.csv",
		"file:///home/ann/%E2%80%9Cq%E2%80%9D": "/home/ann/“q”",
	} {
		if got, err := pathOf(uri); err != nil || got != want {
			t.Errorf("pathOf(%q) = %q, %v; want %q", uri, got, err, want)
		}
	}
	for _, uri := range []string{"https://example.com/a.csv", "file://", "%zz"} {
		if got, err := pathOf(uri); err == nil {
			t.Errorf("pathOf(%q) = %q; a URI naming no local file is an error", uri, got)
		}
	}
}

// handed is what Fyne's dialog hands back for a chosen file.
type handed struct {
	uri    fyne.URI
	closed bool
}

func (h *handed) URI() fyne.URI { return h.uri }
func (h *handed) Close() error  { h.closed = true; return nil }

func TestFynesAnswerIsAPathWithTheFileClosed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.csv")
	h := &handed{uri: storage.NewFileURI(p)}
	if got, err := closed(h, nil); got != p || err != nil || !h.closed {
		t.Errorf("closed = %q, %v (file closed: %v); want %q, closed", got, err, h.closed, p)
	}
	var none fyne.URIWriteCloser // a cancel hands back a nil writer
	if got, err := closed(none, nil); got != "" || err != nil {
		t.Errorf("a cancel = %q, %v; want no path", got, err)
	}
	boom := errors.New("boom")
	if got, err := closed(h, boom); got != "" || !errors.Is(err, boom) {
		t.Errorf("an error = %q, %v; want it passed on, with no path", got, err)
	}
}

func TestFynesDialogStandsInWithTheOptions(t *testing.T) {
	test.NewTempApp(t)
	dir := t.TempDir()
	for _, f := range []string{"kept.csv", "other.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, save := range []bool{true, false} {
		w := test.NewTempWindow(t, widget.NewLabel(""))
		w.Resize(fyne.NewSize(900, 700))
		o := Options{Message: "Export “items” as CSV", Name: "items.csv", Extensions: []string{"csv"},
			Directory: dir, Accept: "Export"}
		fallback(w, o, save, func(string, error) {})
		top := w.Canvas().Overlays().Top()
		if top == nil {
			t.Fatalf("save=%v: no dialog shown", save)
		}
		got := texts(top)
		for _, want := range []string{o.Message, o.Accept} {
			if !slices.Contains(got, want) {
				t.Errorf("save=%v: the dialog shows %q, without %q", save, got, want)
			}
		}
		if slices.Contains(got, o.Name) != save {
			t.Errorf("save=%v: the dialog shows %q; only a save dialog suggests %q", save, got, o.Name)
		}
		// Fyne names a file without its extension: it starts in the folder
		// asked for, showing the CSV file and not the other.
		named := func(p string) bool {
			return slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, p) })
		}
		if !named("kept") || named("other") {
			t.Errorf("save=%v: the dialog lists %q; want kept.csv from %s, and not other.txt", save, got, dir)
		}
	}
}

// texts is every label's, button's and entry's text under o.
func texts(o fyne.CanvasObject) []string {
	switch v := o.(type) {
	case *widget.Label:
		return []string{v.Text}
	case *widget.Button:
		return []string{v.Text}
	case *widget.Entry:
		return []string{v.Text}
	case *fyne.Container:
		var out []string
		for _, c := range v.Objects {
			out = append(out, texts(c)...)
		}
		return out
	case fyne.Widget:
		var out []string
		for _, c := range test.WidgetRenderer(v).Objects() {
			out = append(out, texts(c)...)
		}
		return out
	}
	return nil
}
