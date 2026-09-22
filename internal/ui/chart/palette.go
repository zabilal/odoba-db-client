package chart

import "image/color"

// The colours series are drawn in.
//
// Okabe and Ito's qualitative set, which was chosen so that every pair of
// its colours can be told apart by the common forms of colour blindness.
// The application's accent is deliberately not used: an accent means "this
// is selected" everywhere else in the window, and a series is not selected.
//
// Colour is never the only thing telling two series apart — the key names
// them, and a hovered point says which series it is in — because about one
// man in twelve cannot rely on it (NFR-A1).
var seriesLight = []color.NRGBA{
	{0x00, 0x72, 0xB2, 0xFF}, // blue
	{0xD5, 0x5E, 0x00, 0xFF}, // vermillion
	{0x00, 0x9E, 0x73, 0xFF}, // bluish green
	{0xCC, 0x79, 0xA7, 0xFF}, // reddish purple
	{0xE6, 0x9F, 0x00, 0xFF}, // orange
	{0x56, 0xB4, 0xE9, 0xFF}, // sky blue
	{0x7A, 0x60, 0x00, 0xFF}, // dark yellow
	{0x44, 0x44, 0x48, 0xFF}, // grey
}

// seriesDark is the same set against a dark background.
//
// Okabe and Ito chose theirs for paper. Two of them — the blue and the black
// that ends the set — disappear on a dark window, so those are lightened and
// the rest are left where they are, which keeps the pairs distinguishable.
var seriesDark = []color.NRGBA{
	{0x3F, 0xA9, 0xF5, 0xFF}, // blue
	{0xFF, 0x7F, 0x3F, 0xFF}, // vermillion
	{0x2F, 0xC7, 0x9A, 0xFF}, // bluish green
	{0xE3, 0x9B, 0xC6, 0xFF}, // reddish purple
	{0xF0, 0xB4, 0x29, 0xFF}, // orange
	{0x9F, 0xD8, 0xF7, 0xFF}, // sky blue
	{0xE8, 0xD4, 0x4D, 0xFF}, // yellow
	{0xB8, 0xB8, 0xBE, 0xFF}, // grey
}

// SeriesColours is the set to draw series in, for a light or a dark window.
func SeriesColours(dark bool) []color.NRGBA {
	if dark {
		return seriesDark
	}
	return seriesLight
}
