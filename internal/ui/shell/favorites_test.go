package shell

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"

	"github.com/ikigai-db/ikigai-db/internal/app"
	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer"
	"github.com/ikigai-db/ikigai-db/internal/ui/explorer/view"
)

// loaded asks the explorer for a node's children and waits for them.
func loaded(t *testing.T, fx *fixture, id string) {
	t.Helper()
	pump(t, fx.q, func() bool {
		k := fx.s.Explorer.Model.Children(id)
		return len(k) > 0 && !explorer.IsPlaceholder(k[0])
	})
}

// selectItems selects the items table in the explorer, loading the tree
// down to it, and returns its connection.
func selectItems(t *testing.T, fx *fixture) store.SavedConnection {
	t.Helper()
	c := fx.create(t, "db1", nil)
	fx.s.Explorer.Refresh(explorer.RootID) // as saving a connection does; the tree listed none at start
	loaded(t, fx, explorer.RootID)
	loaded(t, fx, view.ConnectionID(c.ID))
	loaded(t, fx, view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, itemsNode.Ref))
	fx.s.sync()
	return c
}

func TestAFavouriteIsToggledAndOpens(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	if fx.s.favBox.Visible() {
		t.Error("with no favourites, the section should not show")
	}
	fx.s.run(cmdFavorite)
	if got := fx.settings.Get().Favorites; len(got) != 1 || got[0].Label != "items" || got[0].ConnectionID != c.ID {
		t.Fatalf("favourites %+v", got)
	}
	if !fx.s.favBox.Visible() || !fx.s.menuItems[cmdFavorite].Checked {
		t.Error("the favourite should be listed, and ticked in the menu")
	}
	test.Tap(findButton(fx.s.favBox, "items — db1"))
	if tb := fx.onlyTab(t); tb.item.Text != "items" {
		t.Errorf("choosing the favourite opened %q", tb.item.Text)
	}
	fx.s.run(cmdFavorite)
	if len(fx.settings.Get().Favorites) != 0 || fx.s.favBox.Visible() || fx.s.menuItems[cmdFavorite].Checked {
		t.Error("⌘D again should remove the favourite")
	}
}

func TestOnlyAnObjectWithRowsCanBeAFavourite(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	fx.s.Explorer.Tree.Select(view.NodeID(c.ID, model.NewRef(model.KindDatabase, "main")))
	fx.s.sync()
	if !fx.s.menuItems[cmdFavorite].Disabled {
		t.Error("a database has no rows to open, so it cannot be a favourite")
	}
}

func TestFavouritesAreKeptAndGoWithTheirConnection(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	fx.s.run(cmdFavorite)
	disk, _, err := store.OpenSettings(filepath.Join(filepath.Dir(fx.settingsPath), filepath.Base(fx.settingsPath)))
	if err != nil {
		t.Fatal(err)
	}
	if got := disk.Get().Favorites; len(got) != 1 {
		t.Fatalf("the settings file on disk holds %d favourites, want 1", len(got))
	}
	s := fx.relaunch(t)
	if !s.favBox.Visible() || findButton(s.favBox, "items — db1") == nil {
		t.Error("favourites should be there at the next start")
	}
	test.Tap(findButton(fx.s.favBox, "Remove"))
	if len(fx.settings.Get().Favorites) != 0 || fx.s.favBox.Visible() {
		t.Fatal("Remove should take the favourite off the list")
	}
	fx.s.run(cmdFavorite) // the table is still selected: make it a favourite again
	if len(fx.settings.Get().Favorites) != 1 {
		t.Fatal("setup: the favourite should be back")
	}
	fx.s.deleteConnection(c.ID)
	if len(fx.settings.Get().Favorites) != 0 || fx.s.favBox.Visible() {
		t.Error("deleting a connection should take its favourites with it")
	}
}

func TestARenamedConnectionRenamesItsFavourites(t *testing.T) {
	fx := newFixture(t)
	c := selectItems(t, fx)
	fx.s.run(cmdFavorite)
	c.Name = "renamed"
	if err := fx.conns.Update(c, app.SecretEdit{}); err != nil {
		t.Fatal(err)
	}
	fx.s.connectionSaved(c.ID, true)
	if findButton(fx.s.favBox, "items — renamed") == nil {
		t.Error("a renamed connection should rename its favourites' rows")
	}
}
