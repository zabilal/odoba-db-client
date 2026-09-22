# ADR-0130: A diagram said once, and drawn three ways

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.19 · **Requirements:** FR-8.4
**Packages:** `internal/ui/diagram`, `internal/ui/shell`

## Context

FR-8.4 asks for a diagram exported as PNG or SVG. The widget drew Fyne
objects directly from a graph and a viewport, which is one drawing; a raster
and a vector file would have been two more.

## Decisions

1. **The drawing is said once, as shapes, and each of the three turns those
   into its own form.** Saying it three times would mean three drawings that
   could drift, and an exported picture that differs from the one somebody
   exported it from is worse than no export at all.

   The shapes are boxes, lines, text and dots in screen coordinates, and the
   package that makes them imports no Fyne. This is the same rule the
   catalogue readings follow: one description, several consumers (ADR-0120).

2. **A PNG is the widget rendered off-screen, not a second drawing.** Fyne's
   software renderer takes the same widget and gives back an image, so the
   file is what the window shows, down to the font. It captures at the
   object's minimum size, which is how the picture asks to be drawn at the
   size of the whole diagram.

3. **An SVG writes text as text.** A name in an exported diagram can then be
   searched for and copied, and the file is small enough to put in a
   document. Trailing text is told to end at a point rather than measured and
   moved, so the writer needs no font metrics at all — the one place the two
   backends legitimately differ, because each does the same thing the way its
   format does it.

4. **What is exported is the whole diagram, not what is on screen.** Somebody
   exporting a picture wants the picture, not their scroll position, and a
   file cropped to a window is a file they have to make again. Nothing is
   drawn as selected either: a selection is something somebody is doing, not
   something about the schema.

5. **A diagram too large to draw at its natural size is drawn smaller.** Two
   hundred tables at natural size is tens of thousands of pixels across,
   which is a file nothing will open — and somebody asking for the whole
   picture has already said they want it whole.

6. **The format follows the name.** Somebody who types `.svg` means SVG, and
   asking again in a second dialog would be asking a question they have
   answered.

## What it found

Tightening two tests against the new code found two defects in it.

- The export's pan had the wrong sign. `Viewport.Pan` is the graph-space
  point shown at the screen origin, and the exporter was setting it to the
  negated corner scaled by the zoom — so the diagram sat a margin off the top
  left of every exported picture. The test that caught it asks where the
  nearest box is, rather than only whether the labels are present.
- The PNG came back 120 by 90 — the widget's minimum size — because
  `software.Render` captures at the object's minimum and the exporter had
  resized the widget instead. The test now asks that the image is the size of
  the scene.

## Consequences

- The Fyne renderer decides nothing: what is visible, how finely and in what
  colours are all settled before it is called. It is a translation.
- An SVG names its font as `sans-serif` rather than the theme's, because a
  file that carries a font name nothing else has is a file that renders
  differently everywhere. A PNG has no such problem, and does use the theme's.
- Exporting writes one error path and one success line, because two error
  checks meant one of them was reached by no test.
