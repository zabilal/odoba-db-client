# ADR-0123: A script for the parts somebody agreed with

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.12 · **Requirements:** FR-7.3, RISK-8
**Packages:** `internal/app`, `internal/diff`, `internal/ui/shell`

## Context

A comparison says what differs. FR-7.3 asks for a script that closes it, with
the differences chosen one by one.

The choosing is the requirement, not a refinement of it. A schema comparison
is read and then argued with: this column is wanted, that table is somebody
else's and is not. A script that takes everything or nothing is one nobody
can use on a database they share.

## Decisions

1. **A selection is made of the names the comparison gives its nodes, and
   both sides use the same names.** A comparison has no identifiers of its
   own — it is two models, and neither of them has one either. What a node
   has is its place: the kinds and names down to it. `diff.ID` builds that
   path, the tree draws with it and the script is written from it, so a tick
   in the window and a statement in the script cannot mean different things.

   The kind is in the path because a view and a table can share a name.

2. **A change to part of an object is a change to the object.** A column is
   not altered on its own: the statement alters the table it is in, and the
   generator writes it by comparing two tables. So the parts chosen inside an
   object are gathered onto it, and the script renders the table as it is
   against a copy of it with exactly those parts taken from the other side.

   Choosing three columns of eight therefore renders three, because the other
   five are identical on both sides of the comparison the generator makes.
   There is no second code path for partial changes, and no second way to
   write an ALTER to get wrong.

3. **What is dropped goes first.** The order is a whole schema's order
   (ADR-0118) with that one addition: a table on its way out can be exactly
   what stops a table on its way in from being made — the same name, or a
   constraint on it — and dropping afterwards would fail halfway with the
   mess already made.

4. **Only a difference can be chosen.** There is nothing to write for
   something that is the same on both sides, and a tick box that does nothing
   is a tick box somebody will tick and then wonder about. Choosing an object
   chooses what is under it, because a table being made is one decision and
   its columns are not separate ones.

5. **An incoherent selection is refused, and says which two things it is
   about.** A column chosen inside a table the same script is about to create
   would render an ALTER against something that does not exist yet. The
   window cannot make that selection — an object added is one difference with
   no children (ADR-0119) — so this is a guard on the shape of a selection
   rather than on anything a person can do, and it is what keeps it that way.

6. **The comparison carries both models back with it.** The tree describes
   differences and deliberately does not carry the objects, so a script needs
   the models. Reading them again would mean writing a script for a database
   that had moved since somebody read the comparison, which is the same
   mistake ADR-0115 refuses one object at a time.

7. **The script is written, never run.** It opens in a query tab like every
   other script this program writes, with a header saying how many
   differences it holds and that nothing has run. T3.13 is where applying it
   lands, and applying will go through the preview and the guard like every
   other structural change.

## Consequences

- Only PostgreSQL renders DDL, so only PostgreSQL writes a sync script. On
  the other engines a comparison still reads and still shows what differs.
- A routine, a view, a sequence and a trigger are sent whole rather than
  altered in parts, because they are their own source (ADR-0116) and have no
  parts to choose.
- Nothing short-circuits an empty selection: gathering nothing answers
  nothing, and a guard for it would be a line no test could tell the absence
  of.
- The tick boxes are in the tree rather than in a list beside it, so what is
  chosen is read in the same place as what differs. The cost is that a row is
  three widgets instead of two.
