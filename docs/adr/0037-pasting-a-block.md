# ADR-0037: Pasting a block of cells

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.10 · **Requirements:** FR-4.10, FR-4.3 · **Packages:** `internal/ui/grid`, `internal/ui/shell`

## Context

FR-4.10 asks that a block of TSV or CSV on the clipboard be pasted into the
grid. Copy Cells writes TSV, quoted as CSV is, which is also what
spreadsheets put on the clipboard. A grid's rows are edited as pending
changes (ADR-0027 to ADR-0036), new rows shown before the rows read.

## Decisions

1. **The clipboard's text is read by its shape** (`grid.ParseBlock`). Any
   tab makes it TSV, quoted as CSV is. Lines without a tab are CSV only
   when there are several and each has the same number of fields, more
   than one. Otherwise each line is one cell, so a comma never splits a
   single value ("Smith, John"). One line break at the end is none.

2. **A block is written from the active cell**, rightwards over the columns
   as shown and downwards, as pending changes: nothing reaches the server
   before Commit. Each cell is read as its column's type, as typing it
   would be, and the block pasted is selected.

3. **One value pasted over several selected cells is written into each**,
   as Set to NULL writes NULL.

4. **A block begun on a new row fills new rows**, adding them as it needs.
   An empty cell leaves a new row's column as it is, so the server gives
   its default. **A block begun on a row read fills rows read**, reading
   those not yet loaded off the UI goroutine. It never adds rows among the
   rows read: what reaches past the last row is not pasted.

5. **What a paste cannot write is left, and the rest is written.** It says
   how many cells it pasted, and why the first it did not was not: past the
   last row or column, a value its column cannot hold, a row to be deleted.

6. **A paste takes up to as many rows as Copy does** (100,000).

7. **⌘V is the editor's, so the focused grid takes it** (`OnPaste`), as it
   takes ⌘C. Paste Cells is on the Edit menu without a chord.

## Consequences

- To paste rows in as new ones, insert a row and paste on it.
- An unquoted TSV field that starts with a quote is read as quoted, as a
  spreadsheet would read it.
- A value too long for a column, or a key that repeats, is found at
  Commit, by the server.
