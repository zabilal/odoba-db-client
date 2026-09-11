package theme

import (
	"image/color"

	"fyne.io/fyne/v2"
	ftheme "fyne.io/fyne/v2/theme"
)

// Theme adapts the macOS palette in tokens.go to Fyne's theming interface.
//
// Fyne's colour names are Material-derived, so several map imperfectly onto
// macOS concepts. Where they do, the mapping is chosen for how the widget is
// actually drawn rather than for what the name suggests — ColorNameBackground
// becomes the window chrome, not the content well, because that is what Fyne
// paints with it.
type Theme struct {
	// Appearance pins the theme to light or dark. Zero follows the OS, which
	// is what FR-15.3 requires by default.
	Appearance Appearance
	// Accent is the colour controls and the selection take. Zero is blue.
	Accent Accent
}

// Appearance selects light or dark, or defers to the operating system.
type Appearance uint8

const (
	AppearanceSystem Appearance = iota
	AppearanceLight
	AppearanceDark
)

var _ fyne.Theme = (*Theme)(nil)

// New returns a theme following the system appearance.
func New() *Theme { return &Theme{} }

// PaletteFor returns the palette for a Fyne variant, honouring an explicit
// Appearance override and wearing the Accent.
func (t *Theme) PaletteFor(v fyne.ThemeVariant) Palette {
	dark := v == ftheme.VariantDark
	switch t.Appearance {
	case AppearanceLight:
		dark = false
	case AppearanceDark:
		dark = true
	}
	return accentPalette(t.Accent, dark)
}

// Color maps a Fyne colour name onto the palette.
func (t *Theme) Color(name fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	p := t.PaletteFor(v)

	switch name {
	// Surfaces. macOS window chrome is grey and content wells are white (or
	// near-black in dark mode); Fyne paints containers with Background, so
	// Background is the chrome and the grid paints its own well.
	case ftheme.ColorNameBackground:
		return p.WindowBackground
	case ftheme.ColorNameHeaderBackground:
		return p.SidebarBackground
	case ftheme.ColorNameMenuBackground, ftheme.ColorNameOverlayBackground:
		return p.ElevatedBackground
	case ftheme.ColorNameInputBackground:
		return p.ControlBackground
	case ftheme.ColorNameButton:
		return p.ControlBackground
	case ftheme.ColorNameDisabledButton:
		return p.Hover

	// Text.
	case ftheme.ColorNameForeground:
		return p.Label
	case ftheme.ColorNamePlaceHolder:
		return p.TertiaryLabel
	case ftheme.ColorNameDisabled:
		return p.QuaternaryLabel
	case ftheme.ColorNameHyperlink:
		// The reason AccentText exists: systemBlue is 4.02:1 on white.
		return p.AccentText

	// Accent and interaction.
	case ftheme.ColorNamePrimary:
		return p.ControlAccent
	case ftheme.ColorNamePressed:
		return p.ControlAccentPressed
	case ftheme.ColorNameHover:
		return p.Hover
	case ftheme.ColorNameSelection:
		return p.SelectedEmphasized
	case ftheme.ColorNameFocus:
		return p.FocusRing
	case ftheme.ColorNameForegroundOnPrimary:
		return p.OnControlAccent

	// Status.
	case ftheme.ColorNameError:
		return p.Danger
	case ftheme.ColorNameWarning:
		return p.Warning
	case ftheme.ColorNameSuccess:
		return p.Success
	case ftheme.ColorNameForegroundOnError,
		ftheme.ColorNameForegroundOnSuccess,
		ftheme.ColorNameForegroundOnWarning:
		return onStatusText(p)

	// Lines and chrome.
	case ftheme.ColorNameSeparator:
		return p.Separator
	case ftheme.ColorNameInputBorder:
		return p.ControlBorder
	case ftheme.ColorNameInnerWindowBorder:
		return p.OpaqueSeparator
	case ftheme.ColorNameInnerWindowBorderInactive:
		return p.Separator
	case ftheme.ColorNameShadow:
		return p.Shadow
	case ftheme.ColorNameScrollBar:
		return p.Scrollbar
	case ftheme.ColorNameScrollBarBackground:
		return color.NRGBA{}
	}

	// An unmapped name is a Fyne addition we have not accounted for. Falling
	// through to the stock theme keeps the app usable rather than drawing
	// transparent, and the completeness test flags it in CI.
	return ftheme.DefaultTheme().Color(name, v)
}

// onStatusText returns the label colour for text drawn on a filled status
// background. Status fills use Apple's saturated system colours, which are
// dark enough for white in both appearances.
func onStatusText(p Palette) color.NRGBA {
	if p.Label.R > 0x80 { // dark appearance: labels are near-white
		return p.WindowBackground
	}
	return color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
}

// Size maps a Fyne size name onto the metric scale.
func (t *Theme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	// Type scale, from HIG's macOS text styles.
	case ftheme.SizeNameText:
		return TextBody
	case ftheme.SizeNameCaptionText:
		return TextFootnote
	case ftheme.SizeNameSubHeadingText:
		return TextTitle3
	case ftheme.SizeNameHeadingText:
		return TextTitle2

	// Spacing. macOS runs tighter than Material, and a data tool tighter
	// again — density here is the difference between seeing 20 rows and 30.
	case ftheme.SizeNamePadding:
		return SpaceXS
	case ftheme.SizeNameInnerPadding:
		return SpaceMD
	case ftheme.SizeNameLineSpacing:
		return SpaceXS

	case ftheme.SizeNameInlineIcon:
		return IconInline

	// Scrollers. macOS overlay scrollers are narrow and fully rounded.
	case ftheme.SizeNameScrollBar:
		return ScrollbarSize
	case ftheme.SizeNameScrollBarSmall:
		return SpaceXS
	case ftheme.SizeNameScrollBarRadius:
		return ScrollbarSize / 2

	// Lines.
	case ftheme.SizeNameSeparatorThickness:
		return SeparatorWidth
	case ftheme.SizeNameSplitThickness:
		return SeparatorWidth
	case ftheme.SizeNameInputBorder:
		return BorderWidth

	// Radii, from HIG: 6pt controls, 10pt popovers and sheets.
	case ftheme.SizeNameButtonRadius, ftheme.SizeNameInputRadius:
		return RadiusControl
	case ftheme.SizeNameSelectionRadius:
		return RadiusSmall
	case ftheme.SizeNameMenuRadius:
		return RadiusControl
	case ftheme.SizeNameCardRadius,
		ftheme.SizeNameDialogRadius,
		ftheme.SizeNamePopupRadius,
		ftheme.SizeNameInnerWindowRadius:
		return RadiusPopover

	// Inner window chrome.
	case ftheme.SizeNameWindowTitleBarHeight:
		return HeaderHeight
	case ftheme.SizeNameWindowButtonHeight:
		return IconInline
	case ftheme.SizeNameWindowButtonIcon:
		return TextCallout
	case ftheme.SizeNameWindowButtonRadius:
		return RadiusControl
	}

	return ftheme.DefaultTheme().Size(name)
}

// Font returns the typeface for a text style.
//
// On macOS this resolves to the system font (SF Pro / SF Mono) so the
// application reads as native (UX principle 11). Resolution is best-effort and
// all-or-nothing: mixing SF regular with a fallback bold would look worse than
// using the fallback throughout, so systemFont returns nil unless every style
// it needs is available. See fonts.go.
func (t *Theme) Font(style fyne.TextStyle) fyne.Resource {
	if r := systemFont(style); r != nil {
		return r
	}
	return ftheme.DefaultTheme().Font(style)
}

// Icon returns an icon resource, preferring our own object-kind icons and
// falling back to Fyne's set for standard actions.
func (t *Theme) Icon(name fyne.ThemeIconName) fyne.Resource {
	if r, ok := customIcons[name]; ok {
		return r
	}
	return ftheme.DefaultTheme().Icon(name)
}
