package shell

import (
	"slices"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

func findCheckGroup(o fyne.CanvasObject) *widget.CheckGroup {
	var kids []fyne.CanvasObject
	switch v := o.(type) {
	case *widget.CheckGroup:
		return v
	case *fyne.Container:
		kids = v.Objects
	case fyne.Widget:
		kids = test.WidgetRenderer(v).Objects()
	}
	for _, k := range kids {
		if g := findCheckGroup(k); g != nil {
			return g
		}
	}
	return nil
}

// keyless opens the items table on a connection whose rows have no key.
func keyless(t *testing.T, readOnly bool) (*fixture, *tab) {
	t.Helper()
	fx := newFixture(t)
	c, err := fx.conns.Create(store.SavedConnection{Name: "nokey", Driver: "postgres", Host: "nokey", ReadOnly: readOnly}, nil)
	if err != nil {
		t.Fatal(err)
	}
	fx.s.OpenObject(c.ID, itemsNode)
	tb := fx.onlyTab(t)
	pump(t, fx.q, func() bool {
		if tb.model == nil {
			return false
		}
		_, ok := tb.model.Row(tb.ctx, 3)
		return ok
	})
	return fx, tb
}

func TestATableWithNoKeySaysWhyAndTakesAChosenOne(t *testing.T) {
	fx, tb := keyless(t, false)
	pump(t, fx.q, func() bool { return strings.Contains(tb.footer.Text, noKeyText) })
	if tb.ed.pending != nil || !fx.s.canChooseKey() {
		t.Fatal("a table with no key is not edited, and offers to choose one")
	}
	fx.s.run(cmdChooseKey)
	pick := findCheckGroup(fx.s.win.Canvas().Overlays().Top())
	if pick == nil || !slices.Equal(pick.Options, []string{"id", "name"}) {
		t.Fatalf("the key is chosen from the columns: %v", pick)
	}
	tapOnTop(t, fx, "Edit by These")
	if tb.ed.pending != nil || !strings.Contains(tb.footer.Text, "No key chosen") {
		t.Fatalf("with no column chosen nothing changes: %q", tb.footer.Text)
	}
	fx.s.run(cmdChooseKey)
	findCheckGroup(fx.s.win.Canvas().Overlays().Top()).SetSelected([]string{"name", "id"})
	tapOnTop(t, fx, "Edit by These")
	if tb.ed.pending == nil || !tb.grid.Table.ShowHeaderColumn || fx.s.canChooseKey() {
		t.Fatal("the rows are edited by the key chosen")
	}
	if !strings.Contains(tb.footer.Text, "Edited by id, name, the key chosen") {
		t.Errorf("footer %q: the key is in the table's order", tb.footer.Text)
	}
	row, _ := tb.model.Row(tb.ctx, 1)
	if err := tb.grid.OnEdit(row, 1, "renamed"); err != nil {
		t.Fatal(err)
	}
	cs := tb.ed.pending.Changeset(false)
	if cs.Identity.Kind != model.IdentityChosen || !slices.Equal(cs.Identity.Columns, []string{"id", "name"}) ||
		len(cs.Changes) != 1 || len(cs.Changes[0].Key) != 2 {
		t.Errorf("the change is written by the key chosen: %+v", cs)
	}
}

func TestOnlyATableWithNoKeyOffersOne(t *testing.T) {
	fx, _ := loadedItems(t)
	if fx.s.canChooseKey() || strings.Contains(fx.onlyTab(t).footer.Text, noKeyText) {
		t.Error("a table with a key has none to choose")
	}
	ro, tb := keyless(t, true)
	if ro.s.canChooseKey() || strings.Contains(tb.footer.Text, noKeyText) {
		t.Error("a read-only connection edits nothing, so it offers no key")
	}
}
