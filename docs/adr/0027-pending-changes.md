# ADR-0027: Pending changes

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.1 · **Requirements:** FR-4.3, FR-4.7 · **Packages:** `internal/app`

## Context

FR-4.3 asks for a pending changeset: edits gather locally, marked as added,
modified or deleted, and nothing reaches the server until committed. Phase
0 wrote the driver contract's side (`source.Writer`, whose `Plan` renders a
changeset and `Apply` runs it; `Changeset`; `RowChange`), and every driver's
browse reports how its rows are told apart (`model.RowIdentity`). Nothing
held the edits between the grid and the writer.

## Decisions

1. **`app.Pending` holds one table's changes**, and nothing reaches the
   server until they are committed through the source's writer (T2.5,
   T2.6).

2. **A row read is known by its identity's values, not its place.** An
   edit stays with its row when a sort or a filter moves it, and a row read
   again finds its edits. A key is its values with their types, since an
   SQLite column can hold both 1 and '1'.

3. **Only real changes count.** A cell set back to what the row holds is no
   change, and a row with none left is unchanged. Values are compared as
   values: bytes by their bytes, times as instants.

4. **An update carries the columns changed and the key the row had.** A
   changed key is written by the key it had, and an edit to one cell does
   not write over a change someone else made to another, as the contract
   asks.

5. **A deleted row loses its edits, and takes no more until reverted.**
   Changes are reverted by the cell, the row, or all at once.

6. **New rows are kept apart**, in the order added. The changeset lists the
   deletions and updates first, in the order made, then the new rows.

7. **Rows that cannot be told apart are refused (FR-4.7).** `NewPending`
   refuses an identity that edits nothing: none, a log's offset, or no
   columns. It also refuses one whose columns are not among the rows',
   since the key could not be read from them.

## Consequences

- An ordinary SQLite table is told apart by its rowid, which its browse
  does not return. So SQLite's tables are refused until the browse returns
  it, or a key column stands in for it (T2.8).
- The grid shows pending changes (T2.2, ADR-0028) but does not edit yet
  (T2.3), and no driver implements the writer yet (T2.5, T2.6).
