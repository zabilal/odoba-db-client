# ADR-0082: The CQL dialect

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.49 · **Requirements:** FR-3.6, FR-5.4, FR-5.10, FR-12.3, NFR-S4, NFR-S6, REQ-DRV-3 · **Packages:** `internal/source/drivers/cassandra`, `internal/source/sqlscript`

## Context

`source.Dialect` is what the rest of the application asks a source about its
language: how a name is written, how an object is addressed, where one
statement ends and the next begins, what a statement does, and what a browse
would send. The editor colours by it, the guard refuses by it, and the grid
shows the statement behind a filter by it.

CQL reads like SQL and is not SQL. It has no OFFSET, no NULLS FIRST, no LIKE
outside an index built for it, no way to ask whether a column is null, and no
inequality in a WHERE clause. It also has a body SQL has not got: a batch,
whose own semicolons do not end it.

## Decisions

1. **A batch is one statement, and the shared splitter learned to say so.**
   `sqlscript.Split` could open a body but only ever closed one at `END`,
   which is right for every SQL dialect here and wrong for
   `BEGIN BATCH … APPLY BATCH`. It gained `SplitWith`, which takes a closer
   of its own; `Split` delegates with the `END` closer and is unchanged for
   its three callers. `CASE … END` is counted only under that closer, since a
   dialect that ends bodies otherwise has no CASE to count.

2. **Names are double-quoted, and quoting is also what keeps their case.**
   CQL folds an unquoted name to lower case, so the quoting that makes a name
   safe (NFR-S6) is the same act that makes `People` stay `People`.

3. **What a statement does is read from its first word**, as it is for every
   other dialect here, and the safe direction is the same: a verb nobody
   listed writes. `SELECT`, `USE`, `DESCRIBE` and `LIST` read; a batch is the
   writes inside it; `TRUNCATE` is structural, as it is in every other driver;
   `CREATE`, `ALTER` and `DROP` are structural unless they are about a role or
   a user, which is the cluster's business rather than a keyspace's, and so
   administrative; `GRANT` and `REVOKE` administer.

4. **A browse refuses what CQL cannot mean.** An offset, because a page is
   where the last one ended rather than a count of rows to skip (T2.51).
   Where the nulls go, because the only thing a row can be ordered by is a
   clustering column, which can never be null. A pattern, a regular
   expression, a range, an inequality, a negation, text inside a value, and a
   comparison with nothing — because CQL has none of them, and silently
   dropping an option the grid is showing would be worse than refusing it
   (REQ-DRV-3).

5. **Nothing adds ALLOW FILTERING.** A query that needs it reads every
   partition on every node. Whether that is worth doing is a person's
   decision, taken knowingly, and not a driver's to make quietly on their
   behalf. A query that would need it is refused by the cluster, in the
   cluster's own words.

6. **The source carries its dialect**, embedded as SQLite's does, so the grid,
   the guard and the conformance suite find it by asking the connection. The
   driver still claims no query language in its capabilities: that claim
   requires a `Queryer` to run a statement, which is the next task's.

## Consequences

The editor can colour CQL, the guard can refuse a write typed into a
read-only connection before it is sent, and the grid can show what a browse
would send — all before the driver can run a statement at all.

`SplitWith` is the first place the shared splitter has admitted a language
whose bodies do not end at `END`. A future dialect with the same shape — a
`DO` block, a stored procedure body of another kind — now has somewhere to
say so rather than a splitter of its own.
