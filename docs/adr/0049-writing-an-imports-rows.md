# ADR-0049: Writing an import's rows

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.21 (inserting) · **Requirements:** FR-10.6, FR-10.7 · **Packages:** `internal/transfer`, `internal/source/sqlscript`, `internal/ui/shell`

## Context

The import panel reads a file, maps its columns and dry-runs it (ADR-0047,
ADR-0048), and writes nothing. No driver implements `source.BulkLoader`.
Every SQL driver writes changes through `source.Writer` (ADR-0031):
planned, guarded, and applied in one transaction.

## Decisions

1. **Rows go through the Writer every edit goes through, in batches.**
   `transfer.Load` makes each row the table's values as `Coerce` does and
   groups the rows into changesets of new rows, 500 a batch, each planned
   and applied in one transaction. An import is guarded as an edit is
   (read-only refused, production asked), binds every value, and works on
   every SQL engine now. `BulkLoader` (PostgreSQL's `COPY`) stays the fast
   path for later, behind the same panel.

2. **New rows need no key.** `sqlscript.PlanWrites` plans a changeset of
   inserts alone without one, since no key addresses a new row; updates
   and deletes still need one (amending ADR-0031 §3). The conformance
   suite writes a row without a key on every engine.

3. **A load stops at the first row with a value that would not go in, and
   at the first the server refuses**, and says which, by its place among
   the file's rows, as the dry run numbers them. The batches before it stay
   written; of its own batch nothing, as the batch is rolled back, unless
   the server could not roll it back, which is said. Skipping or collecting
   bad rows, and the batch size, become choices in T2.22.

4. **NULL goes in as NULL**: a field the file leaves empty, in a column
   that can hold NULL, is written as NULL, not left to the column's default.

5. **Import runs as a task in the task centre**, from the panel's Import
   button, and not beside a dry run. It says the rows written and how
   fast; where a dry run counted the rows, how far it has got and how long
   is left (FR-10.7). Cancel stops it between rows: the batch under way is
   rolled back and those before it stay, said as "The first 1,000 rows
   were written, and none after." A failure shows in the error bar too.

6. **On a production connection Import asks first**, as Commit does. Whether
   it must is the driver's guard's answer about a plan of no rows, so the
   shell keeps no second copy of the rule; every batch then carries the
   consent.

7. **Once any rows are written, the table's tab reads its rows again.**

## Consequences

- An import runs at the speed of single-row INSERTs batched in
  transactions; `BulkLoader` is the upgrade where that is too slow.
- An import stopped part-way leaves its earlier batches written, and says
  how many rows; the dry run is there to find the problem first.
- Replacing a table's rows, upserting, and importing into a new table are
  the rest of T2.21.
