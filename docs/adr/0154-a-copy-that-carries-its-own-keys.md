# ADR-0154: A copy that carries its own keys

**Status:** Accepted · **Date:** 2026-09-24
**Tasks:** T4.19 · **Requirements:** FR-17.6, FR-1.5, NFR-S1, NFR-S7
**Packages:** `internal/store/secrets`, `internal/app`, `internal/ui/shell`

## Context

FR-17.6 asks for portable mode: all state alongside the binary, so that the
application can be carried on a stick and leave nothing behind. The paths have
resolved that way since Phase 1 — settings, database, logs and cache all go in
a directory beside the executable when a marker file is there.

The secrets did not. They went to the machine's keychain like any other run,
which is the opposite of portable in both directions: they stay on every
machine the copy is carried to, and they travel with it nowhere.

`internal/store/secrets` says why nothing was done about it: "There is
deliberately no file fallback... a secret 'encrypted' with a key stored beside
it is plaintext with extra steps." That is right, and it is the whole problem:
a file beside the binary needs a key that is not beside the binary.

## Decisions

1. **A portable copy keeps its secrets beside itself, and only sealed.** The
   key is the vault lock's (ADR-0152), derived from a passphrase that is on no
   disk at all. The package's rule stands: what is written here is never
   plaintext, and the key is never here.

2. **Until there is a passphrase, a portable copy keeps nothing.** Its vault
   reports that it does not persist, which is the same state as a machine with
   no keychain — a path this application has had since the beginning, with its
   own words in the window: each password is asked for once a session and kept
   no longer. Putting a lock on promotes what the session is holding into the
   file, sealed, because taking a lock on reads every secret and writes it back.

3. **A portable lock cannot be taken off.** There is nowhere for the passwords
   to go: the only place this copy has is a file beside itself, and that file
   is only ever written sealed. Changing the passphrase is offered, which is
   the thing somebody actually wants.

4. **The file is one JSON object of sealed values**, written whole or not at
   all and readable only by its owner. It holds what it is given; sealing is
   the vault's, above it, which keeps the one place that knows about
   ciphertext the one place that knows about passphrases.

## Consequences

- A portable copy with no passphrase is still useful: connections, history,
  saved queries and layouts all travel, and each password is typed once a
  session. That is the trade somebody makes by not setting one, and the form
  that offers the passphrase says so.
- The secrets file is not in the backup archive either, and for the same
  reason the keychain is not (ADR-0153): a backup carries no password.
  A portable copy is itself the way to carry them, which is what it is for.
- Nothing migrates between the two. A copy that becomes portable does not find
  the machine keychain's secrets, and a portable copy's file means nothing to
  an installed one. Both are the same statement: secrets live where the person
  chose to keep them, and nothing moves them quietly.
