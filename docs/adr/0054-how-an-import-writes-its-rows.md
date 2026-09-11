# ADR-0054: How an import writes its rows

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.22 · **Requirements:** FR-10.6 · **Packages:** `internal/transfer`, `internal/ui/shell`

## Context

Every SQL driver can leave a refused row out and go on (ADR-0053). An
import meets two kinds of row that will not go in: one with a value that
cannot be made its column's, found before the row reaches the table, and
one the server refuses. A person should choose, for both, whether the
import stops or goes on, and how many rows a transaction holds.

## Decisions

1. **The panel asks how the rows are written**, in a form above Dry Run
   and Import: the rows a transaction (1 to 100,000, 500 unless told), and
   what a row that would not go in does — stop the import, leave it out and
   go on, or leave it out up to a most (1 to 1,000,000), stopping at the
   row after. The most is asked only for the last. A number that is not
   one is said where it is typed and in the footer, and nothing runs.

2. **`transfer.Load` takes the same policies** and applies them to both
   kinds of row. A value that would not go in is left out before it reaches
   the loader; a row refused is left out by the loader, which is told only
   to skip. The most is counted in `transfer`, over both, so one most holds
   for the whole import.

3. **A row left out is named by its place in the file.** The loader counts
   the rows it is given, which are fewer than the file's once a row has been
   left out before them; `transfer` keeps, for each row it left out, how
   many rows it had handed on, and turns the loader's count back into the
   file's.

4. **The rows left out are listed in the panel's lower tab**, renamed from
   Dry Run to Problems, as it now lists both what a dry run found and what
   an import left out: by row, column and value where a value would not go
   in, and by why where the server refused the row. The first 1,000 are
   kept. The import's last word says how many were left out, except of a
   replace that failed, which left the table as it was.

5. **A dry run is unchanged**: it lists every value that would not go in,
   whatever the policy.

## Consequences

- Leaving rows out costs the loader a savepoint a row (ADR-0053).
- A server-refused row past the most is noticed at the next row read, and
  its batch is rolled back with it.
