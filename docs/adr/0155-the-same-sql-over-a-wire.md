# ADR-0155: The same SQL over a wire

**Status:** Accepted · **Date:** 2026-09-25
**Tasks:** T4.3 · **Requirements:** REQ-DB-4, FR-1.1, FR-4.8, NFR-S1, NFR-S4
**Packages:** `internal/source/drivers/libsql`, `internal/source/drivers/sqlite`, `cmd/ikigai`

## Context

libSQL is a fork of SQLite that added a server. The SQL is SQLite's SQL, the
catalog is `sqlite_master`, the pragmas are the same pragmas, the type rules
are the same rules — what changed is that the database is reached over HTTP
or a WebSocket instead of by opening a file. Turso is that server, hosted.

So a libSQL driver could be written two ways: a second driver's worth of
introspection, browsing, scripting, statistics and DDL for an engine whose
answers are identical; or a way in, with everything below it shared.

## Decisions

1. **libSQL is the SQLite driver with a different way in.** The package is
   `Describe`, `Open`, and three helpers for addresses, tokens and failures.
   `Open` ends with `sqlite.NewSource`, and every question the application
   ever asks of the connection is answered by code written and proved for
   SQLite. A second copy would be a second thing to keep right about one
   engine, and the two would drift the first time either was touched.

2. **What differs is a `Flavour`, not a fork.** `sqlite.Flavour` carries what
   a connection is — the product name, the file or address, and whether the
   connection can say where a query's columns came from. It is three fields
   because three things differ. A fourth would be a reason to look again at
   whether this is one driver.

3. **`tursodatabase/libsql-client-go`, not the `go-libsql` the task named.**
   The named package embeds a local replica through cgo and a Rust archive
   built per platform. That is an offline-replica feature, and it is not what
   connecting to a database is. The client used here is pure Go, which keeps a
   remote database as free of a C toolchain as a local file already is — the
   same judgement DuckDB got, and the opposite outcome, because here there is
   a pure-Go choice that does the job (ADR-0146).

4. **The result of a query is not editable over libSQL, and says so.**
   Editing a query's rows needs to know which table each column was read from
   (FR-4.8, ADR-0035). SQLite answers that from the prepared statement;
   Hrana, libSQL's wire protocol, carries a column's name and its declared
   type and nothing about where the value came from, so the answer is not
   available at any price. `Capabilities().Query.EditableResults` is therefore
   the flavour's, and over a wire it is false. Browsing a table and editing it
   is untouched: a browse knows its own table without asking (ADR-0034).

5. **A column the connection did not type is asked of the object.** The Go
   client drops the declared type the protocol carries, so `ColumnTypes()`
   comes back saying nothing. Without a repair, browsing a table over libSQL
   would be a grid of "unknown" — nothing to group by, nothing to right-align,
   every number shown as the text it arrived as. A browse knows the object it
   is reading, so it asks it: any column still untyped takes the type the
   object declares for that name. A column nothing declares — a rowid
   selected to address the row by, an expression — stays unknown, which is the
   truthful answer. A local file answers for itself and is never asked, so the
   repair costs it no round trip.

6. **The token is a secret in the keychain and reaches nothing but the dial.**
   It is a `FieldPassword` marked `Secret`, so it is never in `settings.json`
   (NFR-S1); it is read through `cfg.Secret` at connect time and appended to
   the DSN handed straight to the client. No error this package returns
   carries it, and a test asserts that against a real failure (NFR-S2).

7. **Read-only over a wire is the guard's alone.** A file is opened `mode=ro`
   with `query_only`, so the engine refuses writes as well as the guard. A
   server connection has no such mode and each request may be its own session,
   so a pragma would not hold. The guard is where read-only is enforced either
   way (NFR-S4); over a wire it is the only defence, and the live suite proves
   it refuses and that nothing was written.

## Consequences

Another engine for the price of a way in. The reverse also holds, and is the
risk to watch: a change to the SQLite driver now changes libSQL, and the only
thing that will notice is the conformance suite run against a server.

A second finding came out of adding a driver: Kafka had been written, given a
tree, an admin menu, a record viewer and a produce dialog, and was never
imported into `cmd/ikigai` — so none of it could be reached from the
application at all. Nothing said so, because every one of its own tests
imports its package directly. The driver packages are now held against what
the binary depends on, so the next one fails the build's tests until it is
reachable.

That fix has a price, and it is the interesting number here: libSQL costs the
binary 1.0 MB, and Kafka 10.4. The binary went from 68.9 MB to 80.2, against
NFR-P8's 60, and the CI ceiling moved from 80 MB to 84 to let it through. The
budget was already the owner's open decision (T4.30); this makes Kafka the
second name on it after Oracle, and both are drivers a build tag could take
out. Moving a ratchet is a decision, so the number is written in the workflow,
in the defect register and in T4.30 rather than only in a diff.

## Alternatives

**A second driver.** Rejected: identical answers, written twice.

**`go-libsql` with cgo, as the task named.** Rejected for what it costs and
what it is: a C toolchain and a per-platform Rust archive, for a feature
(embedded replicas) nothing here asked for.

**Guessing a type from the first row's Go value.** Rejected: a column's type
must be known before any row is read, an empty result would have no type at
all, and a column of nulls would be typed by whatever arrived next.

**Claiming `EditableResults` and failing at write time.** Rejected: that puts
an editable grid in front of somebody whose edits cannot be applied.
