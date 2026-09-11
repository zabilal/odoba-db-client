package shell

import (
	"slices"
	"testing"

	"fyne.io/fyne/v2/test"
)

func shownIDs(p *shortcutsPanel) []string {
	var out []string
	for _, c := range p.shown {
		out = append(out, c.ID)
	}
	return out
}

func TestTheShortcutSheetListsEveryShortcut(t *testing.T) {
	fx := newFixture(t)
	p := fx.s.showShortcuts()
	want := 0
	for _, c := range fx.s.reg.All() {
		if !c.Shortcut.IsZero() {
			want++
		}
	}
	if want == 0 || len(p.shown) != want {
		t.Errorf("the sheet lists %d shortcuts, want all %d", len(p.shown), want)
	}
	if !slices.Contains(shownIDs(p), cmdQueryRun) {
		t.Error("Run cannot run with no query tab, but it has a shortcut and belongs in the reference")
	}
}

func TestTheShortcutSheetFiltersByNameOrKeys(t *testing.T) {
	fx := newFixture(t)
	p := fx.s.showShortcuts()
	test.Type(p.filter, "save query")
	if got := shownIDs(p); !slices.Contains(got, cmdQuerySave) || !slices.Contains(got, cmdQuerySaveAs) {
		t.Errorf("\"save query\" found %v", got)
	}
	// Every word must match: Run is in the Query category but has nothing to
	// do with saving.
	if slices.Contains(shownIDs(p), cmdQueryRun) {
		t.Errorf("\"save query\" kept Run: every word typed should have to match")
	}
	c, _ := fx.s.reg.Get(cmdQuerySaveAs)
	keys := c.Shortcut.Label("darwin")
	p.filter.SetText(keys)
	if got := shownIDs(p); !slices.Contains(got, cmdQuerySaveAs) {
		t.Errorf("the keys %q found %v", keys, got)
	}
	p.filter.SetText("zzz")
	if len(p.shown) != 0 || p.status.Text != "No shortcuts match." {
		t.Errorf("nothing should match: %v, %q", shownIDs(p), p.status.Text)
	}
}

func TestTheShortcutSheetIsAPanelOnCommandSlash(t *testing.T) {
	fx := newFixture(t)
	c, _ := fx.s.reg.Get(cmdShortcuts)
	if got := c.Shortcut.Label("darwin"); got != "⌘/" {
		t.Errorf("Keyboard Shortcuts is on %q, want ⌘/", got)
	}
	fx.s.run(cmdShortcuts)
	if !fx.s.panelIs(panelShortcuts) || !fx.s.menuItems[cmdShortcuts].Checked {
		t.Error("⌘/ should open the sheet as a panel, ticked in the Help menu")
	}
	fx.s.run(cmdShortcuts)
	if fx.s.Panel() != nil {
		t.Error("⌘/ again should close it")
	}
}
