# ADR-0030: New rows, duplicates and deletions

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.4 · **Requirements:** FR-4.2, FR-4.3 · **Packages:** `internal/ui/grid`, `internal/app`, `internal/model`, `internal/ui/shell`

## Context

FR-4.2 asks for rows to be inserted, deleted and duplicated. `app.Pending`
keeps new rows apart from the rows read (ADR-0027), and the grid marks a
new row and a deleted one (ADR-0028). But the grid takes its rows by index
from its model, which held only rows read, so a new row had nowhere to be
shown.

## Decisions

1. **New rows are shown first, before the rows read**, in the order added.
   The grid's model holds them (`Model.SetAdded`). Every index it takes or
   gives counts them first, so copying, exporting, the cell viewer and Set
   to NULL reach new rows with no change of their own. Its total stays the
   count of the rows read, while its extent counts both. A sort or a
   filter keeps them. They go at the top because the end of the rows is
   often not known (counting can mean a full scan), and a row put there
   could not be found.

2. **A column a new row is not given is DEFAULT, not NULL.** It holds
   `model.Default`, drawn as NULL is, as a word in italic, and it is left
   out of the INSERT so the server fills it in. An editor opens it empty,
   saying DEFAULT.

3. **Insert Row adds an empty new row** after any others and selects its
   first cell, ready to type into.

4. **Duplicate Rows copies each selected row**, its pending edits included,
   but not its key. A copy with the same key could not be inserted, so the
   server gives it one or it is typed. A column the row was not given
   stays not given.

5. **Delete Rows marks each selected row read to be deleted** (struck
   through, ADR-0028). It takes out the new rows selected, which nothing
   had written, working from the last so that each keeps its place until
   it goes.

6. **A new row's cells are edited as any other's.** The grid sends them to
   `OnEditAdded` rather than `OnEdit` (`TableGrid.SetValue`). A new row's
   value is its own, never a row read's change, even when the two have
   the same key.

7. **The footer counts the rows read apart from the new ones.** New rows
   count among the pending changes.

## Consequences

- The commands are on the Edit menu and in the palette, with no shortcut.
  Delete and ⌘⌫ are a text field's keys too, and a menu shortcut would
  take them from every field (ADR-0011 §2).
- Rows' indexes shift as new rows come and go, and the selection stays
  where it was.
- A deletion is undone by reverting its row (T2.7). What the changes write
  is previewed in T2.5 and committed in T2.6.
