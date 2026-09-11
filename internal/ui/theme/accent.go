package theme

import (
	"image/color"
	"math"
)

// Accent is the colour that controls and the selection take (FR-15.3), from
// macOS's set. Blue is the palettes' own, tuned by hand. The others are
// derived from Apple's system colours so that each passes WCAG AA wherever
// the palette's blue does: where HIG and accessibility disagree,
// accessibility wins (UX principle 13).
type Accent uint8

const (
	AccentBlue Accent = iota
	AccentPurple
	AccentPink
	AccentRed
	AccentOrange
	AccentYellow
	AccentGreen
	AccentGraphite
)

var accentNames = [...]string{AccentBlue: "blue", AccentPurple: "purple", AccentPink: "pink", AccentRed: "red",
	AccentOrange: "orange", AccentYellow: "yellow", AccentGreen: "green", AccentGraphite: "graphite"}

// Accents lists every accent, in the order macOS's settings show them.
func Accents() []Accent {
	out := make([]Accent, len(accentNames))
	for i := range out {
		out[i] = Accent(i)
	}
	return out
}

// String is the accent's name in the settings file.
func (a Accent) String() string {
	if int(a) < len(accentNames) {
		return accentNames[a]
	}
	return accentNames[AccentBlue]
}

// AccentNamed is the accent with a name, or blue for a name it does not know.
func AccentNamed(name string) Accent {
	for i, n := range accentNames {
		if n == name {
			return Accent(i)
		}
	}
	return AccentBlue
}

// systemAccents are Apple's system colours, for light and for dark.
var systemAccents = [...][2]color.NRGBA{
	AccentPurple:   {rgb(0xAF, 0x52, 0xDE), rgb(0xBF, 0x5A, 0xF2)},
	AccentPink:     {rgb(0xFF, 0x2D, 0x55), rgb(0xFF, 0x37, 0x5F)},
	AccentRed:      {rgb(0xFF, 0x3B, 0x30), rgb(0xFF, 0x45, 0x3A)},
	AccentOrange:   {rgb(0xFF, 0x95, 0x00), rgb(0xFF, 0x9F, 0x0A)},
	AccentYellow:   {rgb(0xFF, 0xCC, 0x00), rgb(0xFF, 0xD6, 0x0A)},
	AccentGreen:    {rgb(0x28, 0xCD, 0x41), rgb(0x32, 0xD7, 0x4B)},
	AccentGraphite: {rgb(0x8E, 0x8E, 0x93), rgb(0x98, 0x98, 0x9D)},
}

// accented is every palette an accent makes, worked out once: the theme is
// asked for colours all the time, and the derivation is a search.
var accented [len(accentNames)][2]Palette

func init() {
	for a := range accented {
		accented[a][0] = derive(Accent(a), Light, false)
		accented[a][1] = derive(Accent(a), Dark, true)
	}
}

// accentPalette is the light or dark palette wearing an accent.
func accentPalette(a Accent, dark bool) Palette {
	if int(a) >= len(accented) {
		a = AccentBlue
	}
	if dark {
		return accented[a][1]
	}
	return accented[a][0]
}

// aa is WCAG AA for body text, the bar every accent pair is held to.
const aa = 4.5

// derive makes a palette wear an accent.
//
// The fill is darkened until white text on it passes, unless that would take
// it far from itself, as it would the lightest colours; then the colour stays
// and its text is near-black. Hover and pressed shades keep their text
// passing. As text, the accent is darkened on light, or lightened on dark,
// until it reads on the content, the window and its own tint. The selection
// follows the accent, as macOS's does.
func derive(a Accent, p Palette, dark bool) Palette {
	if a == AccentBlue {
		return p
	}
	c := systemAccents[a][0]
	if dark {
		c = systemAccents[a][1]
	}
	white, black, ink := rgb(0xFF, 0xFF, 0xFF), rgb(0x00, 0x00, 0x00), rgb(0x1D, 0x1D, 0x1F)
	passes := func(fg color.NRGBA) func(color.NRGBA) bool {
		return func(bg color.NRGBA) bool { return contrast(fg, bg) >= aa }
	}
	fill, on := c, white
	if darker, t := towards(c, black, passes(white)); t <= 0.35 {
		fill = darker
	} else {
		on = ink
		fill, _ = towards(c, white, passes(ink))
	}
	away := black // the way that parts a shade from its text
	if on != white {
		away = white
	}
	hover, pressed := mix(fill, black, 0.12), mix(fill, black, 0.24)
	if dark {
		hover = mix(fill, white, 0.10)
	}
	hover, _ = towards(hover, away, passes(on))
	pressed, _ = towards(pressed, away, passes(on))

	subtle := mix(c, p.ContentBackground, 0.86)
	reads := func(x color.NRGBA) bool {
		return contrast(x, p.ContentBackground) >= aa && contrast(x, p.WindowBackground) >= aa && contrast(x, subtle) >= aa
	}
	toward := black
	if dark {
		toward = white
	}
	text, _ := towards(c, toward, reads)

	p.ControlAccent, p.ControlAccentHover, p.ControlAccentPressed = fill, hover, pressed
	p.ControlAccentSubtle, p.OnControlAccent, p.AccentText = subtle, on, text
	p.SelectedEmphasized, p.OnSelectedEmphasized = fill, on
	return p
}

// towards mixes c toward a target in small steps until ok holds, and says
// how far it went: 0 is c itself, 1 is the target.
func towards(c, target color.NRGBA, ok func(color.NRGBA) bool) (color.NRGBA, float64) {
	const steps = 100
	for i := 0; i <= steps; i++ {
		t := float64(i) / steps
		if x := mix(c, target, t); ok(x) {
			return x, t
		}
	}
	return target, 1
}

// mix is the colour t of the way from a to b.
func mix(a, b color.NRGBA, t float64) color.NRGBA {
	at := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return color.NRGBA{R: at(a.R, b.R), G: at(a.G, b.G), B: at(a.B, b.B), A: 0xFF}
}

// luminance is WCAG's relative luminance.
func luminance(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// contrast is WCAG's contrast ratio between two colours.
func contrast(a, b color.NRGBA) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
