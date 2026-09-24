# ADR-0144: A row that knows where it is

**Status:** Accepted · **Date:** 2026-09-23
**Tasks:** T3.32 · **Requirements:** REQ-DB-1, FR-2.1, FR-2.3, FR-3.3, FR-3.4, FR-4.4, FR-4.7, FR-5.4, FR-5.10, NFR-S3, NFR-S4, NFR-S6
**Packages:** `internal/source/drivers/oracle`, `internal/sqllex`

## Context

Oracle is the last of the tier-1 engines REQUIREMENTS names. Two of the
three drivers before it in this phase had no way to address a row; this one
has the best of any engine here, and most of what follows turns on that.

The driver is sijms/go-ora in its thin mode: pure Go, speaking the wire
protocol directly, so there is no Instant Client to install beside the
application and no C toolchain to build it with.

## Decisions

1. **Every row has an address, so every table can be edited.** `ROWID` says
   where a row is, and every table has one. Where a table has a primary key
   that is what addresses its rows; where it has none, the address is, and
   the browse selects it as the first column so the grid has it to write by.
   This is `IdentityRowID`, which the SQLite driver already uses for the
   same reason (FR-4.7).

   A ROWID is the row's address until the row moves — a table reorganised
   or a row updated across a partition invalidates it — so a grid holds one
   only for as long as it holds the row it read, which is what a grid does
   anyway.

2. **Schemas are the top of the tree, not databases.** A connection is to a
   service, and what it holds is schemas — which are its users, Oracle
   having no separate idea of one. `Structure.Schemas` says so, and the
   explorer's first level is `ALL_USERS`. The catalogue is read through
   `ALL_*` rather than `DBA_*`: the second needs a privilege most logins do
   not have, and a tree that failed for want of one would be a tree nobody
   could open.

3. **The connection works in UTC.** Oracle compares a `DATE` or a
   `TIMESTAMP` with a value that carries a zone by reading the zone-less one
   in the session's zone, so a row read at one zone and filtered at another
   misses itself. Every connection is therefore put on UTC as it is opened,
   and a time is bound cast to `TIMESTAMP` so the comparison is between like
   and like. Without both, a date read from the grid does not find its own
   row again — which is how this was found.

4. **A NUMBER arrives as its own text, and stays exact.** go-ora hands every
   NUMBER over as text, whatever its size, which is what lets a column of
   thirty digits arrive without being rounded to a float. A whole number
   declared with a precision that fits an int64 becomes one; everything else
   stays an exact number, padded back to the places its column has, because
   a column of money shown as `1.5` where the row holds `1.50` is a
   different number to whoever reads it.

5. **The wire says less than the catalogue, so structure is read from the
   catalogue.** A `VARCHAR2` and an `NVARCHAR2` both arrive as `NCHAR`, and
   a `BOOLEAN` arrives as a `NUMBER`. A query's own result columns are read
   by what the wire says, because that is all there is; a table's structure
   is read from `ALL_TAB_COLUMNS`, which knows what was declared.

6. **A script is cut at a slash as well as at a semicolon.** A slash alone
   on a line is not SQL: the server has never heard of it, and what it
   separates is what the client sends — the same job `GO` does for SQL
   Server (ADR-0141). A PL/SQL block is sent whole and with the semicolon
   that ends it, because `END` without its semicolon is not a block.

7. **Each statement commits itself**, as on every other engine here. An
   explicit transaction is the `Transactor` interface's business (ADR-0137)
   and this driver does not implement it yet, so Oracle's own habit of
   holding a transaction open until `COMMIT` does not apply through the
   query tab. A changeset written from the grid *is* one transaction, which
   is what `Data.TransactionalWrite` claims.

8. **A large object is read, and is not offered as a value.** Oracle will
   not group or order by a `CLOB`, so it cannot be a picklist's column,
   though its text reads back perfectly well.

## Consequences

- The read path is whole, the grid edits every table, and the conformance
  suite is green.
- Not claimed, for want of something behind them: bulk loading, `EXPLAIN`
  plans, editable query results, explicit transactions, and cancelling a
  statement from another connection — Oracle's `ALTER SYSTEM KILL SESSION`
  needs a privilege a reporting login will not have.
- Where in a statement an error was is not reported. Oracle knows, and
  go-ora reads the offset off the wire and keeps it to itself, so what the
  editor gets is the code and the message (FR-5.10).
