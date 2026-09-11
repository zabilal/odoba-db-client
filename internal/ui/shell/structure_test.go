package shell

import (
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// labelTexts gathers the text of every label in a canvas object.
func labelTexts(o fyne.CanvasObject) []string {
	var out []string
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case nil:
			return
		case *widget.Label:
			out = append(out, v.Text)
			return
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
			return
		}
		if w, ok := o.(fyne.Widget); ok {
			for _, c := range test.WidgetRenderer(w).Objects() {
				walk(c)
			}
		}
	}
	walk(o)
	return out
}

func showsAll(t *tab, want ...string) bool {
	got := strings.Join(labelTexts(t.item.Content), "\n")
	for _, w := range want {
		if !strings.Contains(got, w) {
			return false
		}
	}
	return true
}

func TestOpenStructureShowsTheTablesShape(t *testing.T) {
	fx := newFixture(t)
	selectItems(t, fx)
	if c, _ := fx.s.reg.Get(cmdStructure); c.Shortcut.Label("darwin") != "⌥⌘O" {
		t.Errorf("Open Structure is on %q, want ⌥⌘O", c.Shortcut.Label("darwin"))
	}
	fx.s.run(cmdStructure)
	tb := fx.onlyTab(t)
	if tb.item.Text != "Structure: items" || !tb.structure {
		t.Fatalf("opened %q", tb.item.Text)
	}
	pump(t, fx.q, func() bool {
		return showsAll(tb, "Columns", "id", "integer", "identity", "name", "text", "'x'", "items_pkey", "items_name", "About 41 rows")
	})
	fx.s.run(cmdStructure)
	if len(fx.s.open) != 1 {
		t.Error("an open structure tab should come forward, not open twice")
	}
}

func TestStructureNeedsAnObjectWithRows(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	if !fx.s.menuItems[cmdStructure].Disabled {
		t.Error("a database is not described here; Open Structure should be disabled")
	}
}

func TestADescribeThatPanicsIsSaid(t *testing.T) {
	fx := newFixture(t)
	c := fx.create(t, "db1", nil)
	tb := fx.s.OpenStructure(c.ID, model.Node{Ref: model.NewRef(model.KindTable, "main", "boom"), Label: "boom", Browsable: true})
	pump(t, fx.q, func() bool { return showsAll(tb, "could not read the structure") })
	if !strings.Contains(fx.s.errors.text, "describing fell over") {
		t.Errorf("the error band should say the driver fell over: %q", fx.s.errors.text)
	}
}

func TestAStructureTabComesBack(t *testing.T) {
	fx := newFixture(t)
	fx.s.autosave = time.Hour // so that only quitting saves it
	c := fx.create(t, "db1", nil)
	fx.s.OpenStructure(c.ID, itemsNode)
	fx.s.shutdown()
	s := fx.relaunch(t)
	r := tabNamed(s, "Structure: items")
	if r == nil || !r.structure {
		t.Fatalf("tabs %v; the structure tab should come back as one", tabLabels(s))
	}
	pump(t, fx.q, func() bool { return showsAll(r, "items_pkey") })
}
