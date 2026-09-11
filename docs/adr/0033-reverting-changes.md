# ADR-0033: Reverting changes, and asking before they are lost

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.7 · **Requirements:** FR-4.6 · **Packages:** `internal/ui/shell`, `internal/app`

## Context

A table's changes stay pending until they are committed (ADR-0027,
ADR-0032). FR-4.6 asks for a cell, a row or the whole changeset to be
reverted. Closing a tab or quitting dropped pending changes without
asking.

## Decisions

1. **Revert Cells undoes each selected cell's change.** A row read's cell
   shows what was read again. A new row's column goes back to not given,
   so it is DEFAULT again. A row left with no change is unchanged.

2. **Revert Rows undoes every change to each selected row.** A row read is
   as it was read, and a deletion is undone too. A new row goes.

3. **Discard All Changes… asks first**, then forgets every change, the new
   rows included, and the footer says how many went.

4. **Nothing pending is lost without asking.** Closing a tab with changes
   asks "Close and Discard Changes?", and quitting asks "Quit and Discard
   Changes?". Each says how many changes would go, and names the tasks
   running when there are any.

5. **No shortcuts.** ⌘Z is a text field's undo, and a menu shortcut would
   take it from every field. Reverting is not undo either: it puts back
   what was read, not the step before.

## Consequences

- There is no undo of a single edit. Reverting a cell forgets every edit
  to it since it was read.
- A change is reverted through the selection and the Edit menu, not from
  its mark in the gutter.
