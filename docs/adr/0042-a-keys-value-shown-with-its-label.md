# ADR-0042: A foreign key's value shown with its row's label

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.15 · **Requirements:** FR-3.12 · **Packages:** `internal/ui/grid`, `internal/ui/shell`

## Context

FR-3.12 asks that a foreign key's value be shown with the human-readable
label of the row it refers to: `3 · Alice` rather than `3`. A table's tab
knows its foreign keys (ADR-0040). A grid cell draws one run of text, and
drawing every visible cell is held to a budget by the G0-1 gate.

## Decisions

1. **The label follows the value in the cell's text, after a middle dot**:
   `3 · Alice`. The dot, not a colour, says which part is the label (UX
   principle 14); a second run of text in another colour would change the
   drawing path the gate measures, for a difference the dot already makes.
   A label past 60 characters is cut, and the whole label is the cell's
   hint. Copying, exporting and editing take the value alone.

2. **A row is labelled by its table's first text column** that is neither
   in its primary key nor the column the key refers to. A table with no
   such column labels nothing, and a key of several columns is shown
   without labels.

3. **Labels are read as the grid's pages arrive**, off the UI goroutine:
   for each key, one browse of the table referred to, of its key and label
   columns alone, for the page's values not yet asked about. Each value is
   asked about once for as long as the tab is open, found or not.

4. **The grid asks its owner** (`TableGrid.Labels`) for each cell drawn, and
   the answer comes from memory; a grid without labels pays one nil check
   a cell.

## Consequences

- A label changed on the server shows the old one until the tab is opened
  again.
- A table with many keys reads labels with as many small browses per page.
