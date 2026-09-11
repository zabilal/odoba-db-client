package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/ui/grid"
)

// entryIn is the first text entry in o.
func entryIn(o fyne.CanvasObject) *widget.Entry {
	switch v := o.(type) {
	case *widget.Entry:
		return v
	case *widget.PopUp:
		return entryIn(v.Content)
	case *fyne.Container:
		for _, c := range v.Objects {
			if e := entryIn(c); e != nil {
				return e
			}
		}
	case fyne.Widget:
		for _, c := range test.WidgetRenderer(v).Objects() {
			if e := entryIn(c); e != nil {
				return e
			}
		}
	}
	return nil
}

// filled waits for a fill's own word in the footer.
func filled(t *testing.T, fx *fixture, tb *tab) {
	t.Helper()
	pump(t, fx.q, func() bool {
		return strings.Contains(tb.footer.Text, "Set ") && !strings.Contains(tb.footer.Text, "Setting…")
	})
}

// setValueTo runs Set Value…, gives it text, and waits for it.
func setValueTo(t *testing.T, fx *fixture, tb *tab, text string) {
	t.Helper()
	fx.s.run(cmdSetValue)
	entry := entryIn(fx.s.win.Canvas().Overlays().Top())
	if entry == nil {
		t.Fatal("Set Value asks for a value")
	}
	entry.SetText(text)
	tapOnTop(t, fx, "Set")
	filled(t, fx, tb)
}

func TestSetValueWritesOneValueIntoEverySelectedCell(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 1, Col: 1}, grid.CellID{Row: 2, Col: 1})
	tb.grid.ToggleCell(grid.CellID{Row: 4, Col: 0})
	fx.s.run(cmdSetValue)
	tapOnTop(t, fx, "Cancel")
	if tb.ed.pending.Len() != 0 || strings.Contains(tb.footer.Text, "Setting") {
		t.Fatalf("Cancel sets nothing: %q", tb.footer.Text)
	}
	setValueTo(t, fx, tb, "7")
	for _, r := range []int{1, 2} {
		if v, _ := valueAt(tb, r, 1); v != "7" {
			t.Errorf("row %d's name: %v", r, v)
		}
	}
	if v, _ := valueAt(tb, 4, 0); v != int64(7) || !strings.Contains(tb.footer.Text, "Set 3 cells") {
		t.Errorf("row 4's id, as its column reads it: %v; footer %q", v, tb.footer.Text)
	}
	setValueTo(t, fx, tb, "x")
	if !strings.Contains(tb.footer.Text, "Set 2 of 3 cells; the first not set: id: ") {
		t.Errorf("footer %q", tb.footer.Text)
	}
}

func TestSetValueAndSetToNULLReachRowsNeverLoaded(t *testing.T) {
	fx, tb := loadedItems(t)
	tb.grid.Select(grid.CellID{Row: 0, Col: 1}, grid.CellID{Row: 0, Col: 1})
	tb.grid.SelectColumn()
	setValueTo(t, fx, tb, "z")
	if tb.ed.pending.Len() != fakeRows || !strings.Contains(tb.footer.Text, "Set 250 cells") {
		t.Errorf("a whole column is set: %d changes; footer %q", tb.ed.pending.Len(), tb.footer.Text)
	}
	tb.ed.pending.RevertAll()
	fx.s.run(cmdSetNull)
	filled(t, fx, tb)
	if tb.ed.pending.Len() != fakeRows {
		t.Errorf("Set to NULL reaches them too: %d changes", tb.ed.pending.Len())
	}
}

func TestAFillStopsAtItsLimit(t *testing.T) {
	fx, tb := loadedItems(t)
	copyLimit = 10
	t.Cleanup(func() { copyLimit = 100_000 })
	tb.grid.Select(grid.CellID{Row: 0, Col: 1}, grid.CellID{Row: 0, Col: 1})
	tb.grid.SelectColumn()
	setValueTo(t, fx, tb, "z")
	if !strings.Contains(tb.footer.Text, "Set Value takes up to 10 rows, and the selection has more.") || tb.ed.pending.Len() != 0 {
		t.Errorf("more rows than a fill takes sets none: footer %q", tb.footer.Text)
	}
}
