# ADR-0053: Leaving refused rows out

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.22 · **Requirements:** FR-10.6 · **Packages:** `internal/source`, `internal/source/sqlscript`

## Context

A load stops at the first row the server refuses (ADR-0050). FR-10.6 asks
for an error policy: a person importing a large file may rather have the
rows that go in, and be told of those that did not.

## Decisions

1. **`LoadOptions.OnError` takes three policies.** "abort", or none, stops
   at a row refused, as before. "skip" leaves it out and goes on. "collect"
   leaves out up to `MaxErrors` rows and stops at the next refused, as
   "abort" does. "collect" without `MaxErrors`, and any other policy, is
   refused.

2. **With "skip" or "collect", each row is written in a savepoint**:
   `SAVEPOINT`, the row, then `RELEASE`, or `ROLLBACK TO` and `RELEASE`
   when it is refused. PostgreSQL aborts a transaction at a failed
   statement, so only a savepoint lets it go on; MySQL's InnoDB and SQLite
   take savepoints too, so one way serves every engine. Releasing it each
   time keeps savepoints from piling up. A savepoint statement that fails
   stops the load, rolled back: MySQL rolls a whole transaction back on a
   deadlock, and its savepoint is then gone. Without these policies no
   savepoint is written, as it costs two round trips a row.

3. **`LoadOptions.Skipped` is told of each row left out**, as a
   `source.LoadError`, when it is: memory stays flat, and the person
   importing can see them come. A row left out does not count toward its
   batch.

4. **The conformance suite checks it on every engine**: a row refused left
   out with the rows after it written, and collecting stopped at the row
   after the most left out, its batch rolled back.

## Consequences

- Loads that skip are slower, by the savepoints.
- The import panel offers the policies and the batch size next, and lists
  the rows left out (the rest of T2.22).
