package theme

import "testing"

func TestEnvironmentBadgeContrast(t *testing.T) {
	// The badge is a filled pill with a label on it. If the label is not
	// legible the treatment conveys nothing.
	th := New()
	for _, dark := range []bool{false, true} {
		for _, name := range EnvironmentNames() {
			e := th.Environment(name, dark)
			if r := contrastRatio(e.OnAccent, e.Accent); r < aaText {
				t.Errorf("dark=%v %s: label on badge %.2f:1 (need %.1f:1)",
					dark, name, r, aaText)
			}
		}
	}
}

func TestEnvironmentLabelIsAlwaysPresent(t *testing.T) {
	// Colour alone would make this safety feature fail for users with colour
	// vision deficiency — dev/production is a red/green pair. The text label
	// is the non-colour channel and must never be empty.
	th := New()
	for _, dark := range []bool{false, true} {
		for _, name := range EnvironmentNames() {
			if th.Environment(name, dark).Label == "" {
				t.Errorf("dark=%v %s: empty label", dark, name)
			}
		}
	}
}

func TestEnvironmentsAreMutuallyDistinct(t *testing.T) {
	th := New()
	for _, dark := range []bool{false, true} {
		names := EnvironmentNames()
		for i := 0; i < len(names); i++ {
			for j := i + 1; j < len(names); j++ {
				a := th.Environment(names[i], dark)
				b := th.Environment(names[j], dark)
				if a.Label == b.Label {
					t.Errorf("dark=%v: %s and %s share label %q",
						dark, names[i], names[j], a.Label)
				}
				if d := channelDistance(a.Accent, b.Accent); d < 30 {
					t.Errorf("dark=%v: %s and %s accents differ by only %d",
						dark, names[i], names[j], d)
				}
			}
		}
	}
}

func TestOnlyProductionIsEmphatic(t *testing.T) {
	// If several environments shout, none of them do.
	th := New()
	for _, dark := range []bool{false, true} {
		for _, name := range EnvironmentNames() {
			got := th.Environment(name, dark).Emphatic
			want := name == "production"
			if got != want {
				t.Errorf("dark=%v %s: Emphatic=%v, want %v", dark, name, got, want)
			}
		}
	}
}

func TestUnknownEnvironmentIsNeutralNotDangerous(t *testing.T) {
	// An unlabelled connection should look ordinary. Falling back to the
	// production treatment would cry wolf; falling back to nothing would make
	// the badge vanish.
	th := New()
	got := th.Environment("", false)
	if got.Emphatic {
		t.Error("unknown environment must not be emphatic")
	}
	if got.Label == "" {
		t.Error("unknown environment must still carry a label")
	}
	if got.Label != th.Environment("local", false).Label {
		t.Errorf("unknown environment should fall back to local, got %q", got.Label)
	}
}
