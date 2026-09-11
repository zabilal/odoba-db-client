package theme

import "image/color"

// FolderColor is the colour a connection folder is marked in (FR-1.6,
// ADR-0025): an accent, by its name in the settings file, in the shade the
// palette uses for the accent as text, which reads on the window in the
// appearance given. False for a folder with no colour, or one whose name no
// accent has.
func FolderColor(name string, dark bool) (color.NRGBA, bool) {
	for _, a := range Accents() {
		if a.String() == name {
			return accentPalette(a, dark).AccentText, true
		}
	}
	return color.NRGBA{}, false
}
