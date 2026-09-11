package shell

import (
	"fmt"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/ikigai-db/ikigai-db/internal/model"
	"github.com/ikigai-db/ikigai-db/internal/store"
)

// Favourites are objects pinned above the explorer's tree (FR-2.6,
// ADR-0019). They live in the settings file, and only objects with rows can
// be one, so choosing a favourite always has something to open.

// newFavorites builds the sidebar's favourites section, hidden while there
// are none (UX principle 2).
func (s *Shell) newFavorites() *fyne.Container {
	title := widget.NewLabelWithStyle("Favourites", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	s.favRows = container.NewVBox()
	s.favBox = container.NewVBox(title, s.favRows, widget.NewSeparator())
	s.refreshFavorites()
	return s.favBox
}

// refreshFavorites redraws the favourites from the settings file.
func (s *Shell) refreshFavorites() {
	if s.favBox == nil {
		return
	}
	var favs []store.Favorite
	if s.d.Settings != nil {
		favs = s.d.Settings.Get().Favorites
	}
	rows := make([]fyne.CanvasObject, 0, len(favs))
	for _, f := range favs {
		conn := "a deleted connection"
		if c, ok := s.d.Conns.Get(f.ConnectionID); ok {
			conn = c.Name
		}
		open := widget.NewButton(fmt.Sprintf("%s — %s", f.Label, conn), func() { s.openFavorite(f) })
		open.Alignment = widget.ButtonAlignLeading
		open.Importance = widget.LowImportance
		remove := widget.NewButton("Remove", func() { s.setFavorite(f, false) })
		remove.Importance = widget.LowImportance
		rows = append(rows, container.NewBorder(nil, nil, nil, remove, open))
	}
	s.favRows.Objects = rows
	s.favRows.Refresh()
	if len(rows) == 0 {
		s.favBox.Hide()
	} else {
		s.favBox.Show()
	}
}

// selectedFavorite is the explorer's selection as a favourite, if it is an
// object that can be one.
func (s *Shell) selectedFavorite() (store.Favorite, bool) {
	conn, n, ok := s.Explorer.SelectedNode()
	if !ok || !n.Browsable || s.d.Settings == nil {
		return store.Favorite{}, false
	}
	return store.Favorite{ConnectionID: conn, Kind: string(n.Ref.Kind), Path: n.Ref.Path, Label: n.Label}, true
}

func (s *Shell) isFavorite(f store.Favorite) bool {
	return s.d.Settings != nil && slices.ContainsFunc(s.d.Settings.Get().Favorites,
		func(g store.Favorite) bool { return g.Key() == f.Key() })
}

// toggleFavorite makes the selected object a favourite, or stops it being
// one.
func (s *Shell) toggleFavorite() {
	if f, ok := s.selectedFavorite(); ok {
		s.setFavorite(f, !s.isFavorite(f))
	}
}

func (s *Shell) setFavorite(f store.Favorite, on bool) {
	err := s.d.Settings.Update(func(st *store.Settings) error {
		st.Favorites = slices.DeleteFunc(st.Favorites, func(g store.Favorite) bool { return g.Key() == f.Key() })
		if on {
			st.Favorites = append(st.Favorites, f)
		}
		return nil
	})
	if err != nil {
		s.showError(fmt.Errorf("the favourites could not be saved: %w", err))
	}
	s.refreshFavorites()
	s.sync()
}

// openFavorite opens a favourite's rows, connecting first if need be, as a
// reopened tab does.
func (s *Shell) openFavorite(f store.Favorite) {
	s.OpenObject(f.ConnectionID, model.Node{Ref: model.NewRef(model.ObjectKind(f.Kind), f.Path...), Label: f.Label, Browsable: true})
}
