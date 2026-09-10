// Command themegallery renders every design token for visual review.
//
// TASKS.md T0.36. The contrast suite proves the palette is *legible*; only
// looking at it proves it is *good*. This harness exists so that judgement can
// happen in Phase 0, while the tokens are still cheap to change, rather than
// after they are spread across forty widgets.
//
// Run with: go run ./cmd/themegallery
package main

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	th "github.com/ikigai-db/ikigai-db/internal/ui/theme"
)

func main() {
	a := app.New()
	theme := th.New()
	a.Settings().SetTheme(theme)

	w := a.NewWindow("Ikigai DB — Theme Gallery")
	w.Resize(fyne.NewSize(1120, 860))

	appearance := widget.NewSelect(
		[]string{"System", "Light", "Dark"},
		func(s string) {
			switch s {
			case "Light":
				theme.Appearance = th.AppearanceLight
			case "Dark":
				theme.Appearance = th.AppearanceDark
			default:
				theme.Appearance = th.AppearanceSystem
			}
			a.Settings().SetTheme(theme)
		})
	appearance.SetSelected("System")

	header := container.NewHBox(
		widget.NewLabelWithStyle("Theme Gallery", fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true}),
		widget.NewLabel("macOS Human Interface Guidelines"),
		widget.NewSeparator(),
		widget.NewLabel("Live widgets:"),
		appearance,
	)

	tabs := container.NewAppTabs(
		container.NewTabItem("Palette", scroll(paletteTab())),
		container.NewTabItem("Type & Metrics", scroll(typeTab())),
		container.NewTabItem("Data States", scroll(dataTab(theme))),
		container.NewTabItem("Environments", scroll(environmentTab(theme))),
		container.NewTabItem("Icons", scroll(iconTab())),
		container.NewTabItem("Controls", scroll(controlsTab())),
	)

	w.SetContent(container.NewBorder(
		container.NewVBox(header, widget.NewSeparator()), nil, nil, nil, tabs))
	w.ShowAndRun()
}

func scroll(o fyne.CanvasObject) fyne.CanvasObject {
	return container.NewScroll(container.NewPadded(o))
}

// --- Palette ----------------------------------------------------------------

// paletteTab shows every role in both appearances side by side. Drawing both
// at once matters: most palette mistakes are relational — a role that works in
// light and collapses in dark — and a toggle hides exactly that.
func paletteTab() fyne.CanvasObject {
	groups := []struct {
		title string
		roles []string
	}{
		{"Surfaces", []string{
			"WindowBackground", "ContentBackground", "SidebarBackground",
			"ElevatedBackground", "ControlBackground", "AlternateRow", "Hover"}},
		{"Selection", []string{
			"SelectedEmphasized", "SelectedUnemphasized", "OnSelectedEmphasized",
			"RangeSelection"}},
		{"Labels", []string{
			"Label", "SecondaryLabel", "TertiaryLabel", "QuaternaryLabel", "Null"}},
		{"Lines", []string{"Separator", "OpaqueSeparator", "ControlBorder"}},
		{"Accent", []string{
			"ControlAccent", "ControlAccentHover", "ControlAccentPressed",
			"ControlAccentSubtle", "OnControlAccent", "AccentText"}},
		{"Status", []string{
			"Danger", "DangerSubtle", "Warning", "WarningSubtle",
			"Success", "SuccessSubtle"}},
		{"Changeset", []string{
			"AddedFg", "AddedBg", "ModifiedFg", "ModifiedBg", "DeletedFg", "DeletedBg"}},
	}

	out := container.NewVBox()
	for _, g := range groups {
		out.Add(sectionTitle(g.title))
		row := container.NewGridWithColumns(4)
		for _, role := range g.roles {
			row.Add(swatchPair(role))
		}
		out.Add(row)
		out.Add(widget.NewSeparator())
	}

	out.Add(sectionTitle("Apple system colours (pinned, used verbatim)"))
	sys := container.NewGridWithColumns(5)
	for _, s := range []struct {
		name string
		c    color.NRGBA
	}{
		{"SystemBlue", th.SystemBlue}, {"SystemGreen", th.SystemGreen},
		{"SystemIndigo", th.SystemIndigo}, {"SystemOrange", th.SystemOrange},
		{"SystemPink", th.SystemPink}, {"SystemPurple", th.SystemPurple},
		{"SystemRed", th.SystemRed}, {"SystemTeal", th.SystemTeal},
		{"SystemYellow", th.SystemYellow}, {"SystemGray", th.SystemGray},
	} {
		sys.Add(swatch(s.name, s.c))
	}
	out.Add(sys)

	return out
}

// swatchPair draws one role in light and dark, stacked, with hex values.
func swatchPair(role string) fyne.CanvasObject {
	l, okL := th.RoleColor(th.Light, role)
	d, okD := th.RoleColor(th.Dark, role)
	if !okL || !okD {
		return widget.NewLabel(role + " (missing)")
	}
	return container.NewVBox(
		caption(role),
		container.NewGridWithColumns(2, chip(l), chip(d)),
		caption(fmt.Sprintf("%s   %s", hexOf(l), hexOf(d))),
	)
}

func swatch(name string, c color.NRGBA) fyne.CanvasObject {
	return container.NewVBox(caption(name), chip(c), caption(hexOf(c)))
}

// chip draws a colour over a checkerboard so that alpha is visible — several
// roles (FocusRing, Scrollbar, Shadow) are deliberately translucent, and a
// swatch on an opaque ground would misrepresent them.
func chip(c color.NRGBA) fyne.CanvasObject {
	r := canvas.NewRectangle(c)
	r.SetMinSize(fyne.NewSize(0, 34))
	r.CornerRadius = th.RadiusSmall

	board := container.NewGridWithColumns(8)
	for i := 0; i < 16; i++ {
		shade := color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
		if (i/8+i%8)%2 == 0 {
			shade = color.NRGBA{R: 0xD0, G: 0xD0, B: 0xD4, A: 0xFF}
		}
		board.Add(canvas.NewRectangle(shade))
	}
	return container.NewStack(board, r)
}

// --- Type & metrics ---------------------------------------------------------

func typeTab() fyne.CanvasObject {
	out := container.NewVBox(sectionTitle("Type scale (HIG macOS text styles)"))

	for _, s := range []struct {
		name string
		size float32
	}{
		{"LargeTitle 26", th.TextLargeTitle}, {"Title1 22", th.TextTitle1},
		{"Title2 17", th.TextTitle2}, {"Title3 15", th.TextTitle3},
		{"Body 13 — the standard macOS control size", th.TextBody},
		{"Callout 12", th.TextCallout}, {"Footnote 11", th.TextFootnote},
		{"Caption 10", th.TextCaption},
	} {
		t := canvas.NewText(s.name, nil)
		t.TextSize = s.size
		out.Add(t)
	}

	out.Add(widget.NewSeparator())
	out.Add(sectionTitle("Corner radii"))
	radii := container.NewGridWithColumns(4)
	for _, r := range []struct {
		name string
		v    float32
	}{
		{"Small 4", th.RadiusSmall}, {"Control 6", th.RadiusControl},
		{"Popover 10", th.RadiusPopover}, {"Window 10", th.RadiusWindow},
	} {
		box := canvas.NewRectangle(th.SystemGray)
		box.SetMinSize(fyne.NewSize(0, 44))
		box.CornerRadius = r.v
		radii.Add(container.NewVBox(caption(r.name), box))
	}
	out.Add(radii)

	out.Add(widget.NewSeparator())
	out.Add(sectionTitle("Spacing rhythm (4pt)"))
	sp := container.NewVBox()
	for _, s := range []struct {
		name string
		v    float32
	}{
		{"2XS 2", th.Space2XS}, {"XS 4", th.SpaceXS}, {"SM 6", th.SpaceSM},
		{"MD 8", th.SpaceMD}, {"LG 12", th.SpaceLG}, {"XL 16", th.SpaceXL},
		{"2XL 20", th.Space2XL},
	} {
		bar := canvas.NewRectangle(th.SystemBlue)
		bar.SetMinSize(fyne.NewSize(s.v*8, 10))
		sp.Add(container.NewHBox(widget.NewLabel(s.name), bar))
	}
	out.Add(sp)

	return out
}

// --- Data states ------------------------------------------------------------

// dataTab is the most important panel here. UX principle 7 requires NULL,
// empty string, 0 and false to be visually distinct, and that is a judgement
// only the eye can make.
func dataTab(t *th.Theme) fyne.CanvasObject {
	out := container.NewVBox(
		sectionTitle("Value rendering — UX principle 7"),
		widget.NewLabel("NULL, empty string, zero and false must never be confusable."),
	)

	for _, dark := range []bool{false, true} {
		p := paletteFor(dark)
		out.Add(caption(appearanceName(dark)))

		rows := container.NewVBox()
		for i, cell := range []struct {
			label string
			text  string
			null  bool
		}{
			{"NULL", "NULL", true},
			{"empty string", "", false},
			{"zero", "0", false},
			{"false", "false", false},
			{"whitespace", "   ", false},
			{"text", "Ada Lovelace", false},
		} {
			bg := p.ContentBackground
			if i%2 == 1 {
				bg = p.AlternateRow
			}
			fg := p.Label
			if cell.null {
				fg = p.Null
			}
			rows.Add(gridRow(bg, fg, cell.label, cell.text, p))
		}
		out.Add(rows)
		out.Add(widget.NewSeparator())
	}

	out.Add(sectionTitle("Changeset states — FR-4.3"))
	for _, dark := range []bool{false, true} {
		p := paletteFor(dark)
		out.Add(caption(appearanceName(dark)))
		rows := container.NewVBox()
		for _, s := range []struct {
			label  string
			fg, bg color.NRGBA
		}{
			{"inserted", p.AddedFg, p.AddedBg},
			{"modified", p.ModifiedFg, p.ModifiedBg},
			{"deleted", p.DeletedFg, p.DeletedBg},
			{"unchanged", p.Label, p.ContentBackground},
		} {
			rows.Add(gridRow(s.bg, s.fg, s.label, "value", p))
		}
		out.Add(rows)
	}

	return out
}

// gridRow imitates one row of the data grid at the real row height, so that
// density is judged at the size it will actually be drawn.
func gridRow(bg, fg color.NRGBA, label, value string, p th.Palette) fyne.CanvasObject {
	back := canvas.NewRectangle(bg)
	back.SetMinSize(fyne.NewSize(0, th.RowHeight))

	name := canvas.NewText(label, p.SecondaryLabel)
	name.TextSize = th.TextBody

	val := canvas.NewText(value, fg)
	val.TextSize = th.TextBody
	if value == "NULL" {
		val.TextStyle = fyne.TextStyle{Italic: true}
	}

	line := canvas.NewRectangle(p.Separator)
	line.SetMinSize(fyne.NewSize(0, th.SeparatorWidth))

	content := container.NewGridWithColumns(2,
		container.NewPadded(name), container.NewPadded(val))
	return container.NewVBox(container.NewStack(back, content), line)
}

// --- Environments -----------------------------------------------------------

func environmentTab(t *th.Theme) fyne.CanvasObject {
	out := container.NewVBox(
		sectionTitle("Environment treatment — FR-1.7, UX principle 8"),
		widget.NewLabel("Colour is never the only signal: dev/production is a red/green pair, "+
			"so every badge carries a text label."),
	)

	for _, dark := range []bool{false, true} {
		out.Add(caption(appearanceName(dark)))
		row := container.NewGridWithColumns(4)
		for _, name := range th.EnvironmentNames() {
			e := t.Environment(name, dark)
			p := paletteFor(dark)

			badge := canvas.NewRectangle(e.Accent)
			badge.SetMinSize(fyne.NewSize(0, 24))
			badge.CornerRadius = th.RadiusSmall
			text := canvas.NewText(e.Label, e.OnAccent)
			text.TextSize = th.TextFootnote
			text.TextStyle = fyne.TextStyle{Bold: true}
			text.Alignment = fyne.TextAlignCenter

			bar := canvas.NewRectangle(e.Subtle)
			bar.SetMinSize(fyne.NewSize(0, th.ToolbarHeight))

			tabLabel := canvas.NewText("  orders — "+name, p.Label)
			tabLabel.TextSize = th.TextBody

			note := "quiet"
			if e.Emphatic {
				note = "emphatic — persistent across every derived tab"
			}

			row.Add(container.NewVBox(
				container.NewStack(badge, text),
				container.NewStack(bar, tabLabel),
				caption(note),
			))
		}
		out.Add(row)
		out.Add(widget.NewSeparator())
	}
	return out
}

// --- Icons ------------------------------------------------------------------

func iconTab() fyne.CanvasObject {
	out := container.NewVBox(
		sectionTitle("Object-kind icons"),
		widget.NewLabel("Drawn to match SF Symbols' optical weight; Apple's licence "+
			"does not permit redistributing the originals."),
	)

	grid := container.NewGridWithColumns(5)
	for _, i := range []struct {
		name string
		n    fyne.ThemeIconName
	}{
		{"database", th.IconNameDatabase}, {"schema", th.IconNameSchema},
		{"table", th.IconNameTable}, {"view", th.IconNameView},
		{"column", th.IconNameColumn}, {"index", th.IconNameIndex},
		{"primary key", th.IconNamePrimaryKey}, {"foreign key", th.IconNameForeignKey},
		{"routine", th.IconNameRoutine}, {"collection", th.IconNameCollection},
		{"key", th.IconNameKey}, {"topic", th.IconNameTopic},
		{"partition", th.IconNamePartition}, {"consumer group", th.IconNameConsumerGroup},
		{"cluster", th.IconNameCluster},
	} {
		icon := widget.NewIcon(th.New().Icon(i.n))
		grid.Add(container.NewVBox(container.NewCenter(icon), caption(i.name)))
	}
	out.Add(grid)
	return out
}

// --- Controls ---------------------------------------------------------------

func controlsTab() fyne.CanvasObject {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("host.example.com")

	pw := widget.NewPasswordEntry()
	pw.SetPlaceHolder("password")

	disabled := widget.NewEntry()
	disabled.SetText("read-only")
	disabled.Disable()

	sel := widget.NewSelect([]string{"verify-full", "verify-ca", "require", "disable"}, nil)
	sel.SetSelected("verify-full")

	primary := widget.NewButton("Connect", nil)
	primary.Importance = widget.HighImportance

	danger := widget.NewButton("Drop table", nil)
	danger.Importance = widget.DangerImportance

	return container.NewVBox(
		sectionTitle("Live widgets (follow the appearance selector above)"),
		widget.NewForm(
			widget.NewFormItem("Host", entry),
			widget.NewFormItem("Password", pw),
			widget.NewFormItem("TLS mode", sel),
			widget.NewFormItem("Disabled", disabled),
		),
		container.NewHBox(primary, widget.NewButton("Cancel", nil), danger),
		widget.NewCheck("Read-only connection", nil),
		widget.NewProgressBar(),
		widget.NewSeparator(),
		widget.NewHyperlink("A hyperlink uses AccentText, not systemBlue", nil),
		widget.NewRichTextFromMarkdown("Body text at 13pt.\n\n"+
			"`SELECT * FROM orders WHERE total > 100` — monospace run."),
	)
}

// --- helpers ----------------------------------------------------------------

func sectionTitle(s string) fyne.CanvasObject {
	return widget.NewLabelWithStyle(s, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
}

func caption(s string) fyne.CanvasObject {
	t := canvas.NewText(s, th.Light.SecondaryLabel)
	t.TextSize = th.TextFootnote
	return t
}

func appearanceName(dark bool) string {
	if dark {
		return "Dark"
	}
	return "Light"
}

func paletteFor(dark bool) th.Palette {
	if dark {
		return th.Dark
	}
	return th.Light
}

func hexOf(c color.NRGBA) string {
	if c.A != 0xFF {
		return fmt.Sprintf("#%02X%02X%02X %d%%", c.R, c.G, c.B, int(float32(c.A)/255*100))
	}
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}
