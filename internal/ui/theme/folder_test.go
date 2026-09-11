package theme

import "testing"

func TestEveryFolderColourStandsOutInTheSidebar(t *testing.T) {
	// A folder's colour is a dot beside its name in the explorer, which sits
	// in the sidebar: a graphic, held to WCAG's 3:1 against what it sits on.
	for _, p := range []struct {
		dark bool
		pal  Palette
	}{{false, Light}, {true, Dark}} {
		for _, a := range Accents() {
			c, ok := FolderColor(a.String(), p.dark)
			if !ok {
				t.Errorf("%s has no folder colour", a)
				continue
			}
			if r := contrastRatio(c, p.pal.SidebarBackground); r < 3 {
				t.Errorf("dark=%v %s on the sidebar: %.2f:1, want 3:1", p.dark, a, r)
			}
			if r := contrastRatio(c, p.pal.ContentBackground); r < 3 {
				t.Errorf("dark=%v %s on the content: %.2f:1, want 3:1", p.dark, a, r)
			}
		}
	}
	if _, ok := FolderColor("", false); ok {
		t.Error("a folder with no colour is marked in one")
	}
	if _, ok := FolderColor("mauve", false); ok {
		t.Error("a colour no accent has is marked in one")
	}
	for _, dark := range []bool{false, true} {
		if red, _ := FolderColor("red", dark); red != accentPalette(AccentRed, dark).AccentText {
			t.Errorf("dark=%v: red is marked %v, want red's text shade in that appearance", dark, red)
		}
	}
}
