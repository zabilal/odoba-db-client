package shell

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
)

// Custom shortcuts (T1.8, FR-15.4). A command's chord can be changed, and
// the change is kept in the settings file. Bindings are applied once every
// command is registered and before the menu bar is built, since the menu bar
// is what makes a shortcut work (ADR-0011 §2).

// applyBindings applies the custom shortcuts kept in the settings file. One
// that no longer fits, for a command that has gone, in a form that does not
// read, or on a chord another command has, is said, once, and left in the
// file untouched rather than dropped unseen.
func (s *Shell) applyBindings() {
	if s.d.Settings == nil {
		return
	}
	bindings := s.d.Settings.Get().Bindings
	var bad []string
	for _, id := range slices.Sorted(maps.Keys(bindings)) {
		sc, err := commands.ParseShortcut(bindings[id])
		if err == nil {
			err = s.reg.Rebind(id, sc)
		}
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s (%s): %v", id, bindings[id], err))
		}
	}
	if len(bad) > 0 {
		s.showError(fmt.Errorf("some custom shortcuts were not applied: %s", strings.Join(bad, "; ")))
	}
}

// setBinding gives a command a new shortcut, keeps it in the settings file
// and rebuilds the menu bar. A shortcut set back to its default is taken
// out of the file. A chord another command has is refused, and nothing
// changes.
func (s *Shell) setBinding(id string, sc commands.Shortcut) error {
	if err := s.reg.Rebind(id, sc); err != nil {
		return err
	}
	defer s.rebuildMenu()
	if s.d.Settings == nil {
		return nil
	}
	def := s.reg.Default(id)
	if err := s.d.Settings.Update(func(st *store.Settings) error {
		if sc == def {
			delete(st.Bindings, id)
			return nil
		}
		if st.Bindings == nil {
			st.Bindings = map[string]string{}
		}
		st.Bindings[id] = sc.String()
		return nil
	}); err != nil {
		return fmt.Errorf("the shortcut works now, but could not be kept for the next start: %w", err)
	}
	return nil
}

// rebuildMenu builds the menu bar afresh, so that its shortcuts are the
// registry's. Building it records the new items for sync to keep up to date.
func (s *Shell) rebuildMenu() {
	s.menu = s.buildMenu()
	s.win.SetMainMenu(s.menu)
	s.sync()
}
