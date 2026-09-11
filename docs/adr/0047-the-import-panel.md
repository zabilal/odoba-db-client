# ADR-0047: The import panel

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.19 · **Requirements:** FR-10.4, FR-10.5 · **Packages:** `internal/ui/shell`

## Context

A file is read as rows (ADR-0044), how to read it is found from it
(ADR-0045), and its columns are paired with a table's and its values made
theirs (ADR-0046). A person needs to see and correct all three before
anything is written.

## Decisions

1. **File ▸ Import… on a table opens the import in a tab of its own**, not
   a dialog (UX principle 4): the table stays in its tab, and the import
   can be left and come back to. It is offered on a table whose columns
   are known, on a connection that edits; the file is chosen with the
   platform's dialog, and how to read it is found off the UI goroutine.

2. **The panel shows the options as found, to be corrected**: format,
   encoding, delimiter, whether the first row names the columns, and a
   workbook's sheet; those a format has not are disabled. A change reads
   the file again; a file whose columns changed has them paired afresh.

3. **Each of the table's columns picks the file column that fills it**,
   starting from the pairs by name, or none; its type, and whether it
   needs a value, are said under it.

4. **The file's first 20 rows are shown as the table would take them.** A
   value that would not go in is shown as the file has it, and the footer
   counts the rows with one and says the first, with its column and why.

5. **The file is open for as long as its tab is**, and the tab is not kept
   in the session: an import starts from a file chosen now.

## Consequences

- Nothing is written yet: the dry run over the whole file (T2.20) and the
  writing (T2.21) come next, from this panel.
