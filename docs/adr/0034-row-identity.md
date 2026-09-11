# ADR-0034: Row identity

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.8 · **Requirements:** FR-4.7 · **Packages:** `internal/source/drivers/sqlite`, `internal/source/sqlscript`, `internal/model`, `internal/ui/shell`

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

3. **A table with no key says why its rows are not edited**, in its
   footer. **Choose a Key…** offers its columns to pick from. Its rows are
   then edited by the columns picked, in the table's order, as a key of
   their own kind (`IdentityChosen`); decision 2 refuses any change that
   would write more than one row. A read-only connection offers neither,
   since it edits nothing.

## Consequences

- SQLite tables can be edited, and a table with no key shows its rowid.
- A key chosen lasts while the tab is open. It is not remembered.
- A view is not offered a key; writing through a view is a question of
  its own (T2.9).
