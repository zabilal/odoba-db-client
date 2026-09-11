package store

import (
	"path/filepath"
	"testing"
)

func TestAFavouriteNamesAConnectionThisFileHoldsOnce(t *testing.T) {
	f, _, err := OpenSettings(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	fav := Favorite{ConnectionID: "c1", Kind: "table", Path: []string{"main", "t"}, Label: "t"}
	if err := f.Update(func(s *Settings) error { s.Favorites = []Favorite{fav}; return nil }); err == nil {
		t.Error("a favourite on a connection the file does not hold was saved")
	}
	if err := f.Update(func(s *Settings) error {
		s.Connections = append(s.Connections, SavedConnection{ID: "c1", Name: "db", Driver: "sqlite"})
		s.Favorites = []Favorite{fav}
		return nil
	}); err != nil {
		t.Fatalf("a good favourite was refused: %v", err)
	}
	if err := f.Update(func(s *Settings) error { s.Favorites = append(s.Favorites, fav); return nil }); err == nil {
		t.Error("a favourite listed twice was saved")
	}
	bare := Favorite{ConnectionID: "c1", Label: "nothing"}
	if err := f.Update(func(s *Settings) error { s.Favorites = append(s.Favorites, bare); return nil }); err == nil {
		t.Error("a favourite naming no object was saved")
	}
	if got := f.Get().Favorites; len(got) != 1 || got[0].Key() != fav.Key() {
		t.Errorf("favourites %+v", got)
	}
}
