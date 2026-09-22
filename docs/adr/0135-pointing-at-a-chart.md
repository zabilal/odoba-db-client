# ADR-0135: Pointing at a chart

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.24 · **Requirements:** FR-11.4
**Packages:** `internal/ui/chart`, `internal/ui/shell`

## Context

FR-11.4 is the requirement the charting spike was run for: hover tooltips and
click-to-filter that stay accurate at a hundred thousand points. The spike
left the hit index and the two properties it insisted on (ADR-0004). T3.23
left a widget that keeps the frame it was last drawn at. This is the layer
between them.

## Decisions

1. **Each kind is pointed at the way its marks are shaped.** A line and an
   area are read by column, because somebody points at a day and not at a
   dot. A scatter is read by proximity, because its dots are the thing. A
   bar, a stack's layer, a histogram's bin and a pie's wedge are read by
   containment: the shape is large, and the cursor is either in it or not.

2. **A reading comes from the whole series and never from the drawing.** The
   drawing is reduced to the pixel grid (ADR-0134) and the index is built
   over the data. A tooltip reporting a point the reduction invented would
   show somebody a value that is not in their result, which for a tool whose
   purpose is reading data accurately is the one thing that must never
   happen.

3. **Between two series at one column, the one the cursor is nearest.** That
   is the line being pointed at; the nearest number in data units is not,
   when one axis spans ten years and the other spans nought to one.

4. **A bar is read where it stands.** Grouped bars are moved aside to stand
   beside one another and a stack's layer stands on what is below it, so
   both are pointed at and marked where they are drawn rather than where
   their own value would put them.

5. **Outside the plot reads nothing, with one pixel of slack.** A tooltip
   over the axis would name a value nothing there stands for. The slack is
   because the plot is whole pixels and the scales are not: without it the
   last point of every chart would be the one that could not be pointed at.

6. **The tooltip and the mark are the widget's, not the scene's.** A scene is
   what a picture is, and an exported picture has no cursor in it. They are
   Fyne objects drawn after the scene and never written to a file.

7. **Moving the pointer rebuilds only the tooltip.** What changed is a few
   words and a ring; the hundred thousand marks under them have not. The
   renderer keeps the drawing and the tooltip apart so that a pointer
   crossing a chart does not rasterise it sixty times a second.

8. **The mark is a ring on the point, not on the cursor.** A ring rather than
   a dot, so the mark does not hide what it marks; on the point rather than
   under the hand, so it says which of two close points was read.

9. **The tooltip flips rather than hanging off an edge.** Half a tooltip
   outside the window is unreadable, and it follows the pointer within one
   reading so it never sits under the hand.

10. **A click narrows the result to the value on the bottom axis at that
    mark.** That is what somebody who has just seen a spike wants to do next.
    The value is taken from the row rather than from the chart, because a
    chart holds numbers and a filter has to hold what the source gave: a
    moment, a decimal, a string. It is written as a chosen value, the same
    way the picklist writes one, so the filter row shows what was chosen.

11. **A histogram's bar does not narrow.** It counts an interval that is open
    at its top, and the filter row's range is closed at both ends, so
    narrowing to it would show rows the bar did not count. It says so rather
    than narrowing to something near enough.

12. **A pie's wedge carries the row it came from.** A wedge is a category, and
    narrowing to it is exact. The wedge that gathered a tail came from
    several and names none, so it narrows to nothing and says so.

13. **Where the bottom axis is the row number there is nothing to filter by,
    so a click brings the row forward instead.** A chart whose axis is the
    position of a row in a result says nothing about a value, and inventing
    a filter from the position would narrow to something nobody pointed at.

## Consequences

- Measured on this machine at a hundred thousand points: building the index
  is 0.66 ms, which happens once per drawing, and a reading is 305 ns for a
  line and 813 ns for a scatter — both far inside a frame. The risk the
  spike was run for is closed.
- A reading names the row it came from, so anything else that wants to trace
  a mark back to a row can.
- Nothing drags a box across a chart to select several points. `InRect` has
  answered that since the spike and nothing asks it; no task claims it.
- A chart of a query can be narrowed as well as a chart of a table, because
  the filter goes to the grid the rows came from and both have one.
