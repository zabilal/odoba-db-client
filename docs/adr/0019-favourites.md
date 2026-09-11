# ADR-0019: Favourites live in the settings file

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T1.46 · **Requirements:** FR-2.6, FR-15.4 · **Packages:** `internal/store`, `internal/app`, `internal/ui/shell`

## Context

FR-2.6 asks for pinned or favourite objects, surfaced above the tree. A
favourite is a person's lasting choice about an object on a connection: it
should outlive the window, the session and the local database.

## Decisions

1. **Favourites are kept in the settings file**, beside the connections they
   point into, not in the local database with the session (ADR-0011 §10).
   The session is how a window was left; a favourite is a choice, and it
   should survive the local database being cleared. Each names its
   connection, the object's kind and path, and a label. The file refuses a
   favourite on a connection it does not hold, or one listed twice, so
   deleting a connection deletes its favourites in the same write and
   nothing is left pointing at nothing.

2. **Only objects with rows can be favourites**: tables, views and
   collections. Choosing one opens its rows, connecting first if need be,
   as a reopened tab does. A schema or a database would have to be shown in
   a tree that may not have loaded it, which would mean connecting to find
   it.

3. **Explorer › Favourite (⌘D, in the Connection menu) toggles the selected
   object**, ticked while it is one. Favourites are listed above the tree
   only when there are any (UX principle 2). Each row names the object and
   its connection and opens it, and has a Remove button with words
   (ADR-0006 addendum).

4. **The settings version stays 1.** The field is additive, and a build
   older than this one reads the file and ignores it. Such a build would
   drop favourites the next time it wrote the file. With nothing released
   yet, freezing the whole file against older builds was not worth that.

## Verification

The store refuses a favourite on an unknown connection, one listed twice
and one naming no object. Deleting a connection drops its favourites. In
the shell, ⌘D adds and removes a favourite with its tick, its row opens the
table, a database cannot be one, and favourites are on disk, come back at
the next start and go with their connection.

## Not decided here

Reordering favourites, grouping them by connection, and favourites for
schemas and databases.
