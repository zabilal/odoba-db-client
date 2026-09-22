# ADR-0132: A chart that will not lie

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.21 · **Requirements:** FR-11.1
**Packages:** `internal/ui/chart`

## Context

The W4 spike settled how marks reach a screen — rasterised into one image
rather than drawn as thousands of canvas objects (ADR-0004) — and gave
scales, ticks, downsampling and a hit index. What it did not give is the
shapes. FR-11.1 asks for line, bar, stacked bar, area, pie, scatter and
histogram.

A chart is read as a statement about data. A mark nobody's row put there is a
lie told in a picture, which is harder to catch than one told in a number.
Most of the decisions below are about that.

## Decisions

1. **A kind that cannot honestly draw the data refuses, and says what to draw
   instead.** A pie of negative values has no share of a whole; a stack of
   +5 and −5 is a bar of nothing standing for two numbers that are not
   nothing. Somebody who asked for one wants a chart, not a no, so the
   refusal names the kind that would work.

2. **A bar's axis reaches zero and a line's does not.** A bar's length is its
   value, so an axis starting at 90 draws 91 as ten times 90.1. A line is
   about change, and cutting the axis is how change is seen. That is the
   whole rule, applied by kind: bars, stacks, areas and histograms are drawn
   as lengths from the axis; lines and scatters are not.

3. **A bar reaches the axis, not the foot of the chart**, so a negative value
   hangs below it. A bar that always grew upward from the floor would draw
   −5 and +5 the same.

4. **A stack is as tall as its parts together.** Its axis is sized for the
   whole column, and each layer stands on what is below it.

5. **A histogram's bin edges are round numbers.** Bins running from 3.7194 to
   12.8831 are bins nobody can say anything about, and a histogram exists to
   be said something about. The width is chosen the way an axis's ticks are
   and the first edge is a multiple of it. A value never falls in a bin that
   ends at it, so the largest value belongs to the last bin rather than to
   none.

   Its bars touch, because the values between two edges are one continuous
   run. Gaps would read as categories, which is the other chart.

6. **A pie gathers its tail rather than dropping it.** Past a dozen wedges a
   pie is a colour wheel, so the rest are gathered into one that says how
   many it holds. Dropping them would be a chart that quietly left data out,
   which is the one thing this must not do. Wedges run largest first from the
   top, clockwise, because that is how every pie anybody has read is drawn.

7. **A mark too small to round to a pixel is still drawn.** Drawing nothing
   would say the value is absent, and absent is a different statement from
   small.

8. **A chart of no rows is an answer, not a failure.** A query that returned
   nothing has no chart, and saying so is the answer.

## Consequences

- Nothing here is a Fyne object: these draw into the same NRGBA buffer the
  spike's scatter and line do, blending so that overlap reads as density.
- Three pieces of code came out while the mutations were being written,
  because no test could tell the absence of any of them: a spare histogram
  bin that made the clamp on the largest value unreachable, a guard on an
  area of one point that the loop already said, and a special case for
  every-value-the-same that the general path handles better — it gave an
  interval from a value to the next float after it, and the general path
  gives two round numbers.
- `Check` and `Extent` are the seam T3.22 will pick columns against: what a
  kind needs is stated once, rather than being decided again by whatever
  chooses the columns.
