package shell

import (
	"strings"
	"testing"

	"fyne.io/fyne/v2"

	"github.com/ikigai-db/ikigai-db/internal/l10n"
)

// In a language nobody speaks, everything the window says on its own account
// is in that language (FR-15.11): every command, in the palette and in the
// menus, and the menus themselves. What is still plain never went through
// the catalogue, and no translation would reach it.
func TestEveryCommandAndMenuIsSaidInTheLanguageInUse(t *testing.T) {
	l10n.Use(l10n.Pseudo)
	t.Cleanup(func() { l10n.Use("en") })
	fx := newFixture(t)
	spoken := func(s string) bool { return s == "" || (strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")) }
	all := fx.s.reg.All()
	if len(all) < 100 {
		t.Fatalf("only %d commands were found: this is reading the wrong thing", len(all))
	}
	for _, c := range all {
		if !spoken(c.Title) || !spoken(c.Category) {
			t.Errorf("%s is %q in %q", c.ID, c.Title, c.Category)
		}
	}
	var walk func(m *fyne.Menu, at string)
	walk = func(m *fyne.Menu, at string) {
		if !spoken(m.Label) {
			t.Errorf("the menu %q under %s", m.Label, at)
		}
		for _, it := range m.Items {
			if !it.IsSeparator && !spoken(it.Label) {
				t.Errorf("%q in the menu %s", it.Label, m.Label)
			}
			if it.ChildMenu != nil {
				walk(it.ChildMenu, m.Label)
			}
		}
	}
	for _, m := range fx.s.buildMenu().Items {
		walk(m, "the bar")
	}
	// And in English nothing is marked: the catalogue is not in the way.
	l10n.Use("en")
	en := newFixture(t)
	if c, ok := en.s.reg.Get(cmdImport); !ok || strings.HasPrefix(c.Title, "[") {
		t.Errorf("in English: %q", c.Title)
	}
}
