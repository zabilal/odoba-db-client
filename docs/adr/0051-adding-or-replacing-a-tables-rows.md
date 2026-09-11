# ADR-0051: Adding or replacing a table's rows

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.21 (replacing) · **Requirements:** FR-10.6 · **Packages:** `internal/transfer`, `internal/app`, `internal/ui/shell`

## Context

An import writes a file's rows through the Writer, a plan a batch
(ADR-0049), which cannot replace a table's rows safely: each plan commits on
its own. Every SQL driver now loads rows in bulk, and empties a table and
loads it in one transaction when told (ADR-0050).

## Decisions

1. **An import goes through the source's bulk loader.** `transfer.Load`
   hands it the file's rows as a stream, each made the table's values as
   it is asked for, so memory stays flat whatever the file's size. This
   replaces ADR-0049's plan a batch; its guarding, its stopping at a row,
   and its words stay.

2. **The panel offers two modes**: add the file's rows to the table's, or
   replace the table's rows with them. Replacing empties the table first,
   in one transaction with every row, so that if any row would not go in
   the table is left as it was, and the import says so: "The table is as
   it was."

3. **Replacing always asks first**, on every connection, and says the
   table is left as it was on a failure; on production it says that too.
   Adding asks on production alone, as before.

4. **A file with no rows is not imported**, so replacing cannot empty a
   table by mistake; the footer already says the file has none.

5. **Rows are counted as the loader reads them.** Progress says the rows
   read, and a row refused is named by its place among the file's rows,
   which is the loader's count too. An import that finished before a
   cancel reached it is said as done, not cancelled.

## Consequences

- Replacing a large table holds one transaction open for the whole file.
- Upserting and importing into a new table are the rest of T2.21.
