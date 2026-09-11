# ADR-0006: macOS Human Interface Guidelines as the design system

**Status:** Accepted · **Date:** 2026-09-10

## Context

ADR-0001 chose Fyne, whose stock theme is Material-derived. UX principle 12
already required replacing it, but "replace it with what?" was unanswered — the
first pass was a generic neutral palette in the manner of Linear or Zed.

The owner directed that the application follow **macOS's design system**, on the
grounds that it reads as cleaner and more user-friendly than Material. The
primary development and target platform is macOS (OQ-4).

## Decision

Follow Apple's Human Interface Guidelines for macOS.

Concretely:

- **Apple's system colours are used verbatim** and pinned by a test
  (`TestSystemColoursMatchApple`). Drifting from `systemBlue #007AFF` is how an
  application stops reading as native.
- **macOS's semantic structure is adopted**, not merely its hues: the
  window/content/sidebar background hierarchy, the four-level label hierarchy,
  and the emphasized/unemphasized selection distinction that marks a focused
  window.
- **HIG metrics**: 13pt body (the standard macOS control size), the 4pt spacing
  rhythm, 6pt control radii and 10pt popover radii, a 3pt focus ring, narrow
  overlay scrollers, 24pt table rows.
- **Depth through surface tone and hairlines**, not Material's elevation
  shadows. macOS dark mode is mid-grey (`#323232` chrome over `#1E1E1E`
  content), not black — reproducing that relationship is most of what makes a
  dark theme read as macOS.

## Consequence: HIG and WCAG conflict, and WCAG wins

This is the substantive finding. Several of Apple's neutrals do not meet
WCAG 2.1 AA, which NFR-A2 commits us to:

| HIG colour | Measured | AA needs |
|---|---|---|
| `secondaryLabelColor` (50% black on white) | 3.55:1 | 4.5:1 |
| `tertiaryLabelColor` (26% black) | 1.87:1 | 4.5:1 |
| `separatorColor` (10% black) | 1.20:1 | 3.0:1 |
| `systemBlue` as 13pt text on white | 4.02:1 | 4.5:1 |
| white on `systemBlue` | 4.02:1 | 4.5:1 |

Where they conflict, **AA wins and the deviation is marked `HIG-DEVIATION` in
`tokens.go`**. Hue and hierarchy are preserved; values are tuned until they
pass.

Two of these deserve note because Apple solves them the same way we do:

- **`AccentText` is a separate role from `ControlAccent`.** macOS has exactly
  this split — `linkColor` is a darker blue than `systemBlue`, because
  `systemBlue` is tuned for fills and large glyphs. Using one blue for both is
  the most common way an otherwise-accessible palette fails.
- **Filled controls use `selectedContentBackgroundColor`, not `systemBlue`.**
  That is the blue macOS actually fills selected rows and default buttons with,
  and white on it passes.

Tertiary and quaternary labels are kept at HIG values: WCAG 1.4.3 exempts
placeholder and disabled text, so they are tested for *ordering* rather than
against the AA bar. `separatorColor` is likewise kept as the decorative
hairline WCAG 1.4.11 exempts, with a separate `ControlBorder` carrying the 3:1
duty for boundaries that identify a control.

## Consequence: colour cannot be the only signal

Environment tagging (FR-1.7) marks `dev` green and `production` red — precisely
the axis of the most common colour vision deficiency. A user with deuteranopia
would see the safest and most dangerous connections as near-identical.

Every environment treatment therefore carries a **text label** as a second
channel, and `TestEnvironmentLabelIsAlwaysPresent` enforces it. Colour alone
would make this a safety feature that fails for roughly one in twelve men.

## Consequence: SF Pro is best-effort

Modern macOS ships SF as variable fonts (`/System/Library/Fonts/SFNS.ttf`)
carrying every weight on a `wght` axis. Fyne's text stack renders a variable
font's default instance — Regular — so loading it would give regular glyphs for
bold text.

Font resolution is therefore all-or-nothing: unless distinct static regular and
bold faces are found, Fyne's bundled font is used throughout. This succeeds for
anyone who has installed SF Pro from Apple's developer downloads and degrades
cleanly otherwise. A missing font is never a startup failure.

## Consequence: SF Symbols cannot be shipped

Apple's licence does not permit redistributing SF Symbols in an application.
The object-kind icons in `icons.go` are drawn to match their optical weight —
thin strokes, 16pt grid, rounded caps — rather than copied.

## Verification

The palette is not a matter of taste alone. `contrast_test.go` checks every
foreground/background pair the UI actually draws, in both appearances, and
`coverage_test.go` fails the build if a role is added without being either
checked or explicitly exempted with a reason. `cmd/themegallery` renders every
token side by side in both appearances for the judgement tests cannot make.

## Addendum (2026-09-11): the accent colour

FR-15.3 asks for an accent choice. The accents are macOS's own: blue,
purple, pink, red, orange, yellow, green and graphite, chosen from View ›
Accent Colour and kept in the settings file. Blue is the palettes' own,
tuned by hand. The others start from Apple's system colours and are
derived, once, so that each passes WCAG AA wherever the palette's blue
does. A fill is darkened until white text on it passes, unless that would
take it far from itself, as it would the lightest colours; then the colour
stays and its text is near-black. As text, an accent is darkened on light,
or lightened on dark, until it reads on the content, the window and its
own tint. The selection follows the accent, as macOS's does. Where Apple's
colours fall short of AA, accessibility wins (UX principle 13). A test
holds every accent, in both appearances, to every accent pair.

## Addendum (2026-09-11): buttons carry words

A button with only an icon has no name for a screen reader to read out,
and Fyne gives no other way to name one. So every button the app makes
carries a word: the sidebar's New, the side panel's, error band's and cell
viewer's Close, and Saved Queries' Delete. The icon stays beside the word
where it helps. Where the HIG would draw a bare glyph, a × to close or a +
to add, accessibility wins (UX principle 13). A test walks the window, in
two scenes that between them show the app's panels, bars and lists, and
fails on any visible button with no words. Fyne's own tab bars are the one
exception: they draw a "…" menu the app cannot name, so the test walks
what the tabs hold and not the bar.
