# ADR-0050: Loading rows in bulk

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.21 (replacing) · **Requirements:** FR-10.6 · **Packages:** `internal/source`, `internal/source/sqlscript`, `internal/source/drivers/*`

## Context

Replacing a table's rows with a file's (T2.21) must empty the table and
write the rows so that a failure part-way leaves the table as it was. The
Writer's plans each run in a transaction of their own (ADR-0031), so an
import written a plan a batch (ADR-0049) cannot do that, and one plan of a
whole file would hold every row in memory. `source.BulkLoader` was declared
for imports from the start: `LoadRows` pulls rows from a stream, so memory
stays flat, and `LoadOptions.Truncate` empties the target first.

## Decisions

1. **Every SQL driver implements `BulkLoader` through one helper**,
   `sqlscript.LoadWith` (`LoadSQL` on `database/sql`): each row one bound
   INSERT, written as a changeset's new row is (ADR-0031), committed every
   `BatchSize` rows, 500 unless told.

2. **Emptying the table is `DELETE FROM`, not `TRUNCATE`** — MySQL's
   `TRUNCATE` commits, and PostgreSQL's takes a stronger lock — **and such a
   load runs in one transaction whole**, whatever the batch size: one that
   fails leaves the table as it was.

3. **The guard is asked first**, as for any write: a read-only connection
   loads nothing, and production needs consent. Emptying the table needs
   consent on every connection, as `LoadOptions` says it is always guarded.

4. **A row the server refuses, or one that adds other than one row, stops
   the load** with a `source.LoadError` naming its place among the rows
   given. Its transaction is rolled back and those before it stay
   committed; the count returned is the rows committed. Only the "abort"
   error policy is taken: "skip" and "collect" are T2.22's, and are refused
   until then rather than quietly aborting.

5. **`capability.Data.BulkLoad` says a source implements it**, and the
   conformance suite holds the two together. Its Loader check, on every
   engine, loads a batch at a time, stops at a row refused with the batch
   before it kept, empties the table first in one transaction, leaves the
   table as it was when such a load fails, and runs the read-only and
   production cases.

## Consequences

- The import (ADR-0049) can move onto `BulkLoader`: memory stays flat, and
  adding rows and replacing them take one path.
- PostgreSQL's `COPY` can later go inside its `LoadRows`, unseen above it.
- A batch committed is written for good: only a load that empties the table
  is all or nothing.
