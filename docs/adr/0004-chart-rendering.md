# ADR-0004: Charts — native marks below a threshold, a rasterised data layer above it

**Status:** Accepted · **Date:** 2026-09-10
**Spike:** W4 (TASKS.md T0.55–T0.58) · **Gate:** G0-4 **PASSED**

## Context

Charts exist to understand a result set, not to build dashboards (NG-2). The
requirement that carries the risk is FR-11.4: hover tooltips and click-to-filter
that stay accurate at 100 000 points. T0.55 planned to render `gonum/plot` or
`wcharczuk/go-chart` into a `canvas.Image`.

## Rejected: gonum/plot and go-chart — on design grounds, not measurement

These were **not benchmarked**. They were rejected because of where they put
the text. Both render the whole chart — axes, tick labels, legend — into one
bitmap, which means:

- text is not drawn in the application's type or palette, so a chart would be
  the one surface that ignores ADR-0006;
- text is not crisp on HiDPI displays unless the whole image is re-rendered at
  device scale;
- any hover highlight or resize re-renders the entire image, text included;
- they are a second styling system running parallel to the theme.

If a speed comparison is wanted for the record, it is a short test to add. The
decision below does not rest on speed.

## Measurement: native marks versus one rasterised image

Both approaches go through the same capture path. The empty-window floor
(14.0 ms) is subtracted from each, because spike W1 showed a software capture
can be mostly the rasteriser itself.

| Points | Raster (draw + capture) | Native (build + capture) |
|---|---|---|
| 1 000 | 20 ms | ≈ floor (reads −1 ms; noise) |
| 10 000 | 21 ms | 43 ms |
| 100 000 | 28 ms | **501 ms** |

Pure rasterisation, without the capture step: **2.0 ms per 100 000 points, 0
allocations.**

**Reading it correctly.** Raster cost is nearly constant because it is mostly
the test driver blitting one full-canvas image in software. On the GL build
that blit is a single textured quad. Native cost is about 50 µs per object and
grows linearly. That cost is object creation, layout and per-object drawing,
so it does **not** go away on GL, unlike W1's rasteriser floor. The crossover
measured here, somewhere between 1k and 10k marks, is therefore pessimistic
for raster: GL makes raster cheaper and leaves native where it is.

At 1k marks native wins outright. The raster path pays for a full-canvas image
blit, while the native path draws a thousand small shapes.

## Decision: a hybrid

- **Chrome is always native.** Axes, ticks, labels, legend and tooltip are Fyne
  canvas objects in the theme's type and palette, so they stay crisp and
  consistent with every other surface.
- **Data marks are native up to a threshold, then rasterised.** Bars, pie
  slices and typical line series stay individual objects: few marks, each
  addressable. Beyond **2 000 marks** the data layer is drawn into one
  `canvas.Image`.
- **Dense lines are downsampled first.** A line is reduced with LTTB to twice
  the plot width before drawing. That takes 246 µs for 100 000 points down to
  2 400. At that density a hundred points share every pixel column, so drawing
  them all gives the same picture painted a hundred times.
- **Marks are alpha-blended**, so overlap shows up as density. The dense core of
  a cluster reads darker than its fringe, which is what a scatter plot is for.

## Hit testing — the gate

| | Result | Budget |
|---|---|---|
| Index build, 100 000 points | **14.3 ms** | 250 ms |
| Query, mean | **428 ns** (benchmark 495 ns, 0 alloc) | 50 µs |
| Query, worst | 5 µs | — |
| Disagreements with brute-force oracle | **0 of 20 000** | 0 |

A uniform pixel grid (12 px cells, roughly a cursor's reach) makes a scatter
query touch at most a 3×3 block of cells. Line, area and bar charts resolve a
tooltip by column instead, using a binary search over X, because the user is
pointing at a time, not at a dot.

Accuracy is defined against an oracle, not against "looks close". The index has
to return the **same distance** as a linear scan, which proves no closer point
was missed. Ties at equal distance may resolve either way.

## Two properties that are easy to get wrong, both now tests

**Hits resolve against the full data, never the drawn set.** A tooltip has to
report a value that exists in the user's result set. If the index were built
over the downsampled series, every tooltip would show a proxy.
`TestHitsResolveAgainstFullDataNotDownsampled` asserts that some hits land on
rows LTTB discarded. If every hit landed on a drawn point, the index would have
to be looking at the wrong data.

**Nearest is measured in pixels, not data units.** When X spans ten years and Y
spans 0 to 1, the nearest point in data units is almost never the one under the
cursor. `TestNearestIsMeasuredInPixelSpace` builds that case and checks the
index picks the point the user can see.

LTTB was chosen over every-nth sampling or bucket averaging because it keeps
peaks. In a query result the spike is usually the row the user is looking for.
`TestLTTBPreservesASpike` plants a single row at 1000× the baseline among
100 000 points and requires it to survive. `MinMaxBuckets` is the pixel-exact
alternative for dense time series, and a test checks that every point lies
within its column's drawn extent.

## Consequences

- **SVG export (FR-11.3) needs a vector path for rasterised charts.** A bitmap
  layer cannot become SVG marks. Export re-emits the marks as SVG from the data
  (LTTB-reduced for lines). That happens offline, so per-frame cost does not
  apply.
- **The 2 000 threshold is a starting point.** It comes from a crossover
  measured in software. Re-tune it on the GL build when charts are built
  properly in Phase 3.
- The map view (FR-11.5) is out of scope, as REQUIREMENTS already records.
