package shell

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// accentPrefix begins every accent command's ID.
const accentPrefix = "appearance.accent."

func accentID(a uitheme.Accent) string { return accentPrefix + a.String() }

// accentIDs are the accent commands, in the order macOS shows the colours.
func accentIDs() []string {
	var ids []string
	for _, a := range uitheme.Accents() {
		ids = append(ids, accentID(a))
	}
	return ids
}

// accentCommands choose the accent colour (FR-15.3), one command a colour,
// as the Appearance submenu does for light and dark.
func (s *Shell) accentCommands() []commands.Command {
	var out []commands.Command
	for _, a := range uitheme.Accents() {
		name := a.String()
		out = append(out, commands.Command{ID: accentID(a), Category: "Accent Colour",
			Title: strings.ToUpper(name[:1]) + name[1:], Keywords: []string{"theme", "colour", "color"},
			Run: func() { s.setAccent(a) }})
	}
	return out
}

// setAccent dresses the application in an accent and keeps the choice.
func (s *Shell) setAccent(a uitheme.Accent) {
	s.d.Theme.Accent = a
	s.app.Settings().SetTheme(s.d.Theme)
	s.recolour()
	if s.d.Settings != nil {
		if err := s.d.Settings.Update(func(st *store.Settings) error {
			st.Accent = a.String()
			return nil
		}); err != nil {
			s.d.Log.Warn("saving the accent colour", "err", err)
		}
	}
	s.sync()
}
