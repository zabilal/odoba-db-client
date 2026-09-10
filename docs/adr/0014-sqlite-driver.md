# ADR-0014: The SQLite driver

**Status:** Accepted · **Date:** 2026-09-10
**Tasks:** T1.38 · **Packages:** `internal/source/drivers/sqlite`, `internal/source/sqlscript`, `internal/e2e`

## Context

SQLite is the second engine, and the first on `database/sql`. A database is
a file on the user's disk rather than a server, which changes what "connect",
"read-only" and "cancel" mean.

## Decisions

**modernc.org/sqlite.** It is pure Go, so the application stays one binary
with no C toolchain beyond what Fyne needs. It is already the local store's
engine (ADR-0009).

**Opening never creates a file.** A mistyped path must fail. Silently
creating an empty database would look like the data had vanished. Four
failures get distinct, plain explanations: a missing file, a folder, a file
that is not a database, and no path at all. SQLite reads a file's header
lazily, so `Open` reads the smallest thing it can to find out.

**Read-only is refused twice.** The guard refuses writes by classification,
as on every engine. A read-only connection also opens the file with
`mode=ro` and `query_only`, so the engine itself refuses whatever a
classifier might miss. A test writes around the guard and is refused by the
engine.

**The classifier is SQLite's own.** `ATTACH` can open another file for
writing, `VACUUM` rewrites the database, and a PRAGMA that assigns a value or
is not on the list of known read-only ones counts as admin.
`load_extension` runs native code. A CTE can come before a write. Anything
unrecognised counts as a write.

**Script splitting is shared.** `internal/source/sqlscript` splits for every
dialect, with a hook for bodies whose semicolons do not end the statement.
SQLite's are trigger bodies; PostgreSQL's are `BEGIN ATOMIC` bodies. The
PostgreSQL driver moved onto it and its tests passed unchanged.

**Types follow SQLite's affinity rules.** A column's type class comes from
SQLite's own rules (sqlite.org/datatype3.html §3.1), refined by the names
people use: DATE, DATETIME, BOOLEAN, JSON. A column declared with no type
holds any kind of value, and says so.

**Paging is deterministic.** Tables are ordered last by `rowid`, or by the
primary key for a `WITHOUT ROWID` table. Views have no rowid and are paged in
the order SQLite returns them unless the user sorts them.

**Affected rows come from `changes()`.** `database/sql` hides the count for
a statement run as a query. `changes()` on the same pinned connection has
it, and running everything as a query keeps `INSERT … RETURNING` returning
its rows.

**Cancellation is genuine.** modernc interrupts a running statement when its
context is cancelled. `KillQuery` cancels a session's running statement, so
the driver can honestly claim `Query.Cancel`.

**Counts are exact.** A local file counts quickly, so the grid gets a real
scrollbar. This is a judgment about typical files; a very large one would
make it worth revisiting.

**Regex filters are refused.** SQLite has no `REGEXP` built in, and ignoring
the filter would show unfiltered rows as if they were filtered (REQ-DRV-3).

**The journeys need no server.** J1 and J3 run against a temporary database
file in the ordinary test suite, so CI runs them on every change.

## Also changed

The connection form shows Encryption only for drivers with a host, since a
database that is a file has nothing to encrypt in transit.

## Not decided here

Attached databases, a Choose File… button, pasting `sqlite:` URLs, row
estimates from `sqlite_stat1`, generated-column expressions, and error
positions, which SQLite does not report in a form the driver can read.
