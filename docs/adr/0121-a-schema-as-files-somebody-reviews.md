# ADR-0121: A schema as files somebody reviews

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.10 (format, reader and writer; T4.8 inherits them) · **Requirements:** FR-7.1, FR-7.6
**Packages:** `internal/schemafile`, `internal/app`

## Context

FR-7.1 asks to compare a live database against a saved model. There is no
saved model to compare against until something writes one, so T3.10 has to
settle the format. FR-7.6 names it — a VCS-friendly file tree, one file per
object — and it is the only format the requirements name, so choosing
anything else now would guarantee a migration later. T4.8 keeps the way in
from the window.

## Decisions

1. **One file per object, in a directory per schema and a directory per
   kind.** The point is version control. A schema in one file produces one
   enormous diff for one added column, and a review of that is nobody reading
   it. One file per object means a commit shows the objects that changed.

   The directories are named with the plurals the explorer uses — `tables`,
   `views`, `routines`, `sequences`, `types` — so somebody reading the tree
   in a repository reads the same words as in the window.

2. **The file's name is a label; the object's real name is inside it.** That
   is what lets the naming be readable rather than reversible: everything
   that is not a letter, digit, dot, dash or underscore becomes a dash, runs
   of dashes collapse, and a name Windows keeps for a device or will not end
   in gets a leading underscore. Reading a model never decodes a path.

3. **Two objects that would be one file are refused, before anything is
   written.** The check is case-insensitive, because a pair of names safe on
   Linux collides on macOS and Windows, and a model saved on one and read on
   the other would quietly be missing an object — which the comparison would
   then report as something somebody had dropped.

   Disambiguating instead was the alternative and it is worse: a suffix that
   depends on what else is in the schema changes when something unrelated is
   dropped, and every file after it moves.

4. **The whole tree is built beside the target and swapped in.** Writing
   object by object into the live directory leaves, if anything goes wrong, a
   model that is neither the old one nor the new, and a comparison against it
   would be wrong in a way nobody could see. Swapping also means an object
   dropped from the database is gone from the model rather than left behind
   to compare as one somebody should put back.

5. **A directory holding something else is refused.** Saving replaces
   everything under it, so pointing it at the wrong directory would delete
   somebody's work. Empty is fine, and so is one that already holds a model.

6. **Structure is saved; statistics are not.** A row count changes every day
   and is never a difference worth committing. A table read back says it does
   not know how many rows it has, which is the truth — a file does not — and
   is also what stops it comparing against a live database as a change.

7. **There is a format marker in the root file, and reading refuses a
   version it does not know.** A model saved by a later version says so,
   rather than being read wrongly by an earlier one. Reading a directory that
   holds no model says that too, rather than answering an empty database —
   which would compare as every object having been dropped.

8. **The saved model is the wanted state.** It is the one in version control,
   reviewed and agreed, so it goes on the `to` side of the comparison and
   Added is what the live database is missing. A database that cannot be read
   is reported before the model is opened, naming which side it was.

## Consequences

- T4.8 inherits the format, the reader and the writer; what is left for it is
  the way in from the window and from the CLI (FR-7.7).
- Nothing sorts the files a directory hands back, because `os.ReadDir`
  already returns them sorted by name — a second sort would be a line no test
  could tell the absence of. The order is asserted instead.
- Indexes and triggers are saved inside the table they belong to, because
  that is where the canonical model puts them. A commit touching an index
  therefore touches its table's file.
- What `Describe` cannot read is not in a saved model either. On PostgreSQL
  that is nothing, because it snapshots; on an engine that is walked, its
  user types are missing (ADR-0120).
