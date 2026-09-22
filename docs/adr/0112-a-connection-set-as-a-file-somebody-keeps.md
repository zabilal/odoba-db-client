# ADR-0112: A connection set as a file somebody keeps

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T2.89 · **Requirements:** FR-1.13, FR-1.5, NFR-S2, NFR-S4
**Packages:** `internal/importer`, `internal/app`

## Context

FR-1.13 asks for a connection set to be exported and imported as JSON, with
secrets excluded by default. It is how a team shares the connections everyone
needs, how somebody moves to a new machine, and how a set of connections ends
up in a repository beside the code that uses them.

That last one is why the default matters. An exported set is a file that gets
attached to messages and committed to repositories, which is a career a
settings file does not have.

## Decisions

1. **The format is written out field by field, not serialised from the
   settings struct.** A file other people hold is a contract. Exporting
   `SavedConnection` directly would mean any internal rename silently changed
   what everyone's files look like, and would carry fields that mean nothing
   outside this installation. The set has its own types, its own `kind` and
   its own `version`, and a file from a newer version is refused rather than
   half-read.

2. **Secrets are excluded unless asked for, and the file says which it is.**
   `includes_secrets` is written into every set, true or false. Somebody who
   opens a file months later, or receives one, can see what they are holding
   before they forward it.

3. **What is missing is still named.** A set exported without secrets still
   carries `needs_secrets`: the names, never the values. An import can then
   say this connection still needs a password, instead of leaving somebody to
   find out at connect time with an error from a driver.

4. **An export that was asked to carry credentials and cannot read one
   fails.** If the keychain is locked or a secret has gone missing, the export
   stops and says which connection and which secret. The alternative is a file
   that looks complete, is not, and is discovered to be wrong by whoever it
   was sent to.

5. **Folders travel as names. Identifiers never leave.** A folder ID means
   nothing in somebody else's settings, and neither does a connection's. On
   the way in a folder is matched by name — compared as people read them, so
   `Work` and `work` are one folder rather than two that look identical in the
   tree — and made if it is not there.

6. **Importing a set is importing.** `ReadSet` answers the same `Found` as
   every other source, so saving one goes through the same path as an import
   from DBeaver or a password file, with ADR-0111's rules intact: the same
   place and the same account is the same connection, credentials are kept
   only if the import was told to, and nothing is dropped in silence.

## A defect this corrected

`SavedConnection.Folder` holds a folder **ID**. The DBeaver importer written
in T2.88 put DBeaver's folder *name* there, and its test asserted exactly
that, so the test enshrined the defect rather than catching it. An imported
connection would have been filed under a folder that does not exist, and the
saved-connection panel displays that field directly, so it would have shown
the raw string.

Carrying the folder as a name on `Found` and resolving it when saving fixes
the importer and is what an exported set needed anyway. The lesson is the
older one: a test written from the same misunderstanding as the code confirms
the misunderstanding.

## Consequences

- `Found` carries `Secrets` as a map rather than a single password, because a
  connection can need more than one — an SSH passphrase, a registry password,
  a token — and a set that carried only the password would export
  incompletely while looking complete.
- Export and import are both available without a user interface, which is the
  same gap SSH tunnels, cloud identities and tool imports have. It now spans
  four features.
