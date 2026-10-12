package theme

import "image/color"

// A heat map's colours (FR-3.17).
//
// A column of numbers shaded by how large each is: the smallest left as the
// grid is, the largest tinted with the accent, the rest between. The number
// is still printed in every cell, so the colour says nothing the text does
// not — it lets an eye find the large ones without reading them all, and a
// person who cannot tell the tints apart has lost a convenience and no
// information.
//
// The text has to stay readable on every tint, and where a stronger colour
// would make a better picture and worse text, the text wins: heatStrength is
// how far towards the accent the hottest cell goes — far enough to be seen,
// and on every accent, light and dark, short of where a label on it would
// fall under AA contrast. A test holds each of them to that.
const heatStrength = 0.35

// Heat is the fill of a cell whose value is frac of the way from its column's
// smallest to its largest. Out of that range is held to its ends.
func (p Palette) Heat(frac float64) color.NRGBA {
	switch {
	case frac != frac, frac < 0: // not a number is not anywhere along it
		frac = 0
	case frac > 1:
		frac = 1
	}
	return mix(p.ContentBackground, p.ControlAccent, frac*heatStrength)
}
