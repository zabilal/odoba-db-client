# ADR-0134: A chart on the screen, and taken away

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.23 · **Requirements:** FR-11.3, FR-11.1
**Packages:** `internal/ui/scene`, `internal/ui/chart`, `internal/ui/shell`

## Context

T3.21 gave the kinds and the marks, T3.22 gave the reading. Nothing drew a
chart: there was no frame to put the marks in, no widget, no way into one
from the window, and so nothing to export. FR-11.3 asks for PNG and SVG.

The diagram settled the shape of an export (ADR-0130): say the drawing once,
so an exported picture cannot differ from the one it was exported from. A
chart is further from that than a diagram was, because its marks are pixels
on purpose (ADR-0004), and an SVG of a hundred thousand scatter points is a
file nothing will open.

## Decisions

1. **The vocabulary a picture is described in is one package.** The
   diagram's shapes, its Fyne renderer and its SVG writer moved to
   `internal/ui/scene`, and the diagram names them locally with aliases.
   Two copies of "say the drawing once" would be two drawings that could
   drift, which is the thing the rule exists to prevent.

2. **An SVG embeds the rasterised layer and keeps everything else as text.**
   That is the honest answer to a layer that is pixels by design. The axes,
   their labels, the titles and the key stay `<text>` and `<line>`, so a
   number in an exported chart can be searched for and copied; the marks are
   one `<image>` with a base64 PNG in it, so the file is one file and opens
   in anything.

3. **Transparency is its own attribute, not an eight-digit hex.** Eight
   digits are SVG 2. A viewer that did not read them would draw a faint
   gridline solid black, which is a different picture — and a different
   picture is the one thing an export must not be.

4. **Text is placed by arithmetic rather than by `dominant-baseline`.** That
   attribute is not honoured everywhere a file might be opened, and a tick
   label half a line out of place would be worse than one placed by hand.
   The scene says where the top or the middle of a line of text is; each
   renderer puts that in its own terms.

5. **The frame is worked out without a font.** It is worked out the same way
   for a window, a PNG and an SVG, and only one of the three has a font to
   ask. Label widths are estimated at 0.62 of the type size per character —
   a deliberate over-estimate, because too wide leaves a gap and too narrow
   lets two labels touch.

6. **A picture at twice the size is drawn at twice the detail.** Everything
   in the frame is worked out from the size it was given, so an export is
   laid out at what was asked for rather than captured from the window and
   scaled. An axis that is twice as tall carries more ticks, and the marks
   are rasterised at the size they are placed at.

7. **A histogram's bins are part of the frame.** Its axes are not in its
   data: the bottom is the bins' edges and the side is how many fell in the
   fullest one. So the bins are worked out once, in the frame, and the bars
   and the axis read the same ones — a bar drawn against an axis that binned
   differently would start somewhere its label does not say.

8. **How many bins there are does not depend on the width of the window.**
   The square root of the count, capped at fifty. Bins that changed as
   somebody resized would mean the same rows told two different stories.

9. **Downsampling is still only for drawing.** A dense line is reduced with
   min-max buckets rather than LTTB, so that the picture is the true vertical
   extent of what fell in each pixel column and no spike can be lost. Hovering
   and clicking will read the whole series, as the spike insisted (ADR-0004).

10. **The upright axis is named along the top, not turned on its side.**
    Rotated text is harder to read and a screen reader cannot turn its head.

11. **The key is above the plot and wraps.** Down the right it would take
    width from the data, and the data is what somebody is reading. One
    series has no key: there is nothing to tell it apart from.

12. **Series are coloured with Okabe and Ito's set.** It was chosen so that
    every pair of its colours can be told apart by the common forms of colour
    blindness. Two of them were made for paper and are lightened for a dark
    window. The application's accent is not used: an accent means "this is
    selected" everywhere else, and a series is not selected. Colour is never
    the only thing telling two series apart — the key names them.

13. **A chart is a tab of its own, over rows read once.** Changing the kind
    or an axis redraws from those rows. A chart that changed under somebody's
    hands as they tried three kinds of it would not be a chart of anything.
    The first hundred thousand rows are read, and the footer says so when
    there were more.

## Consequences

- The diagram's own SVG output is unchanged for opaque colours, which is
  every colour it draws with; its tests passed through the move untouched.
- A chart is rasterised at logical pixels, so on a HiDPI display the marks
  are scaled where the text is not. An export at twice the size is the way
  to a crisp file, and the size is the caller's to choose.
- Nothing hovers yet. The widget keeps the frame it was last drawn at, which
  is what turns a cursor position into a value, and that is T3.24.
- Only the tab in front can be charted, and only if it has rows. A chart of
  a chart is not offered.
