package theme

import (
	"image/color"
	"reflect"
	"testing"

	ftheme "fyne.io/fyne/v2/theme"
)

func TestEveryAccentPassesAAInBothAppearances(t *testing.T) {
	pairs := []struct {
		name   string
		fg, bg func(Palette) color.NRGBA
	}{
		{"AccentText/Content", fAccentText, fContent},
		{"AccentText/Window", fAccentText, fWindow},
		{"AccentText/AccentSubtle", fAccentText, fAccentSubtle},
		{"OnAccent/Accent", fOnAccent, fAccent},
		{"OnAccent/AccentHover", fOnAccent, fAccentHover},
		{"OnSelected/SelectedEmph", fOnSelEmph, fSelEmph},
	}
	for _, a := range Accents() {
		for i, look := range []string{"light", "dark"} {
			p := accentPalette(a, i == 1)
			for _, pr := range pairs {
				if r := contrastRatio(pr.fg(p), pr.bg(p)); r < aaText {
					t.Errorf("%s accent, %s: %s is %.2f:1, below AA", a, look, pr.name, r)
				}
			}
		}
	}
}

func TestBlueIsThePalettesOwnAndTheOthersDiffer(t *testing.T) {
	if !reflect.DeepEqual(accentPalette(AccentBlue, false), Light) || !reflect.DeepEqual(accentPalette(AccentBlue, true), Dark) {
		t.Error("blue should be the hand-tuned palettes, untouched")
	}
	seen := map[color.NRGBA]Accent{}
	for _, a := range Accents() {
		c := accentPalette(a, false).ControlAccent
		if other, ok := seen[c]; ok {
			t.Errorf("%s and %s have the same fill", a, other)
		}
		seen[c] = a
	}
}

func TestAccentNamesRoundTrip(t *testing.T) {
	for _, a := range Accents() {
		if AccentNamed(a.String()) != a {
			t.Errorf("%s does not read back", a)
		}
	}
	if AccentNamed("mauve") != AccentBlue {
		t.Error("an accent not known should be blue")
	}
}

func TestTheThemeWearsItsAccent(t *testing.T) {
	th := &Theme{Accent: AccentPurple, Appearance: AppearanceLight}
	if th.PaletteFor(ftheme.VariantDark).ControlAccent != accentPalette(AccentPurple, false).ControlAccent {
		t.Error("the theme should wear its accent, in the appearance it is pinned to")
	}
}
