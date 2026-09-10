package theme

import (
	"strings"
	"testing"
)

// TestEveryRoleIsContrastChecked guards the gap that matters most: a colour
// role added to Palette without a corresponding entry in the contrast suite
// would ship unverified, and nobody would notice until a user could not read
// it. Adding a role therefore forces a decision — check it, or exempt it here
// with a stated reason.
func TestEveryRoleIsContrastChecked(t *testing.T) {
	// Roles that carry no foreground/background relationship of their own.
	exempt := map[string]string{
		"ControlAccentPressed": "transient press state, never a text background",
		"TertiaryLabel":        "placeholder tier; WCAG 1.4.3 exempts, ordering checked instead",
		"QuaternaryLabel":      "disabled tier; WCAG 1.4.3 exempts, ordering checked instead",
		"Separator":            "decorative hairline; WCAG 1.4.11 exempts non-identifying lines",
		"OpaqueSeparator":      "decorative; ControlBorder carries the 3:1 duty",
		"FocusRing":            "translucent overlay, drawn over varying backgrounds",
		"Scrollbar":            "translucent overlay, drawn over varying backgrounds",
		"ScrollbarHover":       "translucent overlay, drawn over varying backgrounds",
		"Shadow":               "translucent overlay, drawn over varying backgrounds",
	}

	// Pair names are "Foreground/Background". A role appearing on EITHER side
	// has been verified against a real neighbour, so both halves count.
	alias := map[string]string{
		"OnSelected":     "OnSelectedEmphasized",
		"SelectedEmph":   "SelectedEmphasized",
		"SelectedUnemph": "SelectedUnemphasized",
		"OnAccent":       "OnControlAccent",
		"Accent":         "ControlAccent",
		"AccentHover":    "ControlAccentHover",
		"AccentSubtle":   "ControlAccentSubtle",
		"Content":        "ContentBackground",
		"Window":         "WindowBackground",
		"Sidebar":        "SidebarBackground",
		"Elevated":       "ElevatedBackground",
		"Control":        "ControlBackground",
	}

	checked := map[string]bool{}
	note := func(name string) {
		if full, ok := alias[name]; ok {
			name = full
		}
		checked[name] = true
	}
	for _, group := range [][]pair{textPairs, componentPairs} {
		for _, c := range group {
			fg, bg, ok := strings.Cut(c.name, "/")
			if !ok {
				t.Fatalf("pair %q is not in Foreground/Background form", c.name)
			}
			note(fg)
			note(bg)
		}
	}

	for _, role := range RoleNames() {
		if checked[role] {
			continue
		}
		if _, ok := exempt[role]; ok {
			continue
		}
		t.Errorf("palette role %q is neither contrast-checked nor exempted; "+
			"add it to textPairs/componentPairs, or to the exempt map with a reason", role)
	}
}

func TestRoleColorLookup(t *testing.T) {
	if _, ok := RoleColor(Light, "Label"); !ok {
		t.Error("RoleColor failed on a known role")
	}
	if _, ok := RoleColor(Light, "NoSuchRole"); ok {
		t.Error("RoleColor succeeded on an unknown role")
	}
	if len(RoleNames()) < 30 {
		t.Errorf("RoleNames returned only %d roles; reflection is not seeing the struct",
			len(RoleNames()))
	}
}
