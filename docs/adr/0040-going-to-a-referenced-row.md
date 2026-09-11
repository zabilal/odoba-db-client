# ADR-0040: Going to the row a foreign key refers to

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.13 · **Requirements:** FR-3.11 · **Packages:** `internal/ui/shell`

## Context

FR-3.11 asks to jump from a value to the row it refers to. The drivers
describe a table's foreign keys (`model.Table.ForeignKeys`); a table's
tab did not read them, and a browse could be filtered only from the
filter row.

## Decisions

1. **A table's tab reads its description once, when it opens**, off the
   UI goroutine, for its foreign keys. A source that cannot describe it
   leaves the tab without them, and nothing is offered.

2. **Go to Referenced Row is offered on a cell in a foreign key's
   columns** whose row has a value in each of them, as the grid shows
   it: a pending change is followed, a NULL or a new row's column given
   nothing refers to nothing. It is on the View menu.

3. **It opens the table the key refers to, filtered to that row**, or
   brings its tab forward and filters it afresh. The table is in the key's
   schema where the key names one (PostgreSQL's schema, MySQL's database,
   SQLite's main), else in the first table's.

4. **The filter is written into the filter row**, in its own notation
   (`filterexpr.Pick`), so the row says what is applied; every other
   column's filter there is cleared, so that none left from before hides
   the row. A table that cannot be filtered opens unfiltered, and says so.

## Consequences

- A query's result offers no foreign keys, though its columns say which
  table they come from (ADR-0035).
- Rows that refer to this one (FR-3.11's second half, T2.14) need the keys
  of other tables, which a table's own description does not hold.
