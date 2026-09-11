# ADR-0052: Updating rows by key

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T2.21 (upserting) · **Requirements:** FR-10.6 · **Packages:** `internal/source`, `internal/source/sqlscript`, `internal/source/drivers/*`, `internal/transfer`, `internal/ui/shell`

## Context

An import adds a file's rows to a table's, or replaces them (ADR-0051). A
file often carries rows the table already has, changed: a row whose key is
there should update that row, and the rest be added.

## Decisions

1. **`LoadOptions.Keys` names the columns of a key**: a row whose key is
   taken already updates the row there, and is not refused. It cannot go
   with `Truncate`, and every key column must be among those loaded.

2. **Each dialect says how, as `sqlscript.Upserter`.** PostgreSQL and SQLite
   append `ON CONFLICT (key) DO UPDATE SET c = EXCLUDED.c` (`OnConflict`);
   MySQL and MariaDB append `ON DUPLICATE KEY UPDATE c = VALUES(c)`, which
   both read, though MySQL 8.0.20 deprecates `VALUES()`. A row of its key
   alone is left as it is. A dialect that cannot say refuses such a load.

3. **An upsert's row count is not checked**: engines count an update their
   own ways (MySQL says 2, and 0 or 1 for an update that changes nothing).
   A load still counts the rows it was given.

4. **The panel offers a third mode**, "Update rows with the same key, add
   the rest", on a table with a primary key, and updates by it. The mapping
   must fill every key column; otherwise the footer says which, and nothing
   runs. It asks on production, as adding does, and says at the end that the
   rows whose key was there already were updated.

5. **The conformance suite checks it on every engine**: an upsert on the
   Writable table updates row 10 and adds row 13.

## Consequences

- Only the primary key is offered; a unique key of another kind is not yet.
- On MySQL and MariaDB a row can update another row by any unique key of
  the table, not only the one named: the engine does not let the statement
  choose.
- Rows updated and rows added are not counted apart.
