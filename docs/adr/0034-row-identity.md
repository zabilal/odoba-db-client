# ADR-0034: Row identity

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.8 · **Requirements:** FR-4.7 · **Packages:** `internal/source/drivers/sqlite`, `internal/source/sqlscript`

## Context

FR-4.7 asks that rows with no reliable identity not be edited, that the
refusal be explained, and that a key can be nominated. A row is edited by
its key (ADR-0027, ADR-0031). SQLite reported every table as known by its
rowid, which its browse did not return, so `app.Pending` refused every
SQLite table. That included tables with a primary key, and a table whose
INTEGER PRIMARY KEY *is* its rowid.

## Decisions

1. **SQLite knows a table by its primary key when it has one**, or else by
   its rowid. An INTEGER PRIMARY KEY is the rowid by another name, and a
   WITHOUT ROWID table always has a primary key. For a table with neither,
   the browse selects the rowid as the first column, typed as an integer,
   so that its rows can be written by it. When the columns are chosen, they
   are what is shown; the rowid is not added.

2. **A statement changes exactly one row.** One that changes more than one
   is refused and rolled back, just as one that changes none is
   (`sqlscript.ErrManyRows`). A primary key never lets this happen. A key a
   person nominates might not be unique, and without this check it would
   write every row that shares it. The count checked is the statement's own
   rows; on these engines a trigger's writes are not added to it.

## Consequences

- SQLite tables can be edited, and a table with no key shows its rowid.
- Saying why a table cannot be edited, and nominating a key, are the
  grid's part, still to come under T2.8.
