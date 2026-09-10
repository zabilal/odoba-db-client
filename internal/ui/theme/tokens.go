// Package theme defines Ikigai DB's visual identity, following Apple's macOS
// Human Interface Guidelines.
//
// UX principle 12 makes this a Phase 0 deliverable rather than end-stage
// polish. Fyne's stock theme is Material-derived, and retrofitting a visual
// identity onto a built application is where projects of this kind usually fail
// aesthetically (RISK-6).
//
// tokens.go deliberately imports no Fyne. Tokens are plain colours and numbers,
// so the WCAG suite in contrast_test.go runs headlessly and fails the build on
// a regression (NFR-A2). theme.go adapts them to fyne.Theme.
//
// # Where HIG and WCAG disagree
//
// Apple's neutral label and separator colours are defined as low-alpha blacks
// and whites, and several of them do not meet WCAG 2.1 AA:
//
//	secondaryLabelColor   50% black on white  = 3.55:1  (AA needs 4.5:1)
//	tertiaryLabelColor    26% black on white  = 1.87:1
//	separatorColor        10% black on white  = 1.20:1
//
// NFR-A2 commits us to AA, so the neutrals here are darkened until they pass
// while keeping HIG's hue, structure and hierarchy. Every such departure is
// marked "HIG-DEVIATION" below. Apple's chromatic system colours are used
// unmodified: they already pass at the sizes we draw them.
package theme

import "image/color"

// --- Apple system colours ---------------------------------------------------
//
// The macOS semantic palette, sRGB, taken from Apple's published values. These
// are used verbatim: they are what makes the application read as macOS-native
// rather than as a generic desktop app.

var (
	SystemBlue   = rgb(0x00, 0x7A, 0xFF)
	SystemGreen  = rgb(0x28, 0xCD, 0x41)
	SystemIndigo = rgb(0x58, 0x56, 0xD6)
	SystemOrange = rgb(0xFF, 0x95, 0x00)
	SystemPink   = rgb(0xFF, 0x2D, 0x55)
	SystemPurple = rgb(0xAF, 0x52, 0xDE)
	SystemRed    = rgb(0xFF, 0x3B, 0x30)
	SystemTeal   = rgb(0x59, 0xAD, 0xC4)
	SystemYellow = rgb(0xFF, 0xCC, 0x00)
	SystemGray   = rgb(0x8E, 0x8E, 0x93)

	SystemBlueDark   = rgb(0x0A, 0x84, 0xFF)
	SystemGreenDark  = rgb(0x32, 0xD7, 0x4B)
	SystemIndigoDark = rgb(0x5E, 0x5C, 0xE6)
	SystemOrangeDark = rgb(0xFF, 0x9F, 0x0A)
	SystemPinkDark   = rgb(0xFF, 0x37, 0x5F)
	SystemPurpleDark = rgb(0xBF, 0x5A, 0xF2)
	SystemRedDark    = rgb(0xFF, 0x45, 0x3A)
	SystemTealDark   = rgb(0x6A, 0xC4, 0xDC)
	SystemYellowDark = rgb(0xFF, 0xD6, 0x0A)
	SystemGrayDark   = rgb(0x98, 0x98, 0x9D)
)

// Palette is the complete set of colour roles, named after macOS's semantic
// colours so the mapping to HIG stays legible. Every colour the UI draws comes
// from here; no widget hardcodes a value.
type Palette struct {
	// Backgrounds follow macOS's window/content/sidebar hierarchy rather than
	// Material's elevation model. macOS conveys depth through distinct surface
	// tones and hairline separators, not drop shadows.
	WindowBackground   color.NRGBA // window chrome, toolbars
	ContentBackground  color.NRGBA // the grid, the editor — where data lives
	SidebarBackground  color.NRGBA // source list; vibrancy stand-in
	ElevatedBackground color.NRGBA // popovers, menus, sheets
	ControlBackground  color.NRGBA // text fields, wells

	// AlternateRow is macOS's alternating table row tint. Used at low
	// frequency in the grid: it aids row tracking across wide tables.
	AlternateRow color.NRGBA

	Hover color.NRGBA

	// macOS distinguishes selection in the focused window from selection in an
	// unfocused one. Honouring that is a small thing that reads as genuinely
	// native rather than approximately native.
	SelectedEmphasized   color.NRGBA
	SelectedUnemphasized color.NRGBA
	OnSelectedEmphasized color.NRGBA

	// RangeSelection is the grid's multi-cell selection tint, lighter than a
	// row selection so the two remain distinguishable.
	RangeSelection color.NRGBA

	// Label hierarchy, mirroring macOS's four levels.
	Label           color.NRGBA
	SecondaryLabel  color.NRGBA
	TertiaryLabel   color.NRGBA
	QuaternaryLabel color.NRGBA

	// Separator is HIG's hairline. OpaqueSeparator and ControlBorder are the
	// heavier lines used where a boundary must be identifiable (WCAG 1.4.11).
	Separator       color.NRGBA
	OpaqueSeparator color.NRGBA
	ControlBorder   color.NRGBA

	// ControlAccent is the user's system accent colour; macOS lets people
	// change it in System Settings and expects apps to follow. Blue is the
	// default. Following the live system value is FR-15.3 follow-up work.
	ControlAccent        color.NRGBA
	ControlAccentHover   color.NRGBA
	ControlAccentPressed color.NRGBA
	ControlAccentSubtle  color.NRGBA
	OnControlAccent      color.NRGBA

	// AccentText is the accent used AS text — links, active tab labels.
	// macOS has exactly this split: linkColor is a darker blue than
	// systemBlue, because systemBlue is tuned for fills and large glyphs and
	// measures only 4.02:1 on white. Using one blue for both is the most
	// common way an otherwise-accessible palette fails.
	AccentText color.NRGBA

	// Status, drawn from the system palette.
	Danger        color.NRGBA
	DangerSubtle  color.NRGBA
	Warning       color.NRGBA
	WarningSubtle color.NRGBA
	Success       color.NRGBA
	SuccessSubtle color.NRGBA

	// Null is a data colour, not a label colour. HIG would render it as a
	// tertiary label, but tertiary fails AA and a NULL is information the user
	// must be able to read, so it is held to the text bar (UX principle 7).
	Null color.NRGBA

	// Changeset states (FR-4.3). Green/amber/red follow the system palette;
	// the tints are pulled apart far enough to be told apart at a glance,
	// because that judgement happens at the moment before a commit.
	AddedFg    color.NRGBA
	AddedBg    color.NRGBA
	ModifiedFg color.NRGBA
	ModifiedBg color.NRGBA
	DeletedFg  color.NRGBA
	DeletedBg  color.NRGBA

	// Chrome.
	FocusRing      color.NRGBA
	Scrollbar      color.NRGBA
	ScrollbarHover color.NRGBA
	Shadow         color.NRGBA
}

// Light is the macOS light appearance.
var Light = Palette{
	WindowBackground:   rgb(0xEC, 0xEC, 0xEC),
	ContentBackground:  rgb(0xFF, 0xFF, 0xFF),
	SidebarBackground:  rgb(0xF5, 0xF5, 0xF7),
	ElevatedBackground: rgb(0xFF, 0xFF, 0xFF),
	ControlBackground:  rgb(0xFF, 0xFF, 0xFF),

	AlternateRow: rgb(0xF5, 0xF5, 0xF7),
	Hover:        rgb(0xE8, 0xE8, 0xED),

	SelectedEmphasized:   rgb(0x00, 0x63, 0xE1),
	SelectedUnemphasized: rgb(0xDC, 0xDC, 0xE0),
	OnSelectedEmphasized: rgb(0xFF, 0xFF, 0xFF),
	RangeSelection:       rgb(0xD6, 0xE4, 0xFB),

	// HIG-DEVIATION: Apple's label alphas (85/50/26/10% black) fail AA below
	// the first level. Hue and hierarchy are kept; values are darkened.
	Label:           rgb(0x1D, 0x1D, 0x1F),
	SecondaryLabel:  rgb(0x5E, 0x5E, 0x63),
	TertiaryLabel:   rgb(0x8E, 0x8E, 0x93),
	QuaternaryLabel: rgb(0xB4, 0xB4, 0xB9),

	// HIG-DEVIATION: separatorColor (10% black) is 1.2:1. Kept as the
	// decorative hairline, which WCAG exempts; ControlBorder carries the
	// 3:1 duty for boundaries that identify a control.
	Separator:       rgb(0xDC, 0xDC, 0xE0),
	OpaqueSeparator: rgb(0xC6, 0xC6, 0xC8),
	ControlBorder:   rgb(0x86, 0x86, 0x8A),

	// HIG-DEVIATION: the fill is macOS's selectedContentBackgroundColor
	// rather than systemBlue, because white on systemBlue is 4.02:1. This is
	// the blue macOS actually fills selected rows and default buttons with.
	ControlAccent:        rgb(0x00, 0x63, 0xE1),
	ControlAccentHover:   rgb(0x00, 0x56, 0xC4),
	ControlAccentPressed: rgb(0x00, 0x4A, 0xA8),
	ControlAccentSubtle:  rgb(0xE8, 0xF1, 0xFE),
	OnControlAccent:      rgb(0xFF, 0xFF, 0xFF),
	AccentText:           rgb(0x00, 0x60, 0xCE),

	// HIG-DEVIATION: systemRed/Orange/Green are tuned for large glyphs and
	// fills. As 13pt body text on white they fall below AA, so the text-weight
	// variants here are darkened while the fills keep Apple's values.
	Danger:        rgb(0xC4, 0x1E, 0x18),
	DangerSubtle:  rgb(0xFF, 0xE5, 0xE5),
	Warning:       rgb(0x8A, 0x51, 0x00),
	WarningSubtle: rgb(0xFF, 0xF3, 0xD6),
	Success:       rgb(0x17, 0x70, 0x2A),
	SuccessSubtle: rgb(0xE3, 0xF7, 0xE8),

	Null: rgb(0x5E, 0x5E, 0x63),

	AddedFg:    rgb(0x17, 0x70, 0x2A),
	AddedBg:    rgb(0xE3, 0xF7, 0xE8),
	ModifiedFg: rgb(0x8A, 0x51, 0x00),
	ModifiedBg: rgb(0xFF, 0xF3, 0xD6),
	DeletedFg:  rgb(0xC4, 0x1E, 0x18),
	DeletedBg:  rgb(0xFF, 0xE5, 0xE5),

	FocusRing:      rgba(0x00, 0x7A, 0xFF, 0x80),
	Scrollbar:      rgba(0x00, 0x00, 0x00, 0x40),
	ScrollbarHover: rgba(0x00, 0x00, 0x00, 0x66),
	Shadow:         rgba(0x00, 0x00, 0x00, 0x26),
}

// Dark is the macOS dark appearance.
//
// macOS dark mode is mid-grey, not black: window chrome sits around #323232 and
// content wells drop to #1E1E1E. Reproducing that relationship is most of what
// makes a dark theme read as macOS rather than as a generic dark theme.
var Dark = Palette{
	WindowBackground:   rgb(0x32, 0x32, 0x32),
	ContentBackground:  rgb(0x1E, 0x1E, 0x1E),
	SidebarBackground:  rgb(0x2A, 0x2A, 0x2C),
	ElevatedBackground: rgb(0x3A, 0x3A, 0x3C),
	ControlBackground:  rgb(0x1C, 0x1C, 0x1E),

	AlternateRow: rgb(0x25, 0x25, 0x27),
	Hover:        rgb(0x3A, 0x3A, 0x3C),

	SelectedEmphasized:   rgb(0x0A, 0x63, 0xCE),
	SelectedUnemphasized: rgb(0x46, 0x46, 0x48),
	OnSelectedEmphasized: rgb(0xFF, 0xFF, 0xFF),
	RangeSelection:       rgb(0x1E, 0x36, 0x57),

	Label:           rgb(0xF5, 0xF5, 0xF7),
	SecondaryLabel:  rgb(0xAE, 0xAE, 0xB2),
	TertiaryLabel:   rgb(0x8E, 0x8E, 0x93),
	QuaternaryLabel: rgb(0x63, 0x63, 0x67),

	Separator:       rgb(0x3A, 0x3A, 0x3C),
	OpaqueSeparator: rgb(0x48, 0x48, 0x4A),
	ControlBorder:   rgb(0x8E, 0x8E, 0x93),

	ControlAccent:        rgb(0x0A, 0x63, 0xCE),
	ControlAccentHover:   rgb(0x1B, 0x72, 0xDD),
	ControlAccentPressed: rgb(0x08, 0x54, 0xB0),
	ControlAccentSubtle:  rgb(0x1B, 0x2B, 0x3D),
	OnControlAccent:      rgb(0xFF, 0xFF, 0xFF),
	// Lightened rather than darkened: on dark surfaces the accent must rise
	// to meet contrast, which is the mirror of what light mode needs.
	AccentText: rgb(0x4D, 0xA3, 0xFF),

	Danger:        rgb(0xFF, 0x6B, 0x60),
	DangerSubtle:  rgb(0x3A, 0x1E, 0x1E),
	Warning:       rgb(0xFF, 0xB3, 0x30),
	WarningSubtle: rgb(0x33, 0x28, 0x0E),
	Success:       rgb(0x4C, 0xD9, 0x64),
	SuccessSubtle: rgb(0x16, 0x32, 0x1F),

	Null: rgb(0xAE, 0xAE, 0xB2),

	AddedFg:    rgb(0x4C, 0xD9, 0x64),
	AddedBg:    rgb(0x16, 0x32, 0x1F),
	ModifiedFg: rgb(0xFF, 0xB3, 0x30),
	ModifiedBg: rgb(0x33, 0x28, 0x0E),
	DeletedFg:  rgb(0xFF, 0x6B, 0x60),
	DeletedBg:  rgb(0x3A, 0x1E, 0x1E),

	FocusRing:      rgba(0x0A, 0x84, 0xFF, 0x99),
	Scrollbar:      rgba(0xFF, 0xFF, 0xFF, 0x40),
	ScrollbarHover: rgba(0xFF, 0xFF, 0xFF, 0x66),
	Shadow:         rgba(0x00, 0x00, 0x00, 0x80),
}

// --- Metrics ----------------------------------------------------------------

// Spacing follows macOS's 4pt rhythm. HIG's standard control spacing is 8pt,
// with 20pt window margins; a data tool runs tighter than a settings pane, so
// the scale starts smaller while keeping the same rhythm.
const (
	Space2XS = float32(2)
	SpaceXS  = float32(4)
	SpaceSM  = float32(6)
	SpaceMD  = float32(8)
	SpaceLG  = float32(12)
	SpaceXL  = float32(16)
	Space2XL = float32(20)
)

// Type scale, from HIG's macOS text styles. Body is 13pt — the standard macOS
// control size, and already the right density for a data tool.
const (
	TextCaption    = float32(10)
	TextFootnote   = float32(11)
	TextCallout    = float32(12)
	TextBody       = float32(13)
	TextTitle3     = float32(15)
	TextTitle2     = float32(17)
	TextTitle1     = float32(22)
	TextLargeTitle = float32(26)
)

// Corner radii, from HIG. macOS controls are 6pt at regular size; popovers and
// sheets are 10pt. Material's pill shapes read as consumer software.
const (
	RadiusControl = float32(6)
	RadiusSmall   = float32(4)
	RadiusPopover = float32(10)
	RadiusWindow  = float32(10)
)

// Structural sizes.
const (
	BorderWidth    = float32(1)
	FocusWidth     = float32(3) // HIG's focus ring is a 3pt glow
	SeparatorWidth = float32(1)
	ScrollbarSize  = float32(9) // macOS overlay scrollers are narrow
	IconInline     = float32(16)

	// RowHeight matches macOS's compact table row. It sets how much data fits
	// on screen, which is the most consequential number in the product.
	RowHeight     = float32(24)
	HeaderHeight  = float32(28)
	ToolbarHeight = float32(38)
)

func rgb(r, g, b uint8) color.NRGBA {
	return color.NRGBA{R: r, G: g, B: b, A: 0xFF}
}

func rgba(r, g, b, a uint8) color.NRGBA {
	return color.NRGBA{R: r, G: g, B: b, A: a}
}
