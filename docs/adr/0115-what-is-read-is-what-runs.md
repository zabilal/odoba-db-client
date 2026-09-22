# ADR-0115: What is read is what runs

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.4 · **Requirements:** FR-6.4, FR-6.7, UX-6, NFR-S4, FR-4.9
**Packages:** `internal/source`, `internal/source/drivers/postgres`, `internal/app`, `internal/ui/shell`

## Context

FR-6.4 and UX principle 6 say every structural change previews its DDL
before execution. T3.1 to T3.3 built a designer that changes a table on paper
(ADR-0114); this is the half that sends it.

`source.DDLGenerator` had been declared since the driver contract was
written, and nothing implemented it. Writing the first implementation is what
found the decisions below — three of them by breaking a test.

## Decisions

1. **The list read is the list sent.** The preview holds the statements and
   hands those same statements to the server. A preview that rendered again
   on the way to running would be a preview of something else, and the gap
   between the two would be exactly where a surprise lives.

2. **Columns are matched by name, and a rename is asked for on its own.**
   Two tables cannot say which column became which: a rename and a
   drop-with-an-add look identical, and guessing wrong throws a column's data
   away. So `AlterObject` matches by name and never guesses, and whoever does
   know — the designer, which keeps each column's origin — renames first
   through `RenameColumn` and hands over a table already using the new names.
   A schema diff (FR-7) has no such knowledge and will render a rename as a
   drop and an add, which is the honest answer there.

   The first version matched by position. Its test caught it within minutes,
   for the same reason ADR-0114 gives: drop a column in the middle and every
   column after it moves up one.

3. **The order is the point.** DDL is a sequence, not a set, and the wrong
   sequence fails halfway and leaves a table nobody meant to build. Renames
   first, so everything after names what things are now called. Then what is
   dropped, constraints and indexes before the columns they are made of.
   Then the columns. Then what is added, after the columns it is made on.
   Comments last: they depend on everything and nothing depends on them, so
   one that fails takes nothing with it.

4. **A statement names the table by its ref, not by the table's name.** A
   `model.Table` carries a bare name. Rendering from that alone produces
   statements for whichever table of that name the search path reaches —
   which is the right one until it is not. `CreateObject` and `AlterObject`
   take the ref for the same reason `DropObject` always did. Found by a live
   test against a table in a fixture schema.

5. **An index that backs a constraint is left to it.** PostgreSQL builds an
   index to enforce a primary key or a unique constraint, reports it among
   the table's indexes under the constraint's own name, and will not let it
   be dropped on its own. Rendering it as an index as well sends a
   `DROP INDEX` for something that no longer exists. Also found live.

6. **Not one transaction, and it says so.** Some engines cannot roll DDL back
   at all, and one that can would still leave the rest half done. Rather than
   promise atomicity that depends on which engine somebody is on, this
   reports how far it got and which statement stopped it. What that leaves is
   a table part-way changed, which is the truth and can be looked at.

7. **Every statement goes through the session, so the guard sees it.** A
   read-only connection refuses them all; a production connection asks first,
   with the name typed out (ADR-0113). Nothing has run when it asks, because
   the guard refuses before the first statement reaches the server.

8. **After they run, the table is read again.** What it is now is what the
   server says it is, not what was asked for: PostgreSQL answers `varchar(40)`
   as `character varying(40)`, and a statement can succeed and still leave
   something other than what was typed.

## Consequences

- PostgreSQL is the only driver that renders DDL. The others say so — the
  capability is what the designer asks — and the preview tells somebody
  before they edit for an hour rather than after.
- The generator is proved twice: against expected text, which says what it
  renders, and against a live server, which says whether the text runs.
  Three of the decisions above came from the second kind.
