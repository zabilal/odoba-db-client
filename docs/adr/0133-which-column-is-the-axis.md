# ADR-0133: Which column is the axis

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.22 · **Requirements:** FR-11.2, FR-11.1
**Packages:** `internal/ui/chart`

## Context

A result is a table and a chart is not. Something has to say which column
runs along the bottom, which are drawn, and which splits the drawing into
several. FR-11.2 asks for that with sane auto-detection.

## Decisions

1. **It guesses, and everything it guesses can be changed.** Asking first is
   a form to fill in before seeing whether the chart was worth having. It
   will be wrong sometimes, and a chart nobody can correct is a chart nobody
   trusts, so the roles are a value the window edits rather than something
   worked out again each time.

2. **The order is what somebody would do by eye.** A moment in time goes
   along the bottom, because a result ordered by one is almost always a
   result about change. Failing that, a column of labels: the categories of a
   bar chart. Then a column of a few repeated labels splits the numbers into
   series, looked for after the axis so the two are never the same column.
   Everything else that is a number is drawn.

3. **What cannot be drawn is left out rather than pressed into a role.** A
   chart of an identifier is a chart of nothing. A column with a different
   value in nearly every row is an identifier and not a category, however it
   is named and whatever its type — which is also the point past which the
   colours would have to repeat.

4. **A category is given a position, and its name comes back to be drawn.**
   An axis is a line and a word is not a place on one, so the words become
   first, second, third and the labels are handed back for the axis to carry.

5. **A row that cannot be read is left out, and how many is said.** A chart
   drawn over gaps nobody was told about lies by omission, and the one thing
   this must not do is that.

6. **A line is sorted along its axis and nothing else is.** A line drawn in
   the order rows arrived zigzags wherever the result was not sorted, which
   reads as data going backwards in time. A bar chart's order is the order
   somebody asked for.

7. **Neither an infinity nor a not-a-number is a place on an axis.** Drawing
   one would move every other point to make room for it, so they are read as
   values that cannot be drawn — which is what they are.

8. **A null is named rather than left blank.** A chart cannot leave a gap in
   a legend, and an empty name would read as a name somebody chose.

## Consequences

- A moment is read as the seconds since the epoch, which is what makes an
  axis of times the same kind of axis as one of numbers. A decimal is read
  back from the text it is carried as: a chart is drawn in pixels, so the
  precision that matters is gone before it is drawn.
- Every point keeps the row it came from, which is what FR-11.4's
  click-to-filter will need and what the spike's downsampling was careful to
  preserve.
- A fallback came out while the mutations were being written: it turned a
  numeric axis into a drawn column, and could never fire, because an axis is
  only ever taken from a column that orders or one that labels and a number
  is neither.
- Nothing in the window draws a chart yet. The kinds, the shapes, the roles
  and the reading all exist and no tab opens one.
