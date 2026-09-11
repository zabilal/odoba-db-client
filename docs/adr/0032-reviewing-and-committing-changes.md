# ADR-0032: Reviewing and committing changes

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.5, T2.6 · **Requirements:** FR-4.4, FR-4.5, FR-4.9, FR-1.8 · **Packages:** `internal/ui/shell`, `internal/app`, `internal/ui/grid`

## Context

The drivers plan a table's pending changes as bound SQL, and apply a plan
in one transaction (ADR-0031). The grid holds and marks the changes
(ADR-0027, ADR-0028). Nothing yet let a person see the statements, or
commit them.

## Decisions

1. **Review Changes shows the plan before anything runs.** It says how
   many statements will run, and that they run in one transaction (or one
   by one, where the source cannot promise that). Then it lists each
   statement: its line, its SQL, and its values, numbered as its
   placeholders are. Values are shown as SQL would write them, near enough
   to read, though the statement itself binds them. The review is a sheet,
   because it is the one thing to do at that point; Commit and Cancel
   close it.

2. **The Review Changes… button sits at the right of the footer** while
   changes are pending, beside the count of them. The command is also on
   the Edit menu and in the palette. It has no shortcut: ⌘S is Save
   Query's, and a menu shortcut cannot belong to two commands.

3. **Commit runs the plan off the UI goroutine.** The footer says
   Committing…, and Review Changes is off until the commit is done.

4. **Production asks again (FR-4.9).** The review says so beforehand.
   Commit opens "Change Data on Production?", and only on yes is the plan
   made again with that consent and applied. No leaves everything as it
   was.

5. **Once written, the changes are forgotten.** The new rows go, the rows
   are read again, and the footer says "Committed N changes".

6. **If the commit fails, nothing is forgotten.** The footer names the
   change that failed and why, and says whether anything was written. The
   row that change was for is selected: a new row by its place, a row read
   by its key if it is in memory (`Model.Find`, which fetches nothing).

7. **A read-only connection edits nothing (FR-1.8).** It has no gutter and
   no editors. Before this, its rows could be edited and then refused at
   commit.

## Consequences

- Closing a tab with pending changes still drops them without asking;
  T2.7 asks.
- A failing row that is not in memory is named in the footer, but not
  selected.
- Nobody has seen the review sheet in a real window yet.
