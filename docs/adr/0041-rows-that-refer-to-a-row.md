# ADR-0041: Rows that refer to a row

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.14 · **Requirements:** FR-3.11 · **Packages:** `internal/source`, `internal/model`, `internal/app`, `internal/source/drivers/postgres`, `internal/source/drivers/mysql`, `internal/source/drivers/sqlite`, `internal/ui/shell`

## Context

FR-3.11's second half asks for the rows that refer to a row. A foreign key
belongs to the table it is in, so a table's description (ADR-0040) holds
the keys it has, not the keys that refer to it.

## Decisions

1. **An optional contract, `source.Referrer`, lists the keys that refer to
   a table**, each with the table it is in (`model.Referrer`). It is
   optional, as `Snapshotter` and `Searcher` are: a source without keys
   between tables has nothing to list.

2. **Each driver reads its catalogue by the table referred to.**
   PostgreSQL reads `pg_constraint` by `confrelid`, once per key however
   many partitions inherit it; MySQL and MariaDB read `KEY_COLUMN_USAGE` by
   the `REFERENCED_*` columns; SQLite joins every table's
   `pragma_foreign_key_list` in one statement, matching the table's name
   without regard to case, as SQLite does.

3. **A SQLite key that names no columns refers to the primary key**, as
   SQLite takes it; its referred-to columns are filled in from that key,
   in Referrers and in a table's description alike. Before, they were
   empty, so Go to Referenced Row filtered on nothing.

4. **A table's tab lists its referrers when it opens**, with its
   description, off the UI goroutine. **Show Referring Rows opens the rows
   of a table whose key holds the active row's values**, filtered in the
   filter row as Go to Referenced Row does: at once where one table refers,
   else after asking which, each named by its table and its key's columns.
   It is on the View menu.

5. **The row's written values are followed**, not a pending change: other
   rows refer to what is written, so a new row has none referring to it.

## Consequences

- A table referred to by many is asked about with a list as long; there
  is no count of the rows each would show.
