# ADR-0127: A diagram draws what the catalogue says

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.16 · **Requirements:** FR-8.1, FR-8.3, FR-8.5
**Packages:** `internal/ui/erd`

## Context

The canvas spike (W3, ADR-0007) built a graph model, a layout, edge routing
and a viewport, and is deliberately free of any idea of a database: one
canvas serves the ER diagram and the visual query designer, which differ in
what a node means and not in how either is drawn.

FR-8.1 asks for a diagram generated from a schema or a subset of its tables.
That is the half that knows about databases.

## Decisions

1. **It lives in a package of its own, between the model and the canvas.**
   `internal/ui/erd` imports both and knows what the canvas must not: a table
   is a node, a column is a port, a foreign key is an edge. Nothing in it
   imports Fyne, so a diagram is built and checked without a window.

2. **It draws what the catalogue says and nothing it has inferred.** A join
   table between two others is two foreign keys, so it is drawn as two.
   Calling it a many-to-many would be this program's opinion about a pattern,
   drawn as though the server had said it — and the pattern is only a
   convention, so it would be wrong sometimes and unfalsifiable always.

3. **An edge attaches to the columns the key is about**, not to the boxes.
   A table with twenty columns should show which two the relationship joins.

4. **A relationship is one-to-one when the child's own side is unique.** A
   foreign key whose columns are the child's primary key, or a unique
   constraint on exactly them, or a unique index over exactly them, can hold
   at most one row per parent. Everything else is one-to-many.

   A unique index over only some rows guarantees nothing about the rest, so
   it says nothing here. An index over an expression is not a guarantee about
   a column and needs no check of its own: an expression column carries no
   name, so the empty name it contributes matches nothing a key names.

5. **A subset says what leads out of it.** A diagram of three tables whose
   keys point at a fourth is honest about the edges it dropped, because a
   table whose relationships lead off the page would otherwise look
   unrelated.

6. **A table is named by its schema and its name.** Two schemas may hold
   tables of one name and a diagram may show both. A bare name is accepted
   only where the database has one schema: elsewhere it is ambiguous, and a
   diagram that quietly drew the wrong table of two would be worse than one
   that drew neither.

7. **A table that points at itself gets an edge to itself.** It is a real
   relationship and a common one — a row's parent, a comment's reply — and
   dropping it for being awkward to route would be dropping something true.

8. **Views are drawn only when asked for.** They have columns and are worth
   seeing, but nothing points at a view, so a schema with many of them draws
   mostly boxes with no lines.

## Consequences

- Nothing here lays out or draws anything: the canvas does both, and a
  diagram is a `canvas.Graph` like any other. Measuring the nodes is done
  here because a node with no size cannot be laid out.
- The neighbourhood search FR-8.5 needs is a walk over the full graph, so it
  is built once and thrown away. On a schema large enough for that to matter,
  the diagram being built is the larger cost.
- Nothing in the window opens a diagram yet. T3.17 is pan, zoom and drag with
  a persisted layout, and T3.18 is what a node draws at each zoom.
