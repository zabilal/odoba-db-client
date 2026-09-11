# ADR-0048: The dry run

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.20 · **Requirements:** FR-10.5 · **Packages:** `internal/transfer`, `internal/ui/shell`

## Context

The import panel shows a file's first 20 rows as the table would take them
(ADR-0047). A file of a million rows can have its one bad value at row
900,000, and a person wants to know that before anything is written, not
from a failed import.

## Decisions

1. **`transfer.Check` reads every row as `Coerce` makes it, and writes
   nothing.** It counts the rows, the rows with a value that would not go
   in, and those values, and keeps the first 1,000 of them, each with its
   row's place among the file's rows, from 1. A row the file cannot give
   ends it with an error and what it found before; so does its context
   ending. It reports how far it has got every 500 rows.

2. **Text longer than its column's length would not go in**, where the
   source knows the length; spaces past it do not count, as PostgreSQL and
   a strict MySQL drop them. Only PostgreSQL's descriptions carry a length
   today (`varchar(n)`, `char(n)`); MySQL's and SQLite's leave it unknown,
   and SQLite would not enforce one.

3. **`transfer.Unfilled` names the columns that need a value no file
   column gives**: not nullable, without a default, and not filled by the
   database (identity, auto-increment, generated). The panel's footer says
   them first, and a dry run with any says every row would be refused
   without reading the file. A generated column is not offered in the
   mapping at all.

4. **Dry Run runs as a task in the task centre** (FR-15.6): it says how
   many rows it has read and how many would not go in, can be cancelled
   there, and stops when the import's tab closes. One runs at a time.

5. **It answers in the panel's Dry Run tab**, beside First Rows: what it
   found, and a grid of the values that would not go in, by row, column,
   value and why. A change to the options or the mapping stops a dry run
   under way ("Stopped, as the import changed") and forgets the last one's
   findings, which were for the import as it was.

## Consequences

- The file is read once for the dry run and again for the import; reading
  is cheap beside writing.
- What only the database knows (unique keys, foreign keys, check
  constraints) is not found; it fails when written, under T2.22's error
  policy.
- Rows are counted among the file's rows, not its lines: a quoted field
  running over two lines makes the two differ.
