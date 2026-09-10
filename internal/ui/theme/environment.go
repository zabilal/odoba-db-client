package theme

import "image/color"

// Environment treatment (FR-1.7, UX principle 8).
//
// A connection's environment is carried across every tab derived from it, so
// that a user always knows what they are about to write to.
//
// The palette is deliberately not the only signal. Red/green is the most
// common axis of colour vision deficiency, and dev/production is exactly that
// pair — a user with deuteranopia would see the safest and most dangerous
// connections as near-identical. Every treatment therefore also carries a
// short text label, and callers must render it. Colour alone would make this
// a safety feature that fails for roughly one in twelve men.

// EnvironmentTint is the complete visual treatment for one environment.
type EnvironmentTint struct {
	// Label is the short text shown in the badge. Never omit it: it is the
	// non-colour channel that makes the treatment accessible.
	Label string

	// Accent is the badge fill and the tab stripe.
	Accent color.NRGBA

	// OnAccent is the label colour drawn on Accent.
	OnAccent color.NRGBA

	// Subtle tints a toolbar or connection row without shouting.
	Subtle color.NRGBA

	// Emphatic marks an environment that warrants a persistent, unmissable
	// treatment rather than a quiet one. True only for production.
	Emphatic bool
}

// environmentTints maps an environment name to its treatment. Keys match
// source.Environment values; theme takes a plain string so that the UI theme
// carries no dependency on the data layer.
var environmentTints = map[string]struct{ light, dark EnvironmentTint }{
	"local": {
		light: EnvironmentTint{Label: "LOCAL", Accent: rgb(0x6E, 0x6E, 0x73),
			OnAccent: rgb(0xFF, 0xFF, 0xFF), Subtle: rgb(0xEE, 0xEE, 0xF0)},
		dark: EnvironmentTint{Label: "LOCAL", Accent: rgb(0x8E, 0x8E, 0x93),
			OnAccent: rgb(0x1E, 0x1E, 0x1E), Subtle: rgb(0x2E, 0x2E, 0x30)},
	},
	"dev": {
		light: EnvironmentTint{Label: "DEV", Accent: rgb(0x1B, 0x7A, 0x3D),
			OnAccent: rgb(0xFF, 0xFF, 0xFF), Subtle: rgb(0xE4, 0xF6, 0xEA)},
		dark: EnvironmentTint{Label: "DEV", Accent: rgb(0x32, 0xD7, 0x4B),
			OnAccent: rgb(0x11, 0x2A, 0x18), Subtle: rgb(0x17, 0x2E, 0x1E)},
	},
	"staging": {
		light: EnvironmentTint{Label: "STAGING", Accent: rgb(0x9A, 0x5B, 0x00),
			OnAccent: rgb(0xFF, 0xFF, 0xFF), Subtle: rgb(0xFF, 0xF1, 0xD9)},
		dark: EnvironmentTint{Label: "STAGING", Accent: rgb(0xFF, 0x9F, 0x0A),
			OnAccent: rgb(0x2A, 0x1C, 0x00), Subtle: rgb(0x33, 0x27, 0x0D)},
	},
	"production": {
		light: EnvironmentTint{Label: "PROD", Accent: rgb(0xC4, 0x1E, 0x18),
			OnAccent: rgb(0xFF, 0xFF, 0xFF), Subtle: rgb(0xFF, 0xE3, 0xE1), Emphatic: true},
		dark: EnvironmentTint{Label: "PROD", Accent: rgb(0xFF, 0x45, 0x3A),
			OnAccent: rgb(0x2E, 0x0F, 0x0D), Subtle: rgb(0x3D, 0x1C, 0x1A), Emphatic: true},
	},
}

// Environment returns the treatment for an environment name.
//
// An unknown name falls back to the neutral local treatment rather than to
// nothing: an unlabelled connection should look ordinary, never dangerous, and
// never invisible.
func (t *Theme) Environment(name string, dark bool) EnvironmentTint {
	e, ok := environmentTints[name]
	if !ok {
		e = environmentTints["local"]
	}
	if dark {
		return e.dark
	}
	return e.light
}

// EnvironmentNames returns the known environments in escalating order of risk,
// which is the order the connection form presents them in.
func EnvironmentNames() []string {
	return []string{"local", "dev", "staging", "production"}
}
