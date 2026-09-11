# ADR-0046: Mapping a file's columns to a table's

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.19 · **Requirements:** FR-10.5 · **Packages:** `internal/value`, `internal/transfer`

## Context

An import reads a file as rows of values as the file has them (ADR-0044):
text for CSV and TSV, JSON's own types, a workbook's numbers and dates.
FR-10.5 asks that its columns be mapped to a table's, and its values made
the table columns' types. The grid already reads typed text as a column's
type, but that code was the UI's, and the core imports no UI (ARCH-1).

## Decisions

1. **A value's text is read and written by a core package of its own**,
   `internal/value`: the grid's editor and an import read text by the same
   rules, so a value that can be typed into a cell can be imported, and
   one that cannot is refused with the same words.

2. **Columns are paired by name** (`transfer.Suggest`): a file's column
   fills the table column whose name is the same but for case, spaces,
   underscores and hyphens ("Order ID" fills order_id). A table column is
   filled once, by the first file column to match it, and a file column
   no table column matches is left out. The pairs are a starting point, to
   be changed.

3. **A value is made its column's type as it would be typed**
   (`transfer.Coerce`): text is read by `value.Parse`; a value the file gave
   a type (a JSON number, a workbook's boolean) is written as text and read
   the same way, so a number goes into a column of whole numbers only where
   it is one. A date and time a workbook gives goes as it is into a column
   of dates or times. NULL stays NULL, and an empty field is NULL except in
   a column of text, where it is the empty string, as in a cell.

4. **Every value that cannot be made is said**, with its column, the value
   as the file has it and why, rather than the first stopping the rest: a
   dry run lists them (T2.20), and an error policy decides what a load does
   with them (T2.22). NULL in a column that cannot hold it is one of them.

## Consequences

- A table column with no file column paired to it is left out of the
  rows, for the server to give its default.
- Rows go to a source in the table's own types, ready for its bulk path
  (`source.BulkLoader`) or its inserts (T2.21).
