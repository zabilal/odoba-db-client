# ADR-0036: Editing a query's result

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.9 · **Requirements:** FR-4.8, FR-4.7, FR-4.3 to FR-4.6 · **Packages:** `internal/ui/shell`, `internal/app`, `internal/model`, `internal/source/drivers/postgres`, `internal/source/drivers/sqlite`, `internal/source/conformance`

## Context

A query's result can say it is known by a table's key (ADR-0035), and a
table's rows are edited in its tab (ADR-0029 to ADR-0034). A query tab
shows each result of a run in a grid of its own, and runs its statements
on a session of its own, which may hold a transaction, a search path or a
temporary table.

## Decisions

1. **A result is edited when its source writes rows, it is known by a key,
   and the statement that made it only reads**, by the dialect's
   classification. A result of `UPDATE … RETURNING` is not: reading it
   again would write again. Nor is any result on a read-only connection.

2. **Each result's editing is its own.** Its changes, their count and its
   Review Changes… sit under its grid. The Edit menu acts on the result in
   front, found through its grid, by the same commands as a table's rows.

3. **The changes are written on a connection of their own, not the
   session's.** A transaction open on the session is neither joined by
   them nor committed by their COMMIT.

4. **Once they are written, the statement runs again, alone, on the
   session**, for the rows as they now are: with the session's state and
   the values the statement ran with, so it reads the same table it read
   the first time. It is not recorded in history, which holds what the
   user ran. If it fails, the rows read before stay, and it is said.

5. **A run replaces the results, so it asks first** when they hold changes
   not committed, as closing the tab and quitting do. Nothing runs while
   changes are being written.

6. **A result read from a table, but not known by its key, says it is
   read-only and why** (FR-4.7). A result read from no table says nothing,
   nor does one on a read-only connection. Choose a Key… stays a table's.

7. **A result keeps its key wherever the driver holds it**: read on a
   connection of its own, and held in memory while a later statement of
   its script runs. The conformance suite holds PostgreSQL and SQLite to
   both.

## Consequences

- A lock held by a transaction open on the session makes a commit wait
  until that transaction ends; closing the tab stops the wait.
- Reading a result again, like any statement on the session, ends a result
  of the same run whose rows are still arriving.
- MySQL and MariaDB results stay read-only (ADR-0035).
