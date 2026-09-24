# Security review: NFR-S1 … NFR-S6

**Date:** 2026-09-24 · **Against:** the tree at T4.33 · **Reviewer:** development

DoD-6 asks for a review covering NFR-S1 to NFR-S6. This is it: each requirement,
what enforces it, what test holds it, what this review changed, and what is left
open. It is written to be re-run — every claim below names something a reader can
go and check.

Two things found during the review are already fixed and are noted under their
requirement: a data race on the vault lock, and a shared conformance check that
nine SQL drivers now take and three had.

---

## NFR-S1 — credentials in the OS keychain, never plaintext, never in the settings file, never in logs

**Enforced by** `internal/store/secrets`, which has no file fallback by design:
where no keychain exists, `Set` fails and the application asks for the password
each session, holding it in memory. The settings file has no password field and
must never have one; `SavedConnection.Secrets` lists the *names* of the secrets
held for a connection, never their values.

A portable copy is the one case where secrets are in a file, and then only
sealed: the key comes from the vault lock's passphrase, which is on no disk
(ADR-0154). Without a passphrase a portable copy persists nothing at all.

**Held by** `internal/store/secrets` tests (including the file keychain's), the
settings tests, `internal/app/vaultlock_test.go`, and the backup test that greps
the whole archive for the password and fails if it is there.

**Residual:** a secret typed for one session lives in this process's memory in
the clear, which is what "for this session" means. Nothing is done about a core
dump; Go gives no locked memory, and claiming otherwise would be the sort of
claim this file exists to avoid.

## NFR-S2 — connection strings redacted in every log line and error message

**Enforced by** `internal/redact`, applied at the points where a connection
string or a secret can reach a message: `internal/app/connections.go`,
`internal/app/live.go`, `internal/app/connstr`, `internal/source/connerr.go`
and `internal/source/stmterr.go`.

**Held by** `internal/redact/redact_test.go` — URLs, secret-shaped map keys, and
the requirement that redaction preserves error identity, so wrapping does not
break `errors.Is`.

**Residual:** a driver that puts a password into an error of its own making is
redacted only where the application wraps it. The wrapping points above are the
paths a driver error travels; a new path would need the same treatment, and
nothing automated would catch its absence.

## NFR-S3 — TLS verification on by default; disabling requires an explicit, per-connection, acknowledged choice

**Enforced by** `internal/source/tlsconf`. The default is a verified connection
with `MinVersion: TLS 1.2`. `InsecureSkipVerify` is set in exactly two modes,
both of which a person chooses per connection: `require` (encrypted, unverified)
and `verify-ca`, which skips the built-in check only to verify the chain by hand
so that the host name alone is unchecked — never skipped and left unverified.

**Held by** the `tlsconf` tests and each driver's own connection tests.

**Residual:** ClickHouse's driver builds one `tls.Config` of its own
(`internal/source/drivers/clickhouse/source.go`) rather than going through
`tlsconf`. It honours the same modes; it is a second place the rule is written,
and a third would be one too many.

## NFR-S4 — read-only mode enforced in the data layer

**Enforced by** the guard each driver holds, and checked the same way in all of
them by `internal/source/guardcheck`, which has two halves: it runs every
mutating operation on a read-only connection and on a production one and insists
on the right refusal, and it holds the table of operations to the interfaces
themselves — so a method added to a mutating interface and not listed fails the
test. A new way to change a database cannot arrive without a guard in front of
it.

Kafka's produce and offset reset are in that table, as the requirement asks. So
is browsing, which must never join a consumer group or commit an offset
(FR-13.19).

**Held by** `internal/source/guardcheck`, run by every driver's suite, and the
conformance suite's own `ReadOnlyGuard` check.

**Residual:** statement classification is per dialect and is a judgement about
text. Each driver's `classify` has its own table of tests, including the awkward
cases — a keyword inside a string, a comment, a quoted name — and the rule
everywhere is that anything not confidently read-only is reported as mutating.

## NFR-S5 — no telemetry, no phone-home, no analytics without explicit opt-in

**Enforced by** there being none. Nothing in this application contacts a host
belonging to this project. The network is used in three places, each of them
something a person asked for: a driver connecting to a server they configured,
an SSH tunnel they configured, and a cloud IAM token a connection asked for.
The update check (FR-15.10) happens only when somebody asks for it, and only in
a build that was given a release feed — a build made from source has none.

**Held by** `cmd/ikigai/offline_test.go`: the packages that keep, read and write
cannot reach `net/http`, `net/smtp` or `net/rpc` at all, with a control pointed
at `internal/cloud` so that a rule looking for the wrong thing fails; and no
file outside `internal/cloud`'s four token fetchers and `internal/update`
imports `net/http`, so a new one has to be added to that list on purpose and say
why.

## NFR-S6 — every value from the grid is bound; identifiers are quoted through a per-dialect quoter

**Enforced by** the `Dialect` interface: `QuoteIdentifier` for names,
`Placeholder` for values, and `BuildBrowse`/`Plan` rendering statements whose
values are arguments. `internal/source/sqlscript` builds the shared shapes
(SELECT, INSERT, UPDATE, WHERE) the same way for every dialect.

**Changed by this review.** Three drivers asserted for themselves that a value
shaped like a statement never reaches the SQL, and the other six did not. That
is exactly the gap a shared suite is for, so the check moved into it: the
conformance suite now renders a browse whose filter value is `'; DROP TABLE
orders --`, and again with one shaped like a quoted name, and fails if either
reaches the statement text or is not among the bound arguments. Nine SQL drivers
take it and all nine pass.

It is scoped to the relational paradigm on purpose. A document or stream source
builds its query as a structure — MongoDB hands its driver a `bson.D` — and what
its dialect renders is a preview for somebody to read. A value appearing in a
preview is not a value reaching a server, and holding a preview to this rule
would be holding it to something it is not.

**Residual:** the check is at the rendering boundary, which is where a
concatenation would show. A driver that rendered a correct statement and then
built a different one to execute would pass it; nothing but reading the driver
catches that, and each driver's browse tests read the statement it executes.

---

## Also found, and fixed

- **A data race on the vault lock** (T4.17, found by the commit gate). Putting a
  lock on happens off the goroutine the window runs on, because deriving a key
  takes a tenth of a second on purpose, and the window reads the lock while it
  does. Both the lock a vault holds and the key a lock holds are behind a mutex
  now. A test that waited for the wrong moment is what surfaced it.
- **Rows decoded in place** (T4.16). The stream that decodes a topic's records
  for an export was writing into its caller's rows, so a record could be read
  once in the window or once into a file but not both. It copies now. Not a
  security defect; found in the same pass and worth naming.

## Open, and not this review's to close

- **Signing and notarisation** (NFR-S8, T4.24–T4.26) need certificates and
  accounts. Until they exist, releases are unsigned, and the update check
  deliberately does not install anything (ADR on T4.27).
- **Three dependency advisories with no fix**, accepted by name with their
  exposure written down in `security/accepted-vulnerabilities.md` (NFR-S9). All
  three are denial of service by unbounded allocation while decoding Avro; none
  writes anything and none involves a credential.
- **Screen-reader support** (NFR-A3) is a known gap, documented in
  `docs/ACCESSIBILITY.md`. It is not a security matter, and it is the other
  thing a reader of this file may want to know is written down.
