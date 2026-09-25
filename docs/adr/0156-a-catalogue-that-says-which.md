# ADR-0156: A catalogue that says which

**Status:** Accepted · **Date:** 2026-09-25
**Tasks:** T4.2 · **Requirements:** REQ-DB-1, REQ-DRV-1, REQ-DRV-3, FR-3.4, FR-3.6, FR-4.7, FR-5.4, FR-10.6, NFR-S3, NFR-S4
**Packages:** `internal/source/drivers/firebird`, `internal/source/sqlscript`, `internal/sqllex`, `cmd/ikigai`

## Context

Firebird is smaller than the engines around it and different in ways that go
all the way down. A connection is to one database file; there is no
server-wide list of databases to browse. There are no schemas, so a database
holds tables directly. There is no `information_schema` — the catalogue is a
set of ordinary tables whose names begin with `RDB$`, which anybody may
select from. An unquoted identifier folds to **upper** case rather than lower.
And it does not speak TLS.

## Decisions

1. **The catalogue is read, and read defensively.** Every name is `TRIM`med,
   because Firebird pads some of them and not others. Every
   `RDB$SYSTEM_FLAG` is read through `COALESCE(…, 0)`, because on objects made
   by older versions it is NULL rather than 0 — without that, a database
   restored from an old backup shows none of its tables. The engine's own
   objects are excluded by that flag *and* by their name prefixes, so a
   database whose flag was lost still does not list `RDB$RELATIONS` as one of
   its tables.

2. **What a value is comes from the object, not from the wire.** Firebird
   stores several of its types the same way and tells them apart by a sub-type
   or a scale: an `INT64` with a scale is a `NUMERIC` and without one a
   `BIGINT`; a `BLOB` with sub-type 1 is text and with 0 is bytes. The library
   reports only the storage type, so a browse asks the table what it declared
   and uses that; the wire's answer stands only for a column nothing declared
   — an expression, a computed column of a view — and is read as the *wider*
   of the types sharing that storage, because guessing narrow would be wrong
   about the values as well as the name.

3. **`RDB$DB_KEY` is not a row identity.** Firebird has an eight-byte address
   of the row, and it is only stable within one transaction: a row updated
   gets a new one, and a browse and the edit that follows it are two
   transactions. A key that silently stops addressing the row it was read for
   is worse than no key, so a table with no primary key and no unique
   constraint over non-null columns reports `IdentityNone`, and the grid says
   the rows cannot be told apart — which is the truth. A unique constraint
   over a nullable column is refused too: Firebird allows any number of rows
   whose key is NULL, and two of them are the same row to an update.

4. **Encryption is on by default, and there is nothing to verify.** Firebird
   has wire encryption of its own — a cipher negotiated during authentication,
   keyed from the credentials, with no certificate anywhere in it. So the two
   halves of NFR-S3 come apart: encryption is available and is required by
   default (`wire_crypt=required`, which fails closed rather than falling back
   to plain text), and `verify-ca` and `verify-full` are **refused** rather
   than quietly treated as encryption alone. Somebody who chose verify-full
   asked for the server to prove who it is, and this protocol cannot. A second
   container, configured `WireCrypt=Disabled`, proves both halves: the default
   refuses it, and an explicit choice of Disable connects.

5. **`CONTAINING`, not `LIKE`, for a "contains" filter.** Firebird's `LIKE` is
   case sensitive, so `LIKE '%ada%'` misses Ada — and the grid's contains
   filter means what a person means by it. `CONTAINING` also takes its
   argument as text rather than as a pattern, so `50%` needs no escaping.

6. **A regular-expression filter is refused.** Firebird has `SIMILAR TO`,
   which is the SQL standard's pattern language and not the POSIX one the
   operator means: `\d` and `[[:digit:]]` are not in it and anchoring works
   the other way round. Accepting a POSIX pattern would match the wrong rows
   and present them as filtered (REQ-DRV-3).

7. **A script says where its statements end in either of two ways, and both
   are read.** `sqlscript` gained `SplitTerminated` for `SET TERM`, isql's
   directive, which names the new terminator and then closes itself with the
   one in force — the opposite way round from MySQL's `DELIMITER`, which is
   why it is its own function; and `FirebirdBlocks`, which finds the `BEGIN`
   of a procedure, trigger, function, package or `EXECUTE BLOCK` body for the
   scripts written without it. The directive is never sent.

8. **An upsert goes around the INSERT rather than after it.** Firebird writes
   `UPDATE OR INSERT INTO t (cols) VALUES (…) MATCHING (keys)`: the first two
   words change, so a suffix cannot express it. `sqlscript.Upserter` stays as
   it is for the engines whose upsert is a clause, and `UpsertRewriter` is new
   for a dialect that needs both ends. `MERGE` would say the same thing and
   would bind every value twice, once to match and once to insert.

9. **`DEFAULT VALUES` is asked for only where it exists.** The clause arrived
   in Firebird 4. On an older server a row of nothing but defaults is refused
   with a message naming the version, rather than sent as SQL the server
   cannot parse and reported as a syntax error nobody can act on.

10. **Three things are not claimed, and each says so.** No `Explain`: a plan
    is available to a client that asks the API for it while preparing a
    statement, and this library keeps it to itself. No `EditableResults`: a
    result carries a column's name and type and nothing about where it came
    from. No badge and no `ApproximateCount`: Firebird keeps no row estimate
    anybody can read, so a count is a scan, which is what the grid's own count
    already is. `Schema.Diff` is not claimed either — it means a whole
    database in one pass (`source.Snapshotter`), and comparison still works
    walked object by object, as SQLite's is.

11. **Read-only is the guard's, with the transaction behind it.** Firebird has
    a read-only transaction, which is the one place the engine itself can be
    told to refuse a write, so `Begin` asks for one on a guarded connection.
    The browse and write paths hold no transaction of their own, and there the
    guard is the only defence — which is where NFR-S4 puts it anyway.

## Consequences

Another relational engine, with its own catalogue and its own idea of case.
The wire encryption is the part to watch: it is not TLS, and a deployment that
needs a verified server identity needs a tunnel in front of Firebird. The
connection form says so where somebody would choose it, and the driver refuses
rather than implying it did something it did not.

`sqlscript` grew one interface, which every other driver ignores. The cost of
that is one more shape in a shared helper; the alternative was a loader of
this driver's own, which is the thing the helper exists to avoid.

## Alternatives

**`RDB$DB_KEY` as a fallback identity.** Rejected: see decision 3. It would
make more tables editable and some of those edits would hit the wrong row.

**Reading types from the wire alone.** Rejected: every exact decimal would
read as an integer whose values are strings, and every text blob as bytes.

**Mapping `verify-full` to `wire_crypt=required`.** Rejected: it would report
success for a verification that never happened.

**Ignoring `SET TERM`.** Rejected: it is how Firebird scripts are written, and
a script that defines a procedure would be split in the middle of its body.
