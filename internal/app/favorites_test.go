package app

import (
	"path/filepath"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/store/secrets"
)

func TestDeletingAConnectionDropsItsFavourites(t *testing.T) {
	sf, _, err := store.OpenSettings(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sf.Update(func(s *store.Settings) error {
		s.Connections = []store.SavedConnection{{ID: "c1", Name: "one", Driver: "x"}, {ID: "c2", Name: "two", Driver: "x"}}
		s.Favorites = []store.Favorite{
			{ConnectionID: "c1", Kind: "table", Path: []string{"a"}, Label: "a"},
			{ConnectionID: "c2", Kind: "table", Path: []string{"b"}, Label: "b"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	conns := NewConnections(sf, NewVault(secrets.NewMemory(), nil), nil)
	if err := conns.Delete("c1"); err != nil {
		t.Fatalf("deleting a connection with a favourite: %v", err)
	}
	if got := sf.Get().Favorites; len(got) != 1 || got[0].ConnectionID != "c2" {
		t.Errorf("favourites after the delete: %+v", got)
	}
}
