# ADR-0039: The form view

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.12 · **Requirements:** FR-3.10, FR-4.1 · **Packages:** `internal/ui/shell`

## Context

FR-3.10 asks for one record laid out vertically, for wide tables and for
documents. A grid reads a row across, and a table of forty columns cannot
be read across at any width. The cell viewer (ADR-0016) shows one value
beside the grid, not a row.

## Decisions

1. **The form view takes the grid's place.** View ▸ Form View shows the
   active row, a field for each column the grid shows, in the grid's
   order, the column's type under its value. A side panel would be as
   narrow as the problem it is meant to solve. Toggled again, it gives the
   grid its place back, with the cell viewer if that was open; the viewer
   and the form take turns in that place.

2. **It follows the grid's active row, and moves it.** Previous Row and
   Next Row move the grid's selection, so the grid is on the same row when
   it comes back, in the column that was active or the last left. A row
   not in memory is read off the UI goroutine, as the viewer reads it; only
   the latest read lands. A column hidden or shown changes the fields at
   once.

3. **A field is typed into where the grid edits the row.** Return, or
   leaving the field, keeps what was typed as a pending change, read as
   the column's type, as typing into a cell is (ADR-0029). Escape puts back
   what the field started with. What cannot be written is said under the
   field, and stays typed. Moving to another row keeps what was typed
   first. A row to be deleted, and rows that are not edited, are shown,
   not typed into.

4. **What has changed is said in words**: a field says "changed" under its
   value, and the row says "changed" or "to be deleted" beside its number
   (colour is never the only sign).

## Consequences

- A long value is shown as the grid shows it, cut at 200 characters; the
  cell viewer shows it whole, and edits it at length.
- Documents from sources without columns will need a form of their own
  when those sources arrive.
