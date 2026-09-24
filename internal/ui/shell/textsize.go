package shell

import (
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

// How large text is drawn (NFR-A2).
//
// Fyne scales the whole canvas for the display, which is what makes the
// window the right size on a high-resolution screen. That is not the same
// question as this one: somebody may want larger text on a screen whose
// scale is already right, and every platform offers that setting for its
// own applications. This is ours, kept with the appearance and the accent,
// and it scales the type and nothing else — spacing, icons and radii are
// the window's proportions.

// textSizeID names the command that chooses one size.
func textSizeID(t uitheme.TextSize) string { return "appearance.text." + textSizeName(t) }

// textSizeName is what a size is saved and found by.
func textSizeName(t uitheme.TextSize) string {
	for _, s := range uitheme.TextSizes {
		if s.Size == t {
			return strings.ToLower(s.Name)
		}
	}
	return "default"
}

// textSizeNamed is the size a saved name means. A name nobody offers is
// the default, which is what a settings file from a later version should
// read as rather than as nothing at all.
func textSizeNamed(name string) uitheme.TextSize {
	for _, s := range uitheme.TextSizes {
		if strings.EqualFold(s.Name, name) {
			return s.Size
		}
	}
	return uitheme.TextDefault
}

// textSizeIDs are the commands, in order, for the menu.
func textSizeIDs() []string {
	var out []string
	for _, s := range uitheme.TextSizes {
		out = append(out, textSizeID(s.Size))
	}
	return out
}

// textSizeCommands choose how large text is drawn, one command a size, as
// the Appearance submenu does for light and dark.
func (s *Shell) textSizeCommands() []commands.Command {
	var out []commands.Command
	for _, size := range uitheme.TextSizes {
		out = append(out, commands.Command{ID: textSizeID(size.Size), Category: "Text Size",
			Title: size.Name, Keywords: []string{"text", "size", "larger", "bigger", "font", "accessibility"},
			Run: func() { s.setTextSize(size.Size) }})
	}
	return out
}

// setTextSize draws the application's text at a size and keeps the choice.
func (s *Shell) setTextSize(t uitheme.TextSize) {
	s.d.Theme.Text = t
	s.app.Settings().SetTheme(s.d.Theme)
	s.recolour()
	if s.d.Settings != nil {
		if err := s.d.Settings.Update(func(st *store.Settings) error {
			st.TextSize = textSizeName(t)
			return nil
		}); err != nil {
			s.d.Log.Warn("saving the text size", "err", err)
		}
	}
	s.sync()
}
