# ADR-0152: A lock that seals

**Status:** Accepted · **Date:** 2026-09-24
**Tasks:** T4.17 · **Requirements:** NFR-S7, FR-1.5, NFR-S1
**Packages:** `internal/app`, `internal/store`, `internal/ui/shell`

## Context

NFR-S7 asks for an optional app-level lock over the credential vault. Without
one, each secret is in the OS keychain under the user's login, and anything
running as that user can read it — this application and everything else. That
is the platform's bargain and the right default: the alternative is a
passphrase at every start, which is not what most people want from a database
client.

A lock could be a gate: ask for a passphrase and then hand out what the
keychain holds. That is worth very little. Whoever can run the application can
also run `security find-generic-password`, and a gate in front of a door with
no wall is a curtain.

## Decisions

1. **The lock seals rather than gates.** With it on, what the keychain holds is
   the secret encrypted with a key derived from the passphrase: Argon2id to the
   key, AES-256-GCM to the ciphertext. The keychain entry on its own is no
   longer the secret, and neither is this application without the passphrase.

2. **Nothing that could produce the passphrase is stored.** What is saved
   beside the connections is a salt and a verifier — a hash of the derived key,
   which says whether a passphrase is the right one and nothing about what it
   is. The key exists in memory while the lock is open and nowhere else.

3. **Each secret says for itself whether it is sealed**, through a versioned
   prefix. Putting the lock on rewrites every secret, and a run that stops half
   way therefore leaves a vault that still reads: what is sealed is opened with
   the key, and what is not is read as it is. This is also why the setting is
   saved before the resealing starts, and cleared after the unsealing ends —
   whichever half is done, the other half is still readable.

4. **The cost is said where the lock is offered.** A passphrase nobody
   remembers is passwords nobody can read, here or anywhere, and the only way
   on is to type them again. Nothing recovers them, and the form says so before
   the button.

5. **A locked vault refuses before reading, not after.** A secret stored before
   the lock went on is not sealed, and a locked vault that handed that one over
   would be a locked vault handing a secret over.

6. **A secret typed for this session is neither sealed nor held back.** It is
   in this process because somebody just typed it here, and sealing it would be
   encrypting a thing with a key derived from a passphrase in order to store it
   in memory next to the key.

7. **The window asks before it puts the last session back.** Reopening the
   tabs first would be a window full of connections that failed for a reason
   nobody had been asked about yet. Going on without the passphrase is offered
   too: connections that need no password still work, and each other password
   can be typed as it is needed.

## Consequences

- Biometric is not offered. Touch ID is LocalAuthentication, which is
  Objective-C, and this application is Go and nothing else (ADR-0001). A
  passphrase is what a pure-Go build can hold to.
- There is no idle re-lock. Shutting the vault is a command somebody runs, and
  the lock shuts itself at every start. A timer that shut it while somebody was
  working would be a timer they turned off.
- Unlocking costs the Argon2id derivation — a tenth of a second, once a start.
  That is the point: it is a great deal of work for each guess and nothing for
  the one person who knows the passphrase.
- The lock covers what the vault persists, which is connection secrets. It is
  not a lock on the application: the local database holds query history and
  saved queries in the open, as it did before.
