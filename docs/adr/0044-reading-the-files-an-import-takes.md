# ADR-0044: Reading the files an import takes

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.17 · **Requirements:** FR-10.4, FR-10.3 · **Packages:** `internal/transfer`

## Context

FR-10.4 asks to import CSV, TSV, JSON, NDJSON and Excel files, and FR-10.3
that memory not grow with the rows. Export writes all but Excel
(`internal/export`). None of the module's dependencies reads a
spreadsheet, and ARCHITECTURE.md puts import and export pipelines in
`internal/transfer`, in the core that imports no UI (ARCH-1).

## Decisions

1. **`internal/transfer` reads a file as a stream of rows**: one entry,
   `Open(r io.ReaderAt, size, Options{Format, Header, Sheet})`, returning a
   `model.RowStream` that reads the file as its rows are asked for. The
   options are given here; finding them from the file is T2.18's.

2. **CSV and TSV are read as export writes them**: quoted as CSV is, a
   byte-order mark dropped. An empty field is NULL, as export writes NULL:
   a delimited file cannot tell an empty string from NULL. The first
   record's width is every row's; a shorter row is padded with NULL, and a
   wider one is an error naming its line. The header names the columns,
   or they are numbered; an empty or repeated name is numbered.

3. **JSON and NDJSON keep their types**: a number its digits, a nested
   object or array as JSON, null as NULL. The columns are the keys of the
   first 1,000 records, in the order first seen, each of the class its
   values share; a key first seen later is an error naming it and its
   record, not a value dropped.

4. **Excel is read with the standard library alone**, as a zip of XML
   parts: no dependency is added for it. Shared, inline and rich strings
   are text, their phonetic runs left out; a number keeps its digits,
   unless its cell's format is a date's (Excel's built-in 14 to 22 and
   45 to 47, or a code of its own with a date's letters outside its quotes,
   brackets and escapes), when it is the instant it counts, from 1900 or
   from 1904 where the workbook says so; a boolean is true or false; an
   error cell is its text. A sheet is picked by name, or is the first; its
   width is its dimension's, else its first row's, and a cell past it is an
   error naming its row.

5. **Values stay as they were read.** Text is text: making it the target
   column's type is the column mapping's (T2.19), which can say what it
   could not convert.

## Consequences

- A workbook's shared strings are held in memory; its rows stream.
- A date before 1 March 1900 is a day out, as Excel's own leap-year slip
  makes it for most readers.
- Nothing yet opens a file to import: the mapping, the preview and the
  writing are T2.19 to T2.22.
