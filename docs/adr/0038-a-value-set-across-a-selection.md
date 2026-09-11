# ADR-0038: A value set across a selection

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.11 · **Requirements:** FR-4.11, FR-4.3 · **Packages:** `internal/ui/shell`

## Context

FR-4.11 asks for one value set across a selection. Pasting one value over
several selected cells already wrote it into each (ADR-0037), but only
into the rows loaded, and only from the clipboard: a whole column selected
reached no further than the rows drawn so far. Set to NULL had the same
limit.

## Decisions

1. **Set Value… asks for one value and writes it into every selected
   cell**, each read as its column's type, as typing it would be: empty is
   NULL, or the empty string in a column of text. The changes are pending,
   as every edit's is, until Commit.

2. **The rows a selection reaches are read first**, those not loaded off
   the UI goroutine, so a whole column is set and not only the rows drawn;
   up to as many rows as Copy takes. Set to NULL, and one value pasted over
   a selection, read them the same way: the three share one fill.

3. **What cannot be written is left, and the rest is written.** It says
   how many cells it set, and why the first it did not was not.

4. **It is on the Edit menu, beside Set to NULL**, without a chord.

## Consequences

- A whole column of a large table set holds one change per row until it
  is committed, and the review lists a statement for each.
- Past Copy's limit, an UPDATE in the query editor is the way.
