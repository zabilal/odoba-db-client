# ADR-0031: Changes planned as SQL and applied in one transaction

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.5, T2.6 · **Requirements:** FR-4.4, FR-4.5, FR-4.9, NFR-S6 · **Packages:** `internal/source/sqlscript`, `internal/source/drivers/*`, `internal/source/conformance`

## Context

A table's pending changes become a `source.Changeset` (ADR-0027). The
driver contract's `source.Writer` plans a changeset without running it and
applies a plan, so that the preview FR-4.4 asks for is part of the
contract. No driver implemented it.

## Decisions

1. **One planner for every SQL engine** (`sqlscript.PlanWrites`). A row
   changed is an UPDATE of only the columns changed, matched by the key the
   row had. A row deleted is a DELETE matched by its key. A new row is an
   INSERT of only the columns given, so the server fills in the rest; with
   none given it is written as the engine says (`DEFAULT VALUES`, or
   MySQL's `() VALUES ()`). Columns go in name order, so a plan reads the
   same each time.

2. **Every value is bound, never written into the text** (NFR-S6), and
   every name comes from the dialect (ARCH-2). Decimals and JSON are bound
   as their text, which each engine reads as the column's type, as it does
   a parameter's (ADR-0026).

3. **A changeset that cannot be written whole is refused** rather than
   planned in part. This covers rows with no key, a key of the wrong length,
   a NULL key (which matches no row), an update of nothing, and a change of
   unknown kind.

4. **Each statement is described in a line** ("Update name where id = 3"),
   for the preview.

5. **A plan is applied in one transaction: all of it, or none**
   (`sqlscript.ApplyWith`). A statement the server refuses, or one that
   changes no row or more than one (ADR-0034), stops it, and it is
   rolled back. The outcome says which statement failed and why. Every statement is planned for exactly one
   row, so one that matches none means the row was changed or deleted since
   it was read. MySQL is connected with `ClientFoundRows`, so an UPDATE
   counts the rows it matched as the other engines do, not only those whose
   values changed. A commit that fails is said as such, not reported as a
   rollback.

6. **The guard is asked before anything runs**, with the consent each
   statement carries (FR-4.9). A read-only connection writes nothing. A
   production plan is marked Guarded and is refused until the changeset is
   confirmed.

7. **`WritePlan` carries its target**, so that PostgreSQL can apply it on
   that database's pool.

8. **The conformance suite checks it on every engine.** In the Writable
   table it inserts a row of defaults (the server numbers it), then
   inserts, updates and deletes rows, sets a value to what it already is,
   and reads the rows back each time. It checks that a plan with a row not
   there, or with a key used twice, leaves nothing behind, and it runs the
   read-only and production cases.

## Consequences

- PostgreSQL, MySQL, MariaDB and SQLite claim Insert, Update, Delete and
  TransactionalWrite, all backed by the conformance suite.
- The preview and the Commit button that use this are the grid's
  (ADR-0032). SQLite's tables are known by their key, or their rowid
  (ADR-0034).
- The count checked is the statement's own rows; a trigger's writes do
  not add to it. A table with an INSTEAD OF trigger that writes nothing
  would read as a row gone.
