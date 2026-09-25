# ADR-0153: A backup without the passwords

**Status:** Accepted · **Date:** 2026-09-24
**Tasks:** T4.18 · **Requirements:** FR-17.5, NFR-S1, FR-17.1
**Packages:** `internal/app`, `internal/store/localdb`, `internal/ui/shell`

## Context

FR-17.5 asks for backup and restore of all app data as a single archive: a new
laptop, a reinstall, or an afternoon somebody wants to be able to undo.

What the application keeps is two files: the settings, which hold the
connections, folders, favourites and changed shortcuts, and the local database,
which holds query history, saved queries, unsaved query text, saved views,
workspaces and the session. The passwords are in neither. They are in the
operating system's keychain (ADR-0009, NFR-S1).

## Decisions

1. **The archive is those two files and a manifest, as a zip.** A zip because
   it is the format a person on any platform can open by double-clicking and
   see what is in it — which is part of being able to trust it.

2. **No password is in it, and the manifest says so in words.** An archive that
   carried them would be the plaintext password file a keychain exists to
   avoid, and "back up my data" and "back up my passwords" are the same
   sentence to most people. So the archive says, in a field somebody reading it
   will find: no passwords are in this archive, they are in the keychain. A
   restored connection asks for its password the first time it is used — on the
   machine it came from, the keychain still has it and nothing is asked.

3. **The database is copied through SQLite, not read off the disk.** `VACUUM
   INTO` runs while the database is open and in use, and what it writes is the
   database as of when it ran, with no `-wal` beside it to carry. Copying the
   file by hand would copy whatever was mid-write.

4. **A restore stages both files beside their targets and moves them in
   afterwards**, and what was there is renamed `.before-restore` rather than
   removed. A failure part way leaves what was there, and a restore somebody
   did not mean is one they can undo.

5. **A restore ends the session.** Everything the window is holding — the open
   tabs, the workspace, the connections — is about a database that has just
   been replaced, so the window says what it is about to do, does it, and
   closes. The background writer is stopped first, so nothing this session was
   keeping is written into the database that has gone.

6. **An archive says what wrote it and when**, and one from a later version is
   refused rather than half read.

## Consequences

- Restoring onto a machine with nothing on it works, which is what a backup is
  for: a new laptop gets every connection, query and layout, and asks for each
  password once.
- The keychain is not restored, so a restore cannot be used to move passwords
  between machines. That is the point of where they are kept, and the manifest
  says it.
- The archive is not encrypted. It holds no secret, and encrypting it would
  need a passphrase — which is the vault lock (ADR-0152), and a different
  question about a different thing.
- Nothing is offered to restore a single connection or one saved query out of
  an archive. The archive is a zip and its settings file is JSON, so that is
  a text editor away; a chooser for it is a feature nobody has asked for yet.
