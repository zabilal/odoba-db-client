# ADR-0009: The local store — settings, secrets, history

**Status:** Accepted · **Date:** 2026-09-10
**Tasks:** T1.17–T1.22 · **Packages:** `store`, `store/secrets`, `store/localdb`, `logging`

## One rule shapes everything here

**Never lose the user's data, and never write their secrets to disk.**

Each decision below applies one half or the other.

## Paths (FR-17.1, FR-17.6)

Each platform follows its own conventions. On Windows the settings file
*roams* with a domain profile, which a user moving between machines expects.
The history database and logs stay *machine-local*, because they grow large and
roaming them would slow every login. XDG variables are honoured only when they
hold an absolute path. The spec says a relative value is invalid and must be
ignored, rather than resolved against whatever the working directory happens to
be.

Portable mode activates when a marker file, `ikigai-portable`, sits beside the
executable. Everything then lives in a directory next to it.

## Settings (FR-17.2)

- **JSON, with snake_case keys.** The standard library reads it, it round-trips
  predictably, and it diffs cleanly under version control. People edit this
  file by hand, so the keys are written for them.
- **Atomic writes.** Data goes to a temp file, which is fsynced and renamed, and
  then the *directory* is fsynced. Without that last step a POSIX system can
  come back from a power cut with the name pointing at nothing. The file is
  mode 0600.
- **Update changes a copy.** The copy is validated, written, and only then made
  current. A change that fails at any step leaves memory and disk exactly as
  they were.

Three failures, each handled without losing anything:

| Condition | Behaviour |
|---|---|
| Not valid JSON | Moved aside byte-for-byte to `settings.json.corrupt-<time>`. Defaults are used. Overwriting it would destroy the only copy of the user's connections. |
| Written by a newer version | Loaded but **frozen**. Saving would drop fields this build does not know. |
| Hand-edited to fail validation | Also frozen, with the reason. The alternative is "fixing" the file silently. |

## Secrets (FR-1.5, NFR-S1)

**The guarantee is structural.** A saved connection has no password field.
Validation refuses to save any parameter whose name looks like a secret, so no
code path can put one in the settings file by mistake. A connection records
only the *names* of the secrets held for it, so the UI can show "password saved"
without reading the keychain.

The keychain backend is `zalando/go-keyring`: macOS Keychain, Windows
Credential Manager, and the Secret Service on Linux. One concern was checked in
its source, not assumed. On macOS the library drives `/usr/bin/security`, and if
it passed the password as an argument, the password would show up in `ps`. At
v0.2.8 it doesn't: it runs `security -i` and writes the command over **stdin**
(`keyring_darwin.go`).

Secrets are capped at **2048 bytes on every platform**. The platforms' own
limits differ, and macOS's limit is hit after base64 inflation, so a single cap
below all of them makes behaviour the same everywhere.

**There is no file fallback.** Where no keychain exists, such as headless Linux
without a Secret Service, the application holds secrets in `Memory` for the
session and asks again next time. A secret "encrypted" with a key stored beside
it is plaintext with extra steps.

The keychain contract test runs against `Memory` every time. It runs against
the real OS keychain only when `IKIGAI_TEST_OS_KEYCHAIN=1` is set, under a
separate service name, so an ordinary `go test ./...` never touches a
developer's keychain. Verified on macOS: it passes and leaves nothing behind.

## The local database (FR-17.3, FR-17.4)

`modernc.org/sqlite` is pure Go, so this database needs no C library of its own.

- **The file is created 0600 before SQLite touches it.** SQLite otherwise
  creates it under the umask, typically 0644, and history holds everything the
  user has run.
- **Pragmas go in the DSN, not an Exec after opening.** `database/sql` pools
  connections, and `busy_timeout` and `foreign_keys` are per-connection
  settings. WAL lets a history search read while a statement is being recorded.
  `_txlock=immediate` takes the write lock at BEGIN, so two writers never
  deadlock upgrading a read lock. The path is escaped because it goes in a
  `file:` URI, and macOS keeps it under "Application Support". There is a test
  for exactly that path.
- **Migrations are append-only and transactional.** Each one runs in a
  transaction together with its `user_version` bump. SQLite's DDL is
  transactional, so a migration that fails part-way leaves no half-built table,
  and there is a test for that. A schema **newer** than this build is refused
  rather than written to.
- **Search uses FTS5, with `_` as a token character.** That way `customer_id`
  is one word, the way someone searching their SQL thinks of it. Every search
  term is quoted and prefix-matched. FTS5 has its own operator language, so
  passing user text through verbatim would turn a stray quote or `order-by` into
  a syntax error or a different query.
- **History is capped at 50 000 entries.** The delete trigger keeps the search
  index in step.

### History is redacted; saved queries are not

History is a plaintext record of everything the user has run, so
`CREATE USER bob PASSWORD 'hunter2'` would otherwise sit on disk verbatim.
Before a statement is recorded, the lexer masks:

- the literal after `PASSWORD`;
- the literal *or bare name* after `IDENTIFIED BY` or `IDENTIFIED WITH ... AS`;
- DSN-style secrets inside any string;
- literals that span lines.

Because this goes through the lexer, a `PASSWORD` inside a comment arms
nothing, and a column named `password_hash` is not the keyword. The test
confirms the secret is absent from the stored row *and* from the search index.

**Saved queries are stored verbatim.** The user chose to save them, and
silently rewriting their script would corrupt their work.

## Logging (NFR-R4, NFR-S2)

The log is `log/slog` JSON. Redaction happens in the **handler**, which masks
the message, string attributes, errors, Stringers, and any attribute whose
*key* names a secret, whatever its value looks like. Arbitrary structs are
rendered as text and then redacted, because the JSON encoder would otherwise
marshal every field, credentials included. That loses structure but not the
secret.

Rotation is by size: 5 MiB per file, current file plus three rotated, about
20 MiB. Files are mode 0600. A rotation that fails keeps writing to the current
file: an oversized log is recoverable, a lost line is not.

## Follow-ups

- The "export diagnostics" bundle (NFR-R4) is not built yet.
- Importing connections from other tools (FR-1.12) will read `.pgpass` and
  similar files **explicitly**, when the user asks. That is deliberately
  different from the driver, which refuses to pick them up implicitly
  (ADR-0008).
- History retention could be made configurable once the preferences UI exists.
