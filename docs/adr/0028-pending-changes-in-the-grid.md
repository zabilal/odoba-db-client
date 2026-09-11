# ADR-0028: Pending changes in the grid

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.2 · **Requirements:** FR-4.3, UX principle 9, NFR-A2 · **Packages:** `internal/ui/grid`, `internal/model`, `internal/ui/shell`

## Context

T2.1's `app.Pending` holds a table's edits, each kept by its row's key
(ADR-0027). FR-4.3 asks that they be marked in the grid as added, modified
or deleted. The palette already had a text colour and a tint for each state,
checked against WCAG AA. The grid had only a placeholder from spike W1, a
hook that tinted a row by its place. The design system allows colour to
carry meaning, never alone.

## Decisions

1. **The grid asks `grid.Changes` about a row, not a place.** It asks how a
   row stands and what a cell's new value is. So its marks follow a row
   through a sort or a filter, as `app.Pending` keeps them. `*app.Pending`
   is one: `RowState` moved into `internal/model` so the grid need not
   import `internal/app`.

2. **A changed cell shows its new value**: what will be written, not what
   was. It is bold, on the modified tint. With the pointer resting on it, it
   says what it was ("Changed from Ada Lovelace"). The row's other cells are
   as they were.

3. **A deleted row is struck through**, every cell, on the deleted tint to
   the grid's edge. This uses `TextStyle.Strikethrough`, new in Fyne 2.8. A
   new row is on the added tint; rows are added in T2.4.

4. **A gutter before the first column marks each changed row**: a dot when
   changed, a minus when deleted, a plus when new. Each is in its state's
   colour on its tint, and says what it means in words when the pointer
   rests on it ("2 cells changed", "To be deleted", "New row"). The gutter
   is Fyne's header column, which stays in view when the grid scrolls
   sideways and does not change the columns' indexes. A grid has it
   whenever it can show changes: on a table whose rows can be told apart,
   from the moment it opens, so the first edit moves nothing. Query results
   and tables with no key have none.

5. **On the selection, a change's text takes the selection's colour.** The
   selection's tint takes the change's place, and the deleted colour is not
   legible on it: 4.33:1 in light and 3.37:1 in dark, under AA's 4.5:1. The
   bold and the line stay.

6. **The gutter and the filler sort nothing.** Fyne makes the gutter's
   cells from the column headers' template and moves cells between the two,
   so the gutter sets everything a column header does. Tapping the filler's
   header used to ask for a sort by column -1, which read the rows again
   for nothing. `ToggleSort` now ignores any column below 0.

7. **With nothing changed, marks cost nothing.** `app.Pending` answers
   without reading a row's key when it holds no changes. With changes, the
   G0-1 gate measured 379µs per 1,800-cell viewport, against 160µs with
   none; the budget is 4ms. The gate now measures both.

## Consequences

- Nothing edits yet: cell editors come in T2.3, insert and delete in T2.4,
  and reverting in T2.7. Each refreshes the grid after changing the
  pending changes.
- The pending changes are the table tab's (`tab.pending`). They stay
  through a sort or a filter, which reads the rows again. Closing the tab
  drops them without asking; T2.6 and T2.7 must ask first.
- A row still loading, or past the end of the rows, has no mark.
- The grid's cells have no accessibility labels, so a screen reader hears
  neither a mark nor its words. That is open for the grid as a whole.
- The marks need the font to have • and − (U+2022, U+2212), which the
  bundled font does. Nobody has seen them in a real window yet.
