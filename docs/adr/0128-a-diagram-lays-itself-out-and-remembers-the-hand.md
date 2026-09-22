# ADR-0128: A diagram lays itself out, and remembers the hand

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.17 · **Requirements:** FR-8.2, NFR-P13
**Packages:** `internal/ui/diagram`, `internal/app`, `internal/store/localdb`

## Context

The canvas spike built a viewport that pans, zooms, culls and hit-tests, and
was benchmarked without a window (ADR-0007). FR-8.2 asks for pan, zoom and
drag, with the layout persisted. This is the first time any of it is drawn on
a screen.

## Decisions

1. **The widget is deliberately thin.** It turns events into calls on a
   viewport and a graph, and draws what the viewport says is worth drawing.
   Everything about where a box goes and what a line does between two of them
   stays in `internal/ui/canvas`, which has no Fyne in it.

2. **Only what is on screen becomes a Fyne object.** That is the whole of why
   a two-hundred-table schema pans at all, and it is the rule the data grid
   already follows (NFR-P13). Below the zoom where a label is legible it is
   not drawn: a whole-schema overview is exactly the view where every node is
   visible, so without that, culling saves nothing at the one zoom that needs
   it most.

3. **A diagram is laid out afresh every time, and what somebody moved is put
   back on top.** Keeping every position instead would mean a diagram never
   laid itself out again: a table added to the schema would land wherever
   nothing else was, while the rest stayed as they were read weeks ago.

   A node put back is pinned, which is what keeps the next layout from
   undoing it — the canvas has had `Pinned` since the spike for exactly this.
   A kept position for a table the schema no longer has is ignored: a table
   dropped is a position nobody needs.

4. **Where somebody was looking is kept too**, so reopening a diagram shows
   the part of it they were reading. A zoom outside what the canvas allows is
   refused rather than clamped: it means the file was written by something
   else, and guessing at what it meant is worse than showing the diagram
   whole.

5. **An arrangement that cannot be read is an arrangement nobody has.** It is
   where boxes were put; losing it costs a rearrangement rather than anything
   a person cannot do again, so it is forgotten rather than reported.

6. **What is being dragged is decided once, on the first step.** Deciding
   again as the pointer moves would have the view stop and a box leap the
   moment one crossed the other. A box follows the hand at any zoom, because
   a drag moves it by as many graph units as the pointer moved pixels,
   divided by the zoom.

7. **The palette is given to the widget, not looked up.** A widget that
   reached for the current theme could not be drawn twice in two appearances,
   and could not be tested without an application. The grid's cells already
   work this way.

## Consequences

- An arrangement is kept per connection and per diagram, so two diagrams of
  one database do not overwrite each other.
- A drag says the diagram changed once, when it ends, rather than at every
  step — otherwise a single drag would write the arrangement fifty times.
- Nothing in the window opens a diagram yet: there is a widget and a store
  and no command. T3.18 is what a node draws, and the way in comes with it.
- The renderer rebuilds its objects on every frame that changes. The spike
  measured 24µs per frame at overview zoom, which is the budget this has to
  stay inside; it is not yet measured with real Fyne objects in it.
