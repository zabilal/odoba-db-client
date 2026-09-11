package shell

import (
	"testing"

	uitheme "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

func TestTheAccentIsChosenSavedAndShown(t *testing.T) {
	fx := newFixture(t)
	if fx.s.d.Theme.Accent != uitheme.AccentBlue {
		t.Fatal("the accent is blue until one is chosen")
	}
	before := fx.s.colours().ControlAccent
	fx.s.run(accentID(uitheme.AccentPurple))
	if fx.s.d.Theme.Accent != uitheme.AccentPurple {
		t.Fatalf("accent %s, want purple", fx.s.d.Theme.Accent)
	}
	if got := fx.settings.Get().Accent; got != "purple" {
		t.Errorf("settings keep %q", got)
	}
	if !fx.s.checked(accentID(uitheme.AccentPurple)) || fx.s.checked(accentID(uitheme.AccentBlue)) {
		t.Error("the menu should tick purple, and only purple")
	}
	if fx.s.colours().ControlAccent == before {
		t.Error("the palette should wear the accent")
	}
}
