# ADR-0119: A diff has no history

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.8 · **Requirements:** FR-7.1, FR-7.2, RISK-8
**Packages:** `internal/diff`

## Context

FR-7 asks for schema comparison and a sync script. RISK-8 names this package
by hand: a sync script is generated from what the comparison answers, and a
sync script that is wrong destroys data.

The comparison sees two models and nothing else. It did not watch anything
happen between them.

## Decisions

1. **Everything is matched by name, and a rename is a removal plus an
   addition.** Two snapshots cannot say which column became which: a rename
   and a drop-with-an-add look identical from here. The designer can tell
   them apart because it kept each column's origin as somebody edited
   (ADR-0114); a comparison of two databases has no such thing, and guessing
   would produce a `RENAME` where a `DROP` and an `ADD` were meant, or the
   reverse — the one mistake in this package that loses data.

   So a rename reads as a removal and an addition, which is the truth about
   what this can see. It is stated in the package comment, not buried.

2. **What is unknown is not a difference.** A model read from a file has no
   row counts and a model read from a server may not either, so
   `RowsEstimate` is not compared at all. Comparing a statistic against an
   absence would report a change in every table, and a report that is noisy
   everywhere is read nowhere.

   The same rule decides the databases' own names: comparing dev against
   production means comparing two differently-named databases every time, so
   the name is not a difference and the root takes the target's.

3. **A column's type is the engine's own word for it and nothing else.**
   Length, precision, scale and time-zone-ness are the driver's reading of
   that same word — `varchar(40)` is where the 40 came from — so comparing
   them as well would report one difference twice, and where they disagreed
   it would be a defect in the driver rather than a difference between two
   schemas.

4. **Identical things stay in the tree.** FR-7.2 asks for added, removed,
   changed *and* identical, and a tree holding only differences cannot be
   filtered into one that shows identical objects. Every object compared
   appears, with a status.

5. **A parent is changed when anything under it is.** A collapsed tree has to
   say there is something inside worth opening, or the first thing somebody
   does with it is expand everything.

6. **An object added or removed is one difference, with no children.** The
   sync script creates or drops it whole, and a column of a table that does
   not exist is not something anybody can choose separately (FR-7.3).

7. **Every difference carries both of its values, and only what differs.**
   Whatever draws the tree never goes back to the models, and a node's status
   can be read off the length of its detail.

8. **Text is compared as text, whitespace and all.** Two servers print the
   same view differently. Normalising here would mean this package deciding
   two strings are the same, quietly, in a place nobody can see or turn off;
   FR-7.5's ignore rules (T3.14) are where that belongs.

9. **The tree is sorted by name, and lists are compared in order.** Two
   servers list their objects in whatever order they please, so a comparison
   that depended on that would report a schema as rewritten because somebody
   rebuilt a table. Within an object, order is meaning: a key on `(a, b)` is
   not a key on `(b, a)`, and an index turned round answers a different
   question — so the direction travels with the column.

10. **`from` is what is there, `to` is what is wanted.** Added means "in `to`
    and not in `from`" — the thing a sync script would create. Getting this
    round the wrong way would generate a script that drops what somebody
    meant to add, so the property is tested both as a direction and as a
    symmetry: turning the comparison round turns every difference round with
    it and changes nothing else.

11. **The tree describes differences and does not carry the objects.**
    Generating a script (T3.12) takes the two models and a selection; keeping
    a `any` on every node so that step could skip a lookup would put engine
    objects in a structure whose whole job is to be drawn.

## Consequences

- Nothing here reads a server or writes a statement, so the comparison is
  exercised in full without either database being reachable. That is what
  RISK-8 asks for and it is why the package is pure.
- Comparing two different engines will be noisy: `int4` against `integer`,
  and every engine-specific attribute. FR-7.5's ignore rules are the answer,
  and until they exist the comparison is honest about the noise rather than
  hiding it.
- A duplicate name within one model keeps the first and ignores the rest. A
  duplicate is that server's business, and reporting it as a difference in
  the other database would report the wrong database's problem.
- `Snapshotter` is still unimplemented by every driver, so nothing yet feeds
  this from a live database. T3.9 is where that lands.
