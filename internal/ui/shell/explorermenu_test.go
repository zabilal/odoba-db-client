package shell

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

func labels(m *fyne.Menu) []string {
	var out []string
	for _, it := range m.Items {
		out = append(out, it.Label)
	}
	return out
}

func titleOf(fx *fixture, id string) string {
	c, _ := fx.s.reg.Get(id)
	return c.Title
}

func TestTheExplorerMenuIsTheNodesCommands(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	items := view.NodeID(c.ID, itemsNode.Ref)
	bar := fx.s.menuItems[cmdFavorite]

	m := fx.s.explorerMenu(items)
	want := []string{titleOf(fx, cmdOpen), titleOf(fx, cmdStructure), titleOf(fx, cmdFavorite), "", titleOf(fx, cmdRefresh)}
	if got := labels(m); !slices.Equal(got, want) {
		t.Fatalf("a table's menu is %q, want %q", got, want)
	}
	if fx.s.menuItems[cmdFavorite] != bar {
		t.Error("making a pop-up menu took over the menu bar's own item")
	}
	if m.Items[0].Disabled || m.Items[1].Disabled || m.Items[2].Checked {
		t.Error("Open Data and Open Structure should be enabled, and Favourite not ticked yet")
	}
	m.Items[2].Action()
	if len(fx.settings.Get().Favorites) != 1 {
		t.Fatal("choosing Favourite in the menu should make the table a favourite")
	}
	if m = fx.s.explorerMenu(items); !m.Items[2].Checked {
		t.Error("the next time, Favourite should be ticked")
	}

	dm := fx.s.explorerMenu(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	if !dm.Items[0].Disabled || !dm.Items[1].Disabled || !dm.Items[2].Disabled {
		t.Error("a database has no rows: Open Data, Open Structure and Favourite should be disabled in its menu, as on the menu bar")
	}

	cm := fx.s.explorerMenu(view.ConnectionID(c.ID))
	got := labels(cm)
	for _, id := range []string{cmdConnEdit, cmdConnDup, cmdDisconnect, cmdConnDelete} {
		found := false
		for _, l := range got {
			found = found || l == titleOf(fx, id)
		}
		if !found {
			t.Errorf("a connection's menu %q lacks %q", got, titleOf(fx, id))
		}
	}
}

func TestARightClickShowsTheMenu(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	if fx.s.Explorer.OnMenu == nil {
		t.Fatal("the shell should answer the explorer's right-clicks")
	}
	fx.s.Explorer.OnMenu(view.NodeID(c.ID, itemsNode.Ref), fyne.NewPos(10, 10))
	if fx.s.win.Canvas().Overlays().Top() == nil {
		t.Error("a right-click should show the node's menu")
	}
}
