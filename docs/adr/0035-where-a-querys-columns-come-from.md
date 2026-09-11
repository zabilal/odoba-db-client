# ADR-0035: Where a query's columns come from

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.9 · **Requirements:** FR-4.8 · **Packages:** `internal/model`, `internal/app`, `internal/source/capability`, `internal/source/drivers/postgres`, `internal/source/drivers/sqlite`, `internal/source/conformance`

## Context

FR-4.8 asks that a query's result be editable when it maps to a single
updatable table. A browse knows its table and the key its rows are told
apart by (ADR-0034). A query's result knew nothing of where its columns
came from: `ColumnDef.Origin` was set only by browses.

## Decisions

1. **A column says where it was read from.** It gives its table (`Origin`)
   and its name there (`OriginColumn`), which a query can rename ("SELECT
   name AS label"). A change is written by the table's names
   (`ColumnDef.SourceName`, used by `app.Pending`).

2. **A result is known by a table's key only when it reads that one
   table.** Every column must come from the table, and the table's key
   must be among them. Otherwise the result is known by nothing and stays
   read-only: a computed column, a join, or a result without the key.

3. **PostgreSQL reads each column's table and column number from the row
   description**, and looks the table up on another pooled connection,
   since the result is still open on its own. Nothing is cached, because a
   column renamed since would otherwise be written as the wrong one. Only
   tables and partitioned tables count.

4. **SQLite asks the statement where each column is read from**, prepared
   but not run (modernc's `ColumnInfo`). A table with no key is known by
   its rowid only when the rowid is among the columns.

5. **MySQL and MariaDB do not.** Their driver keeps a column's table to
   itself, so their query results stay read-only. `capability.Query.
   EditableResults` says which engines report where a result's columns
   come from, and the conformance suite holds those engines to it.

## Consequences

- Editing a result in its grid, and committing it to its table, is the
  grid's part of T2.9, still to come.
- On PostgreSQL, a query that reads tables makes one catalog lookup per
  table it reads, each time it runs.
- SQLite resolves a view's columns to its base table, so a query of a
  simple view can be known by that table's key and written through it.
