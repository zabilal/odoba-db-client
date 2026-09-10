# ADR-0008: The PostgreSQL driver, and what the first real driver changed

**Status:** Accepted · **Date:** 2026-09-10
**Tasks:** T1.32–T1.34 · Reference implementation of the source contract

## Context

The contract in `internal/source` (ADR-0005) was designed in Phase 0 against a
Kafka-shaped fake. This is the first real driver, so it is also the first real
test of that design. The rule going in was that anything awkward is a finding
about the contract, to be fixed there, not worked around in the driver.

The work turned up five things.

## Contract changes

**`Sessioner` / `Session` (new).** SET, temporary tables, prepared statements
and open transactions all live on one server connection. An editor tab that
runs each statement on whichever pooled connection is free loses them between
runs: `CREATE TEMP TABLE t` succeeds and the next `SELECT FROM t` fails. A
`Session` pins one connection. Scripts always run on a single connection, and
a connection can hold one open result at a time, so every result except the
last is buffered, capped at 1 000 rows.

**`QueryMulti(ctx, script, confirmed)`.** Without the flag, a mutating script on
a production connection could never be confirmed, and so could never run. The
guard is also checked for every statement before any of them runs. Refusing the
fourth statement after the first three have executed would leave the user in a
state they did not choose.

**`StatementError` (new).** It carries the server's SQLSTATE code and character
position, which the editor needs to underline the failing token (FR-5.10). The
test sees `42703` at character 8.

**`ConnectError` (new).** "Test connection" needs to say what to fix, not print
a driver's error text (FR-1.4). The kinds are unreachable, TLS, auth, missing
database, refused, and bad config. All five reachable kinds are verified
against the live server.

**The lexer moved into core, as `internal/sqllex`.** Splitting scripts and
classifying statements need the same tokenisation as syntax highlighting:
don't split on a `;` inside a string, don't read `DELETE` in a comment as a
write. ARCH-1 forbids the source layer from importing UI packages, so the lexer
could not stay in the editor.

## Cancel keeps the session

pgx's default reaction to a cancelled context is to put a deadline on the
socket. The failed read that follows makes pgconn abandon the connection
(`asyncClose`): it sends a CancelRequest and a Terminate, then closes the
socket. The query does stop, within about 5 ms. The whole backend stops with
it, though, and so does everything in the session: temporary tables, SETs, an
open transaction. For a pinned editor `Session`, that means every press of
cancel silently throws the user's session away. For a pooled connection, it
means paying for a fresh dial, TLS handshake and authentication.

The driver installs `CancelRequestContextWatcherHandler` instead. It sends the
CancelRequest *without* abandoning the connection, so the statement fails with
`query_canceled` and the session carries on. Measured: the query returns
102 ms after cancel (NFR-P9 budget 200 ms), the backend stops running it, and a
temporary table created before the cancel is still there afterwards.

`TestPgxDefaultCancelDestroysTheConnection` pins pgx's default behaviour, so it
fails if pgx changes and the workaround can be revisited.

**This section was first written with the opposite conclusion**: that pgx's
default never reaches the server and leaves the query running. That was wrong.
The test behind it looked once, at the instant `Exec` returned, before
pgconn's asynchronous teardown had run. Sampled over time, the backend is still
active at +0 ms and gone by +5 ms. The mistake came to light only because the
race detector broke a second hidden assumption in the same test: that 300 ms
is long enough for the query to have started. A test of timing-dependent
behaviour has to sample over time and verify its preconditions. A single look
proves nothing.

## Read-only enforcement has three layers, because no single one is enough

1. **Lexical classification** refuses anything not confidently read-only before
   it reaches the server. It errs toward mutating: a false positive costs a
   prompt, a false negative is a write to a read-only connection. It catches
   the SELECT-shaped escapes:
   - `SELECT set_config('default_transaction_read_only', 'off', false)` turns
     read-only off for the session without issuing a SET.
   - `dblink_exec` runs SQL over a second connection.
   - `RESET ALL` and `DISCARD ALL` restore the server's read-write default.
   - Two statements in one string: pgx runs a parameterless statement over the
     simple protocol, which executes all of them, so the most dangerous
     statement decides.
2. **An explicit `READ ONLY` transaction around every statement.** A lexer
   cannot see inside a function body, so layer 1 alone is not enough.
3. **`default_transaction_read_only=on` at connect time**, as a backstop.

`TestReadOnlyHasThreeLayers` shows why layer 2 exists. A write hidden in a
function passes the lexer, and the server refuses it (SQLSTATE 25006). A
function that calls `set_config` really does flip the session default to `off`;
the test checks that before relying on it. The next hidden write is refused
anyway, because layer 2 does not consult the session default.

**The wrapper transaction commits rather than rolling back.** A READ ONLY
transaction cannot have written anything. SET, however, is transactional, and a
rollback would silently undo a legitimate `SET search_path`.
`TestReadOnlyKeepsLegitimateSessionState` covers that.

## Browsing

- **numeric, json and jsonb are read in text format.** Binary numeric has to be
  formatted back, and may lose a trailing zero the user can see in psql. pgx's
  default jsonb decoding unmarshals into Go maps, which reorders keys and turns
  `1.0` into `1`. The test sees `Decimal("1.10")` and `1.0` intact.
- **Paging is deterministic.** LIMIT/OFFSET over an incompletely ordered result
  can repeat one row and skip another. The source appends the primary key as a
  tiebreaker, or `ctid` for tables without one. Views have no key, so paging
  them stays best-effort. Sorted by a column with four distinct values, paging
  returns exactly 2 000 distinct rows.
- **Picklist semantics rather than raw SQL.** SQL's `NOT IN` drops NULL rows as a
  side effect, so unticking "paid" would also hide every NULL-status row. Here a
  `nil` in the list stands for NULL explicitly. `= NULL` means `IS NULL` rather
  than silently matching nothing. `contains` escapes `%` and `_` in the user's
  text.
- **Every value is bound and every identifier is quoted.** A hostile column
  name is quoted, not executed, and the builder's own output classifies as a
  read.

## Connecting

- The config is parsed from a fixed, fully specified string, never `""`. Given
  an empty string, pgx fills settings from `PGHOST`, `PGSSLMODE`, `PGPASSWORD`
  and `~/.pgpass`, so a stray variable in the user's shell would silently change
  which server a saved connection reaches.
- TLS verification defaults to `verify-full` (NFR-S3), and there is no silent
  fallback to plaintext. `crypto/tls` has no verify-ca mode, so the chain is
  verified by hand rather than left unverified.

## Result

The shared conformance battery passes. Its two skips are legitimate: this
driver has no Writer yet, and it supports server-side sort. Around 30
driver-specific integration tests and the unit suites pass. Capabilities stay
honestly false for what is not built: Writer, Explainer, Transactor,
Snapshotter and DistinctLister.

## Follow-ups

- **A session has no database target.** `Session` and `Query` run on the
  connection's primary database. The editor will need a database selector, and
  `Session(ctx)` should then take a database name.
- A notice raised after `Query` returns, part-way through a streamed result, is
  not captured. Notices almost always precede the rows, but not always.
- `Values()` also decodes the text-format columns before their raw bytes
  replace them. The result is correct but the work is wasted, and worth
  removing if row decoding shows up in a profile.
