package theme

import (
	"image/color"
	"testing"

	"fyne.io/fyne/v2"
	ftheme "fyne.io/fyne/v2/theme"
)

// allColorNames is every colour Fyne asks a theme for. If Fyne adds one and we
// do not map it, TestEveryColorNameIsMapped catches it — the failure mode
// otherwise is a widget drawn transparent, which is easy to miss and hard to
// trace back.
var allColorNames = []fyne.ThemeColorName{
	ftheme.ColorNameBackground, ftheme.ColorNameButton, ftheme.ColorNameDisabled,
	ftheme.ColorNameDisabledButton, ftheme.ColorNameError, ftheme.ColorNameFocus,
	ftheme.ColorNameForeground, ftheme.ColorNameForegroundOnError,
	ftheme.ColorNameForegroundOnPrimary, ftheme.ColorNameForegroundOnSuccess,
	ftheme.ColorNameForegroundOnWarning, ftheme.ColorNameHeaderBackground,
	ftheme.ColorNameHover, ftheme.ColorNameHyperlink, ftheme.ColorNameInnerWindowBorder,
	ftheme.ColorNameInnerWindowBorderInactive, ftheme.ColorNameInputBackground,
	ftheme.ColorNameInputBorder, ftheme.ColorNameMenuBackground,
	ftheme.ColorNameOverlayBackground, ftheme.ColorNamePlaceHolder,
	ftheme.ColorNamePressed, ftheme.ColorNamePrimary, ftheme.ColorNameScrollBar,
	ftheme.ColorNameScrollBarBackground, ftheme.ColorNameSelection,
	ftheme.ColorNameSeparator, ftheme.ColorNameShadow, ftheme.ColorNameSuccess,
	ftheme.ColorNameWarning,
}

var allSizeNames = []fyne.ThemeSizeName{
	ftheme.SizeNameButtonRadius, ftheme.SizeNameCaptionText, ftheme.SizeNameCardRadius,
	ftheme.SizeNameDialogRadius, ftheme.SizeNameHeadingText, ftheme.SizeNameInlineIcon,
	ftheme.SizeNameInnerPadding, ftheme.SizeNameInnerWindowRadius,
	ftheme.SizeNameInputBorder, ftheme.SizeNameInputRadius, ftheme.SizeNameLineSpacing,
	ftheme.SizeNameMenuRadius, ftheme.SizeNamePadding, ftheme.SizeNamePopupRadius,
	ftheme.SizeNameScrollBar, ftheme.SizeNameScrollBarRadius, ftheme.SizeNameScrollBarSmall,
	ftheme.SizeNameSelectionRadius, ftheme.SizeNameSeparatorThickness,
	ftheme.SizeNameSplitThickness, ftheme.SizeNameSubHeadingText, ftheme.SizeNameText,
	ftheme.SizeNameWindowButtonHeight, ftheme.SizeNameWindowButtonIcon,
	ftheme.SizeNameWindowButtonRadius, ftheme.SizeNameWindowTitleBarHeight,
}

func TestEveryColorNameIsMapped(t *testing.T) {
	th := New()
	for _, variant := range []fyne.ThemeVariant{ftheme.VariantLight, ftheme.VariantDark} {
		for _, name := range allColorNames {
			c := th.Color(name, variant)
			if c == nil {
				t.Errorf("variant=%d %s: nil colour", variant, name)
				continue
			}
			// ScrollBarBackground is deliberately transparent: macOS overlay
			// scrollers float over content with no track.
			if name == ftheme.ColorNameScrollBarBackground {
				continue
			}
			if _, _, _, a := c.RGBA(); a == 0 {
				t.Errorf("variant=%d %s: fully transparent; widget would not draw",
					variant, name)
			}
		}
	}
}

func TestEverySizeNameIsPositive(t *testing.T) {
	th := New()
	for _, name := range allSizeNames {
		if v := th.Size(name); v <= 0 {
			t.Errorf("%s = %v; a non-positive size collapses the widget", name, v)
		}
	}
}

func TestSizesAreSaneForADataTool(t *testing.T) {
	th := New()

	// Body text at macOS's standard control size. Drift here changes how many
	// rows fit on screen, which is the most consequential number in the app.
	if got := th.Size(ftheme.SizeNameText); got != TextBody {
		t.Errorf("body text = %v, want %v (HIG macOS body)", got, TextBody)
	}
	// Radii must read as macOS controls, not Material pills.
	if got := th.Size(ftheme.SizeNameButtonRadius); got != RadiusControl {
		t.Errorf("button radius = %v, want %v (HIG control radius)", got, RadiusControl)
	}
	// Padding must stay tight; Material's 8pt default wastes a row per pane.
	if got := th.Size(ftheme.SizeNamePadding); got > SpaceMD {
		t.Errorf("padding = %v, too loose for a data tool", got)
	}
}

func TestAppearanceOverrideBeatsSystemVariant(t *testing.T) {
	// A user who picks Light must get Light even in a dark OS session
	// (FR-15.3), so the override has to win over the variant Fyne passes in.
	light := &Theme{Appearance: AppearanceLight}
	if got := light.PaletteFor(ftheme.VariantDark); got.Label != Light.Label {
		t.Error("explicit Light appearance did not override the dark variant")
	}

	dark := &Theme{Appearance: AppearanceDark}
	if got := dark.PaletteFor(ftheme.VariantLight); got.Label != Dark.Label {
		t.Error("explicit Dark appearance did not override the light variant")
	}

	sys := New()
	if got := sys.PaletteFor(ftheme.VariantDark); got.Label != Dark.Label {
		t.Error("system appearance did not follow the dark variant")
	}
	if got := sys.PaletteFor(ftheme.VariantLight); got.Label != Light.Label {
		t.Error("system appearance did not follow the light variant")
	}
}

func TestHyperlinkUsesAccentTextNotFill(t *testing.T) {
	// The whole reason AccentText exists. If these ever converge, links stop
	// meeting AA on white.
	th := New()
	link := th.Color(ftheme.ColorNameHyperlink, ftheme.VariantLight)
	fill := th.Color(ftheme.ColorNamePrimary, ftheme.VariantLight)
	if link == fill {
		t.Error("hyperlink colour equals the accent fill; " +
			"systemBlue is only 4.02:1 on white and would fail AA as text")
	}
}

func TestFontAlwaysReturnsAResource(t *testing.T) {
	// Font resolution is best-effort: SF Pro may or may not be installed. It
	// must never return nil or panic — a missing font cannot be a startup
	// failure.
	th := New()
	styles := []fyne.TextStyle{
		{}, {Bold: true}, {Italic: true}, {Bold: true, Italic: true},
		{Monospace: true}, {Monospace: true, Bold: true},
	}
	for _, s := range styles {
		if r := th.Font(s); r == nil {
			t.Errorf("Font(%+v) returned nil", s)
		}
	}
}

func TestSystemFontResolutionIsAllOrNothing(t *testing.T) {
	// Mixing SF regular with a fallback bold looks worse than a consistent
	// fallback, so resolveFontSet must supply both weights or neither.
	set := resolveFontSet()
	if set == nil {
		t.Skip("system font not installed as static weights; using Fyne's bundled font")
	}
	if set.regular == nil || set.bold == nil {
		t.Error("font set resolved without both regular and bold")
	}
}

func TestCustomIconsAllResolve(t *testing.T) {
	th := New()
	for name := range customIcons {
		r := th.Icon(name)
		if r == nil {
			t.Errorf("icon %s: nil resource", name)
			continue
		}
		if len(r.Content()) == 0 {
			t.Errorf("icon %s: empty content", name)
		}
	}
	// A name we do not define must fall through to Fyne's set rather than
	// returning nil, or the widget draws nothing.
	if th.Icon(ftheme.IconNameSettings) == nil {
		t.Error("unmapped icon name did not fall back to Fyne's set")
	}
}

func TestIconsCoverEveryBrowsableObjectKind(t *testing.T) {
	// The explorer shows these kinds; a missing icon leaves a blank row.
	required := []fyne.ThemeIconName{
		IconNameDatabase, IconNameSchema, IconNameTable, IconNameView,
		IconNameColumn, IconNameIndex, IconNamePrimaryKey, IconNameForeignKey,
		IconNameRoutine, IconNameCollection, IconNameKey,
		IconNameTopic, IconNamePartition, IconNameConsumerGroup, IconNameCluster,
		IconNameTrigger, IconNameSequence, IconNameType,
	}
	for _, n := range required {
		if _, ok := customIcons[n]; !ok {
			t.Errorf("no icon defined for %s", n)
		}
	}
}

func TestOnStatusTextPicksAReadableLabel(t *testing.T) {
	for _, p := range []struct {
		name string
		pal  Palette
	}{{"Light", Light}, {"Dark", Dark}} {
		on := onStatusText(p.pal)
		for label, fill := range map[string]color.NRGBA{
			"Danger": p.pal.Danger, "Warning": p.pal.Warning, "Success": p.pal.Success,
		} {
			if r := contrastRatio(on, fill); r < aaLarge {
				t.Errorf("%s: status text on %s fill is %.2f:1 (need %.1f:1)",
					p.name, label, r, aaLarge)
			}
		}
	}
}
