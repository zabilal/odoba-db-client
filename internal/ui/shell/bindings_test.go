package shell

import (
	"strings"
	"testing"

	"github.com/ikigai-db/ikigai-db/internal/store"
	"github.com/ikigai-db/ikigai-db/internal/ui/commands"
)

func TestAShortcutCanBeChangedAndIsKept(t *testing.T) {
	fx := newFixture(t)
	sc := commands.Shortcut{Key: "J", Mods: commands.ModShortcut | commands.ModAlt}
	if err := fx.s.setBinding(cmdFind, sc); err != nil {
		t.Fatal(err)
	}
	if c, _ := fx.s.reg.Get(cmdFind); c.Shortcut != sc {
		t.Errorf("Find is on %+v", c.Shortcut)
	}
	if it := fx.s.menuItems[cmdFind]; it == nil || it.Shortcut == nil || it.Shortcut.ShortcutName() != fyneShortcut(sc).ShortcutName() {
		t.Error("the menu bar should carry the new shortcut, since it is what makes it work")
	}
	if got := fx.settings.Get().Bindings[cmdFind]; got != sc.String() {
		t.Errorf("kept as %q", got)
	}
	s := fx.relaunch(t)
	if c, _ := s.reg.Get(cmdFind); c.Shortcut != sc {
		t.Error("a changed shortcut should come back at the next start")
	}
	if err := fx.s.setBinding(cmdFind, fx.s.reg.Default(cmdFind)); err != nil {
		t.Fatal(err)
	}
	if _, ok := fx.settings.Get().Bindings[cmdFind]; ok {
		t.Error("a shortcut set back to its default should leave the file")
	}
}

func TestAClashingShortcutIsRefused(t *testing.T) {
	fx := newFixture(t)
	save, _ := fx.s.reg.Get(cmdQuerySave)
	before, _ := fx.s.reg.Get(cmdFind)
	if err := fx.s.setBinding(cmdFind, save.Shortcut); err == nil {
		t.Fatal("Save Query has that chord; Find should not be given it")
	}
	if c, _ := fx.s.reg.Get(cmdFind); c.Shortcut != before.Shortcut || len(fx.settings.Get().Bindings) != 0 {
		t.Error("a refused shortcut should change nothing")
	}
}

func TestAStoredShortcutThatNoLongerFitsIsSaid(t *testing.T) {
	fx := newFixture(t)
	if err := fx.settings.Update(func(st *store.Settings) error {
		st.Bindings = map[string]string{"no.such.command": "Shortcut+J", cmdFind: "Bogus+J", cmdHistory: "Shortcut+S"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := fx.relaunch(t)
	for _, want := range []string{"no.such.command", "not a modifier", "both use"} {
		if !strings.Contains(s.errors.text, want) {
			t.Errorf("error band %q should mention %q", s.errors.text, want)
		}
	}
	if got := fx.settings.Get().Bindings; len(got) != 3 {
		t.Error("bindings that did not fit should stay in the file, not be dropped unseen")
	}
	if c, _ := s.reg.Get(cmdFind); c.Shortcut != s.reg.Default(cmdFind) {
		t.Error("an unreadable binding should leave the command's default")
	}
}
