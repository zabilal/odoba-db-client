# ADR-0010: Connection management

**Status:** Accepted · **Date:** 2026-09-10
**Tasks:** T1.23–T1.31 · **Packages:** `internal/app`, `internal/app/connstr`

## Context

A saved connection lives in two stores: its settings in the settings file,
and its secrets in the OS keychain (ADR-0009). Nothing spans both, so they
cannot be updated in one transaction. The order of operations is what keeps
them consistent.

## Keeping two stores consistent

| Operation | Order | Why |
|---|---|---|
| Create | Secrets first, then settings. If the settings write fails, the secrets are deleted again. | A connection saved without its password fails on first use, with an error that points nowhere useful. |
| Update | Read each secret being overwritten, write the new one, then write settings. If that fails, restore the old values. | Otherwise a failed save leaves a changed password behind unchanged settings. |
| Duplicate | Copy each readable secret under the new ID, then insert the copy after the original. Undo on failure. | A secret that cannot be read is not copied; the copy simply asks for it. |
| Delete | Settings first, then secrets. A secret that fails to delete is logged. | An orphaned keychain entry is harmless. A connection whose password vanished is not. |

The tests check each rollback against a real failure, not a mocked one: an
invalid environment that settings validation refuses after the secret has
already been written.

**Secret edits distinguish three cases.** A form's blank password field means
"keep the saved one", not "set it to empty", and a plain map cannot say that.
`SecretEdit{Set, Clear}` makes "absent" mean keep.

## No keychain: ask, don't fail

When no OS keychain exists, the vault holds secrets for the session only
(ADR-0009). After a restart the settings still list the secret *names*, but
their values are gone. `MissingSecrets` reports what is missing, and `Open`
returns `MissingSecretsError` *before* dialling. The UI can then ask for the
password, rather than letting the attempt fail with "authentication failed"
for a password that was simply never supplied. A secret typed in for the
session takes precedence over the keychain without overwriting it.

## Test connection

`Test` works on connections that are not saved yet. It uses secrets the user
has typed first, then falls back to the vault, so testing a saved connection
does not mean retyping its password. It gives up after 15 seconds. The result
carries the `ConnectKind`, a short hint at what to fix, and the redacted
technical detail.

Verified against the live server:

- A wrong password is classified as auth, and the password does not appear in
  the detail.
- The connection's default TLS mode, `verify-full` (NFR-S3), against a
  plaintext local container is classified as **TLS**. That is the first error a
  new user meets, and it names the right setting.

## Reconnecting means visibility

The drivers' pools redial on their own when next used, so `Live` does not
reconnect anything itself. What it adds is **visibility**. A lost connection
becomes an explicit `Disconnected` state carrying:

- a redacted reason;
- the attempt count;
- the time of the next retry;
- the time the outage *began*, which does not move forward with each failed
  attempt.

Retries back off from 1 s to 30 s with ±20% jitter, so a restarted server is
not hit by every client at once. Healthy checks notify no one. `Check()` asks
for an immediate re-check, which the UI uses when a query fails in a way that
suggests the connection dropped, so the state changes now rather than at the
next 30-second tick.

## Pasting a connection string

`connstr` understands:

- URLs;
- JDBC URLs;
- libpq `key=value` strings;
- `.pgpass` and `~/.my.cnf` files, as explicit imports.

**Errors never repeat the input.** `url.Parse` quotes the whole input in its
error, and its inner error echoes the offending fragment, which is usually part
of the password. So a malformed URL gets a fixed message that says what kind of
character to check. The tests look for every fragment of the password,
including the escape sequence, in every error message.

**`sslmode=prefer` and `allow` become `verify-full`, with a warning.** libpq
falls back to plaintext without saying so, and nothing in this application
downgrades silently (ADR-0008).

Every result is shown to be storable by saving it. None of them has a secret in
its parameters, and each one's secret names are valid.

Reading `.pgpass` and `~/.my.cnf` happens only when the user asks. That is
deliberately different from the driver, which refuses to read them implicitly
from the environment.

## Follow-ups

- **A pinned `Session` does not survive a reconnect.** The pool redials, but the
  editor's session was on the connection that died, and its temp tables and
  SETs died with it. When `Live` reports a recovery, the editor must be told its
  session was reset.
- The connection form itself (T1.24) is UI work. It builds its fields from
  `Descriptor.Fields`.
- SSH tunnels (T2.85) will slot in under `ConnectionConfig.SSH`.
