# ADR-0129: A key is marked, and a line has ends

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.18 · **Requirements:** FR-8.3, FR-8.1, FR-8.2
**Packages:** `internal/ui/diagram`, `internal/ui/canvas`, `internal/ui/shell`

## Context

The widget drew a box, a title and a list of column names (ADR-0128). FR-8.3
asks for columns, keys and cardinality — and there was still no way to open a
diagram at all, so the way in belongs here too.

## Decisions

1. **A key column is marked, not merely emboldened.** Weight alone is not a
   difference somebody can see at a glance down a column of names, and colour
   alone is not one everybody can see. A mark in a gutter to the left of
   every name is a shape, and the gutter is there whether or not a row has
   one, so names still line up.

2. **A column's type is drawn to its right, in the quieter of the two label
   colours.** It is there to be read when looked for, not to compete with the
   name.

3. **A crow's foot at the many end and a bar at the one end.** It is the
   convention every diagram of this kind uses, and a convention somebody
   already knows beats a legend they have to read. The child's end says what
   the catalogue said — many for an ordinary key, one where the child's own
   side is unique (ADR-0127) — and the parent's end is always one, because a
   foreign key points at a single row.

4. **Marks are not drawn where they would be smudges.** Below the zoom at
   which a title is legible, a mark at the end of a line is three or four
   pixels of noise, and the whole point of that zoom is the shape of the
   schema.

5. **A node taller than a dozen rows says how many it left out.** A table of
   sixty columns drawn in full is taller than the diagram is wide, and a
   diagram somebody has to scroll vertically through one box is not a
   diagram.

6. **What was arranged is applied before the layout, not after.** Then
   everything else is arranged around it, rather than laid out once and
   talked over.

## What it found

`canvas.Layout` moved a pinned node to the origin whenever that node was a
component of its own — which is every table nobody points at, of which a
schema has dozens. So a table dragged out of the shelf of unreferenced tables
and into the picture went straight back to the shelf on the next layout,
which is the one arrangement FR-8.2 exists to keep.

The spike's own comment promised the opposite: "Pinned nodes keep their
positions and still exert force on others, so a user's manual arrangement
survives a re-layout." Nothing depended on it until now, so nothing had
noticed.

## Consequences

- A diagram opens from the same node a comparison does — a schema or a
  database — and reads the same snapshot, so it costs what comparing costs
  and no more.
- There is a Lay Out Again, which unpins everything and forgets the
  arrangement. It is the way back from having moved things into a mess, and
  without it the only way back would be to find the file.
- `erd` supports a subset of tables and the window does not ask for one yet.
  The footer's line about relationships leading outside a diagram is written
  and cannot fire until T3.20's neighbourhood filter gives a way to ask.
- Nothing exports a diagram yet: T3.19 is PNG and SVG.
