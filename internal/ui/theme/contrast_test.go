package theme

import (
	"image/color"
	"math"
	"testing"
)

// NFR-A2 requires WCAG 2.1 AA contrast in both themes. Verifying that by eye
// is verification that decays: a palette tweak six months from now silently
// breaks a pair nobody re-checks. These tests are the authority on the palette,
// and a failure here is a real defect, not a style opinion.
//
// AA thresholds:
//   4.5:1  normal text
//   3.0:1  large text (>=18pt, or >=14pt bold) and UI component boundaries
//
// WCAG 1.4.3 exempts disabled controls, so TextDisabled is held to a lower
// quality floor rather than the AA bar.

const (
	aaText  = 4.5
	aaLarge = 3.0
)

// relativeLuminance implements WCAG 2.1's definition.
func relativeLuminance(c color.NRGBA) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255.0
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// contrastRatio returns the WCAG contrast ratio between two opaque colours.
func contrastRatio(a, b color.NRGBA) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

type pair struct {
	name string
	fg   func(Palette) color.NRGBA
	bg   func(Palette) color.NRGBA
	min  float64
}

// textPairs enumerates every foreground/background combination the UI actually
// draws. Adding a colour role to Palette without adding it here is how a gap
// gets in.
var textPairs = []pair{
	// The label hierarchy on every surface it can land on.
	{"Label/Content", fLabel, fContent, aaText},
	{"Label/Window", fLabel, fWindow, aaText},
	{"Label/Sidebar", fLabel, fSidebar, aaText},
	{"Label/Elevated", fLabel, fElevated, aaText},
	{"Label/Control", fLabel, fControl, aaText},
	{"Label/AlternateRow", fLabel, fAltRow, aaText},
	{"Label/Hover", fLabel, fHover, aaText},
	{"Label/SelectedUnemph", fLabel, fSelUnemph, aaText},
	{"Label/RangeSelection", fLabel, fRangeSel, aaText},

	// Secondary labels carry real information — column types, row counts,
	// timestamps — so they are held to the full text bar.
	{"SecondaryLabel/Content", fSecondary, fContent, aaText},
	{"SecondaryLabel/Window", fSecondary, fWindow, aaText},
	{"SecondaryLabel/Sidebar", fSecondary, fSidebar, aaText},
	{"SecondaryLabel/AlternateRow", fSecondary, fAltRow, aaText},

	// NULL is data (UX principle 7). It must be legible on every row state.
	{"Null/Content", fNull, fContent, aaText},
	{"Null/AlternateRow", fNull, fAltRow, aaText},
	{"Null/Hover", fNull, fHover, aaText},
	{"Null/RangeSelection", fNull, fRangeSel, aaText},

	// Selection: macOS fills the row with the accent and inverts the label.
	{"OnSelected/SelectedEmph", fOnSelEmph, fSelEmph, aaText},

	// Accent as text, and text on accent fills.
	{"AccentText/Content", fAccentText, fContent, aaText},
	{"AccentText/Window", fAccentText, fWindow, aaText},
	{"AccentText/AccentSubtle", fAccentText, fAccentSubtle, aaText},
	{"OnAccent/Accent", fOnAccent, fAccent, aaText},
	{"OnAccent/AccentHover", fOnAccent, fAccentHover, aaText},

	// Status text on plain and tinted backgrounds.
	{"Danger/Content", fDanger, fContent, aaText},
	{"Danger/Window", fDanger, fWindow, aaText},
	{"Danger/Sidebar", fDanger, fSidebar, aaText}, // a filter that failed, in the grid header
	{"Danger/DangerSubtle", fDanger, fDangerSubtle, aaText},
	{"Label/DangerSubtle", fLabel, fDangerSubtle, aaText}, // the error bar's message
	{"Warning/Content", fWarning, fContent, aaText},
	{"Warning/Window", fWarning, fWindow, aaText},
	{"Warning/WarningSubtle", fWarning, fWarningSubtle, aaText},
	{"Success/Content", fSuccess, fContent, aaText},
	{"Success/Window", fSuccess, fWindow, aaText},
	{"Success/SuccessSubtle", fSuccess, fSuccessSubtle, aaText},

	// Changeset states (FR-4.3): a user reviewing pending edits must be able
	// to read the value inside a tinted row.
	{"AddedFg/AddedBg", fAddedFg, fAddedBg, aaText},
	{"ModifiedFg/ModifiedBg", fModifiedFg, fModifiedBg, aaText},
	{"DeletedFg/DeletedBg", fDeletedFg, fDeletedBg, aaText},
	{"Label/AddedBg", fLabel, fAddedBg, aaText},
	{"Label/ModifiedBg", fLabel, fModifiedBg, aaText},
	{"Label/DeletedBg", fLabel, fDeletedBg, aaText},
	// Editor syntax (FR-5.1) on the editor, its current-line tint and its
	// selection. Code is read as closely as data, so it gets the text bar.
	{"SyntaxKeyword/Content", fSynKeyword, fContent, aaText},
	{"SyntaxKeyword/AlternateRow", fSynKeyword, fAltRow, aaText},
	{"SyntaxKeyword/RangeSelection", fSynKeyword, fRangeSel, aaText},
	{"SyntaxType/Content", fSynType, fContent, aaText},
	{"SyntaxType/AlternateRow", fSynType, fAltRow, aaText},
	{"SyntaxType/RangeSelection", fSynType, fRangeSel, aaText},
	{"SyntaxFunction/Content", fSynFunction, fContent, aaText},
	{"SyntaxFunction/AlternateRow", fSynFunction, fAltRow, aaText},
	{"SyntaxFunction/RangeSelection", fSynFunction, fRangeSel, aaText},
	{"SyntaxString/Content", fSynString, fContent, aaText},
	{"SyntaxString/AlternateRow", fSynString, fAltRow, aaText},
	{"SyntaxString/RangeSelection", fSynString, fRangeSel, aaText},
	{"SyntaxNumber/Content", fSynNumber, fContent, aaText},
	{"SyntaxNumber/AlternateRow", fSynNumber, fAltRow, aaText},
	{"SyntaxNumber/RangeSelection", fSynNumber, fRangeSel, aaText},
	{"SyntaxComment/Content", fSynComment, fContent, aaText},
	{"SyntaxComment/AlternateRow", fSynComment, fAltRow, aaText},
	{"SyntaxComment/RangeSelection", fSynComment, fRangeSel, aaText},
	{"SyntaxParameter/Content", fSynParameter, fContent, aaText},
	{"SyntaxParameter/AlternateRow", fSynParameter, fAltRow, aaText},
	{"SyntaxParameter/RangeSelection", fSynParameter, fRangeSel, aaText},
}

// componentPairs are UI boundaries, held to the 3:1 bar of WCAG 1.4.11.
//
// Separator is deliberately absent: it is HIG's decorative hairline, which
// WCAG exempts. ControlBorder is the line that identifies a control, and it
// carries the requirement.
var componentPairs = []pair{
	{"ControlBorder/Content", fControlBorder, fContent, aaLarge},
	{"ControlBorder/Window", fControlBorder, fWindow, aaLarge},
	{"ControlBorder/Sidebar", fControlBorder, fSidebar, aaLarge},
	{"ControlBorder/Control", fControlBorder, fControl, aaLarge},
	// The line under the selected tab, and the drop mark (internal/ui/tabbar):
	// in the accent's text colour, since the plain accent is 2.99:1 in dark.
	{"AccentText/Control", fAccentText, fControl, aaLarge},
	{"AccentText/Window", fAccentText, fWindow, aaLarge},
}

func TestPaletteContrastAA(t *testing.T) {
	for _, p := range []struct {
		name string
		pal  Palette
	}{{"Light", Light}, {"Dark", Dark}} {
		t.Run(p.name, func(t *testing.T) {
			for _, group := range [][]pair{textPairs, componentPairs} {
				for _, c := range group {
					got := contrastRatio(c.fg(p.pal), c.bg(p.pal))
					if got < c.min {
						t.Errorf("%-30s %.2f:1  (need %.1f:1)  fg=%s bg=%s",
							c.name, got, c.min, hex(c.fg(p.pal)), hex(c.bg(p.pal)))
					}
				}
			}
		})
	}
}

func TestLabelHierarchyIsOrdered(t *testing.T) {
	// HIG's four label levels must actually descend in prominence, or the
	// hierarchy conveys nothing. Tertiary and quaternary are placeholder and
	// disabled tiers, which WCAG 1.4.3 exempts, so they are checked for
	// ordering rather than against the AA bar.
	for _, p := range []struct {
		name string
		pal  Palette
	}{{"Light", Light}, {"Dark", Dark}} {
		bg := p.pal.ContentBackground
		levels := []struct {
			name string
			c    color.NRGBA
		}{
			{"Label", p.pal.Label},
			{"SecondaryLabel", p.pal.SecondaryLabel},
			{"TertiaryLabel", p.pal.TertiaryLabel},
			{"QuaternaryLabel", p.pal.QuaternaryLabel},
		}
		for i := 0; i+1 < len(levels); i++ {
			hi := contrastRatio(levels[i].c, bg)
			lo := contrastRatio(levels[i+1].c, bg)
			if lo >= hi {
				t.Errorf("%s: %s (%.2f:1) is not weaker than %s (%.2f:1)",
					p.name, levels[i+1].name, lo, levels[i].name, hi)
			}
		}
		// Quaternary is the faintest tier macOS draws, but it should still be
		// perceptible rather than read as a rendering artifact.
		if r := contrastRatio(p.pal.QuaternaryLabel, bg); r < 1.6 {
			t.Errorf("%s: QuaternaryLabel %.2f:1 is below the perceptibility floor", p.name, r)
		}
	}
}

func TestNullIsDistinguishableFromRealText(t *testing.T) {
	// UX principle 7: NULL must never be confused with a value. Legibility is
	// necessary but not sufficient — it must also differ visibly from ordinary
	// text, or "NULL" reads as the literal string.
	for _, p := range []struct {
		name string
		pal  Palette
	}{{"Light", Light}, {"Dark", Dark}} {
		if r := contrastRatio(p.pal.Null, p.pal.Label); r < 1.8 {
			t.Errorf("%s: Null is too close to Label (%.2f:1); a NULL would read as a value",
				p.name, r)
		}
	}
}

func TestChangesetStatesAreMutuallyDistinct(t *testing.T) {
	// FR-4.3 marks added, modified and deleted rows. If two tints are close, a
	// user cannot tell an insert from a delete before committing — which is
	// exactly the moment it matters.
	for _, p := range []struct {
		name string
		pal  Palette
	}{{"Light", Light}, {"Dark", Dark}} {
		bgs := []struct {
			name string
			c    color.NRGBA
		}{
			{"Added", p.pal.AddedBg}, {"Modified", p.pal.ModifiedBg}, {"Deleted", p.pal.DeletedBg},
		}
		for i := 0; i < len(bgs); i++ {
			for j := i + 1; j < len(bgs); j++ {
				if d := channelDistance(bgs[i].c, bgs[j].c); d < 12 {
					t.Errorf("%s: %sBg and %sBg differ by only %d; too close to tell apart",
						p.name, bgs[i].name, bgs[j].name, d)
				}
			}
		}
	}
}

func TestSurfaceHierarchyReadsWithoutShadows(t *testing.T) {
	// macOS conveys depth through distinct surface tones and hairlines rather
	// than drop shadows, so each surface must differ from its neighbour.
	for _, p := range []struct {
		name string
		pal  Palette
	}{{"Light", Light}, {"Dark", Dark}} {
		pairs := []struct{ a, b, an, bn string }{}
		_ = pairs
		checks := []struct {
			an, bn string
			a, b   color.NRGBA
		}{
			{"WindowBackground", "ContentBackground", p.pal.WindowBackground, p.pal.ContentBackground},
			{"SidebarBackground", "ContentBackground", p.pal.SidebarBackground, p.pal.ContentBackground},
			{"ContentBackground", "AlternateRow", p.pal.ContentBackground, p.pal.AlternateRow},
			{"ContentBackground", "Hover", p.pal.ContentBackground, p.pal.Hover},
		}
		for _, c := range checks {
			if c.a == c.b {
				t.Errorf("%s: %s and %s are identical", p.name, c.an, c.bn)
			}
		}
	}
}

func TestSelectionStatesAreDistinct(t *testing.T) {
	// macOS distinguishes selection in a focused window from selection in an
	// unfocused one. If the two look the same, the distinction is decoration.
	for _, p := range []struct {
		name string
		pal  Palette
	}{{"Light", Light}, {"Dark", Dark}} {
		if d := channelDistance(p.pal.SelectedEmphasized, p.pal.SelectedUnemphasized); d < 30 {
			t.Errorf("%s: emphasized and unemphasized selection differ by only %d", p.name, d)
		}
		if d := channelDistance(p.pal.SelectedEmphasized, p.pal.RangeSelection); d < 20 {
			t.Errorf("%s: row selection and range selection differ by only %d", p.name, d)
		}
	}
}

func TestSystemColoursMatchApple(t *testing.T) {
	// These are Apple's published sRGB values. Drifting from them is how an
	// application stops reading as macOS-native, so they are pinned. The
	// palette's text-weight variants are allowed to differ (see HIG-DEVIATION
	// notes in tokens.go); these constants are not.
	want := map[string]struct {
		got  color.NRGBA
		want string
	}{
		"SystemBlue":      {SystemBlue, "#007AFF"},
		"SystemGreen":     {SystemGreen, "#28CD41"},
		"SystemIndigo":    {SystemIndigo, "#5856D6"},
		"SystemOrange":    {SystemOrange, "#FF9500"},
		"SystemPink":      {SystemPink, "#FF2D55"},
		"SystemPurple":    {SystemPurple, "#AF52DE"},
		"SystemRed":       {SystemRed, "#FF3B30"},
		"SystemYellow":    {SystemYellow, "#FFCC00"},
		"SystemGray":      {SystemGray, "#8E8E93"},
		"SystemBlueDark":  {SystemBlueDark, "#0A84FF"},
		"SystemRedDark":   {SystemRedDark, "#FF453A"},
		"SystemGreenDark": {SystemGreenDark, "#32D74B"},
	}
	for name, c := range want {
		if got := hex(c.got); got != c.want {
			t.Errorf("%s = %s, want Apple's %s", name, got, c.want)
		}
	}
}

// channelDistance is the maximum per-channel difference, a cheap proxy for
// "can a person tell these apart at a glance".
func channelDistance(a, b color.NRGBA) int {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	m := d(a.R, b.R)
	if v := d(a.G, b.G); v > m {
		m = v
	}
	if v := d(a.B, b.B); v > m {
		m = v
	}
	return m
}

func hex(c color.NRGBA) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{
		'#', digits[c.R>>4], digits[c.R&0xF],
		digits[c.G>>4], digits[c.G&0xF],
		digits[c.B>>4], digits[c.B&0xF],
	})
}

// Accessors keep the pair tables readable.
func fWindow(p Palette) color.NRGBA        { return p.WindowBackground }
func fContent(p Palette) color.NRGBA       { return p.ContentBackground }
func fSidebar(p Palette) color.NRGBA       { return p.SidebarBackground }
func fElevated(p Palette) color.NRGBA      { return p.ElevatedBackground }
func fControl(p Palette) color.NRGBA       { return p.ControlBackground }
func fAltRow(p Palette) color.NRGBA        { return p.AlternateRow }
func fHover(p Palette) color.NRGBA         { return p.Hover }
func fSelEmph(p Palette) color.NRGBA       { return p.SelectedEmphasized }
func fSelUnemph(p Palette) color.NRGBA     { return p.SelectedUnemphasized }
func fOnSelEmph(p Palette) color.NRGBA     { return p.OnSelectedEmphasized }
func fRangeSel(p Palette) color.NRGBA      { return p.RangeSelection }
func fLabel(p Palette) color.NRGBA         { return p.Label }
func fSecondary(p Palette) color.NRGBA     { return p.SecondaryLabel }
func fNull(p Palette) color.NRGBA          { return p.Null }
func fAccent(p Palette) color.NRGBA        { return p.ControlAccent }
func fAccentText(p Palette) color.NRGBA    { return p.AccentText }
func fAccentHover(p Palette) color.NRGBA   { return p.ControlAccentHover }
func fAccentSubtle(p Palette) color.NRGBA  { return p.ControlAccentSubtle }
func fOnAccent(p Palette) color.NRGBA      { return p.OnControlAccent }
func fDanger(p Palette) color.NRGBA        { return p.Danger }
func fDangerSubtle(p Palette) color.NRGBA  { return p.DangerSubtle }
func fWarning(p Palette) color.NRGBA       { return p.Warning }
func fWarningSubtle(p Palette) color.NRGBA { return p.WarningSubtle }
func fSuccess(p Palette) color.NRGBA       { return p.Success }
func fSuccessSubtle(p Palette) color.NRGBA { return p.SuccessSubtle }
func fAddedFg(p Palette) color.NRGBA       { return p.AddedFg }
func fAddedBg(p Palette) color.NRGBA       { return p.AddedBg }
func fModifiedFg(p Palette) color.NRGBA    { return p.ModifiedFg }
func fModifiedBg(p Palette) color.NRGBA    { return p.ModifiedBg }
func fDeletedFg(p Palette) color.NRGBA     { return p.DeletedFg }
func fDeletedBg(p Palette) color.NRGBA     { return p.DeletedBg }
func fControlBorder(p Palette) color.NRGBA { return p.ControlBorder }

func fSynKeyword(p Palette) color.NRGBA   { return p.SyntaxKeyword }
func fSynType(p Palette) color.NRGBA      { return p.SyntaxType }
func fSynFunction(p Palette) color.NRGBA  { return p.SyntaxFunction }
func fSynString(p Palette) color.NRGBA    { return p.SyntaxString }
func fSynNumber(p Palette) color.NRGBA    { return p.SyntaxNumber }
func fSynComment(p Palette) color.NRGBA   { return p.SyntaxComment }
func fSynParameter(p Palette) color.NRGBA { return p.SyntaxParameter }
