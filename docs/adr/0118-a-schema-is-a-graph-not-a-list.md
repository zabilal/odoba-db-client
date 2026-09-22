# ADR-0118: A schema is a graph, not a list

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.7 · **Requirements:** FR-6.7, FR-2.4
**Packages:** `internal/app`, `internal/ui/shell`

## Context

FR-6.7 asks for the full DDL of any object or of a whole schema. One object
is nearly free: `Describe` then `CreateObject`, both of which exist.

A whole schema is not, and the reason is not volume. The objects in a schema
refer to one another, and a script is a sequence. Written in the order the
catalogue happens to list them, a schema script fails partway through and
leaves half a database — which is exactly the failure ADR-0115's ordering
rules exist to prevent one object at a time.

## Decisions

1. **The script is written, never run.** It opens in a query tab as unsaved
   text, like every other Script As. A script is something to read, keep, put
   in a repository or edit and run elsewhere; the moment it could also
   execute it would need every guard a change needs, and it would stop being
   a script. That is also why it does not go through the DDL preview — there
   is nothing to preview, because nothing is going to run.

2. **Classes come in a fixed order: sequences, tables, views, materialized
   views, routines, triggers.** A column default may call a sequence. A view
   selects from a table. A trigger needs both a table and a routine, so it is
   last. The order is a constant here rather than something derived, because
   it is a property of SQL and not of any particular schema.

   A class with no place in that order is skipped before its objects are
   listed, not after: what will not be written is not read.

3. **Foreign keys are taken out of the tables and added at the end.** Two
   tables referring to each other is ordinary, and no sequence of
   `CREATE TABLE` statements satisfies a cycle. So every table is written
   without its references and every reference is added once all the tables
   exist.

   It is done by rendering the table with its keys removed and then asking
   the generator for the change from that table to the real one. The
   generator already knows how to add a constraint to a table missing one, so
   there is no second way of writing a foreign key here to get wrong. A table
   with no keys goes the same way rather than taking a short cut round it: the
   short cut renders exactly the same statements, and would be a branch
   nothing could tell from the other one.

4. **Views are sorted by what they select from.** Alphabetical order is
   wrong roughly half the time. The sort uses the same `DependencyReader`
   T3.6 built, so one catalogue read answers both "what breaks if I rename
   this" and "what has to exist before this".

5. **A source that cannot say leaves the order as it was listed.** Guessing
   at an order and being wrong produces a script that fails halfway, which is
   worse than one whose order somebody has to fix themselves. The same holds
   for a cycle among views: the sort refuses rather than emitting something
   that looks ordered and is not.

6. **A whole schema is asked for on whichever node its objects are listed
   under.** PostgreSQL puts them under a schema; MySQL and SQLite put them
   straight under the database. The objects are the same objects, and the
   code walks class folders rather than assuming a depth.

7. **The script says why it is in that order.** The order is the only part of
   a generated script somebody cannot see for themselves, and a reader who
   does not know why the foreign keys are at the bottom will assume it is a
   mistake and "fix" it. Four comment lines, at the top.

   Nothing is said in the status line afterwards: opening the tab redraws it
   as the connection's own, so anything put there would be written and wiped
   in the same instant.

## Consequences

- Only PostgreSQL implements `DDLGenerator`, so only PostgreSQL writes DDL.
  On the other engines both commands are unavailable rather than silently
  producing nothing.
- The view sort costs one dependency read per view. On a schema with many
  views that is many round trips, and it is the only way to get the order
  right.
- Routines are emitted in catalogue order. A routine with a `BEGIN ATOMIC`
  body that calls another routine would need the same treatment views get;
  PL/pgSQL bodies, which are the common case, do not care.
- Privileges, ownership, row-level security and table storage parameters are
  not written. What is generated is what `Describe` reads, and it reads
  structure.
