//go:build cgo

package filedlg

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestAWindowWithNoNSWindowGetsFynesDialog(t *testing.T) {
	test.NewTempApp(t)
	o := Options{Message: "Export “items” as CSV", Name: "items.csv", Directory: t.TempDir()}
	for _, save := range []bool{true, false} {
		w := test.NewTempWindow(t, widget.NewLabel(""))
		w.Resize(fyne.NewSize(900, 700))
		if save {
			Native{}.Save(w, o, func(string, error) {})
		} else {
			Native{}.Open(w, o, func(string, error) {})
		}
		top := w.Canvas().Overlays().Top()
		if top == nil {
			t.Fatalf("save=%v: a window with no NSWindow for a sheet should get Fyne's dialog", save)
		}
		if slices.Contains(texts(top), o.Name) != save {
			t.Errorf("save=%v: the dialog shows %q; only Save suggests a name", save, texts(top))
		}
	}
}
