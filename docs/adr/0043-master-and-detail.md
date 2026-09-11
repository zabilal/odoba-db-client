# ADR-0043: Master and detail

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.16 · **Requirements:** FR-3.13 · **Packages:** `internal/ui/shell`

## Context

FR-3.13 asks to expand a row into its child rows in a nested grid. A
table's tab knows the keys of other tables that refer to it (ADR-0041),
and Show Referring Rows opens those rows in a tab of their own. Keeping
the rows in view while their children are read calls for a panel, not a
tab (UX principle 4).

## Decisions

1. **The child rows are in a panel under the grid**, in a split, not
   nested inside its rows: a row that grew a grid of its own would move
   every row under it, and Fyne's table has no row of another height.
   View ▸ Detail Rows shows it, and hides it again.

2. **The panel wraps the grid's place, not the grid**, so the cell viewer
   and the form view take their turns there as before, with the panel
   under them.

3. **It shows the rows of one referring table at a time**, those whose key
   holds the active row's written values, picked from the tables that
   refer to it, each named by its table and its key's columns.

4. **It follows the active row.** Another row of the same table is read
   into the same grid, off the UI goroutine; another table into a grid of
   its own. A row not written, or with nothing in the key, has nothing
   referring to it, and the panel says so.

5. **The panel's rows are read, not edited.** Open in Tab opens them in a
   tab of their own, filtered in its filter row, where they are edited.

## Consequences

- Only one level shows at a time: a child row's own children are opened
  from its tab.
