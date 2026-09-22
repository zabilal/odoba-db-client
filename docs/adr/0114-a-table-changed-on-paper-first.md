# ADR-0114: A table changed on paper first

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.1 · **Requirements:** FR-6.1, FR-6.4
**Packages:** `internal/app`, `internal/ui/shell`

## Context

FR-6.1 asks for a table designer: columns with their names, types, lengths,
nullability, defaults, identity and comments. FR-6.4 asks that every
structural change previews its DDL before anything runs, and the task list
puts that three tasks later.

So the first question is what a designer is, if it cannot yet run anything.

## Decisions

1. **A design is two tables, not a list of edits.** The table as it was read
   and the table as somebody has made it, side by side. Asked what changed,
   it compares.

   A list of edits would answer differently. Somebody who changes a column's
   type and changes it back has made no change, and an editor that remembered
   the two steps would offer to run something for nothing — and, once the
   preview exists, would show them DDL for it.

2. **Each column remembers which column it came from.** Position cannot
   answer that: drop a column in the middle and every column after it moves
   up one, so matched by position each would read as the column before it
   renamed. That is how a rename — which carries a column's data — becomes a
   drop and an add, which does not.

   The first version of this was matched by position, and its test caught it
   immediately. It is the defect this decision exists to prevent, and it
   would have destroyed data.

3. **The editor runs nothing and says so.** Until FR-6.4's preview exists
   there is nothing to run, and a designer that quietly ran statements would
   be the thing UX principle 6 forbids. What a design holds is visible in the
   footer, in the words of the change: `id renamed to identifier`,
   `name dropped`.

4. **A type is a box to type in, not a list to choose from.** A type is the
   engine's own word for it — `varchar(40)`, `numeric(10,2)`, `timestamptz` —
   and the seven engines here do not agree on those. A chooser would have to
   be wrong somewhere; the box is right everywhere, and what a dialect will
   not accept the preview will say before it runs.

   A type typed over by hand loses the length, precision and scale that were
   read with it. Keeping them would let a column widened from `varchar(40)`
   to `varchar(80)` still say 40 — a number nobody typed, contradicting the
   word beside it.

5. **A NOT NULL column is refused on a table that has rows.** Every existing
   row would need a value and the designer has none to give. It says to add
   it nullable or give it a default, which is the thing to do. A table whose
   size is unknown is treated as holding rows: a question somebody can answer
   beats an error from the server they cannot.

6. **Dropping a column the primary key is made of is refused.** It would take
   the key with it, which is a larger change than the one being asked for,
   and belongs to whoever is editing the key (T3.2).

## Consequences

- The editor is exercised without a server and, when it comes, the DDL will
  be exercised without a window. `Design.Changes` is the seam between them.
- Two tabs cannot design one table: opening a design on a table that already
  has one brings that one forward, because two designs of the same table
  could disagree and only one of them could run.
- The designer is the fifth thing in this application with a finished half
  and no way to finish the act — but the only one whose other half is a
  task already in the list, three along.
