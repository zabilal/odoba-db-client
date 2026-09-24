# ADR-0149: A mark, not a selection

**Status:** Accepted · **Date:** 2026-09-24
**Tasks:** T4.14 · **Requirements:** FR-2.8, FR-2.4, FR-10.2
**Packages:** `internal/ui/explorer/view`, `internal/ui/shell`, `internal/app`

## Context

FR-2.8 asks for multi-select in the explorer, for batch operations: drop N,
export N, script N. Somebody retiring a schema wants the DROPs for forty
tables; somebody handing data over wants a dozen tables as CSV.

Fyne's `widget.Tree` selects one row at a time. Nothing in it takes a
modifier-click, and the glfw driver hands each click to one object: the row
under the cursor either handles it or the tree does, not both. Multi-select
the way Finder does it is not available without a tree of our own.

## Decisions

1. **A batch is a set of marks, not a selection.** A marked row carries a tick
   in front of its label and is drawn in bold; the marks are the explorer's
   own state, kept as branches open and close and as the tree reloads. The
   selection goes on meaning what it has always meant — the one row the
   commands act on.

   This is better than a selection would have been, and not only cheaper: a
   batch built by marking is built on purpose, one row at a time, and nothing
   is in it because of where the cursor happened to be. A mis-click does not
   quietly add a table to a list of DROPs.

2. **The tick is a character, not a colour.** A colour alone is not a sign
   somebody can see or have read out (NFR-A2), and the tick is in the row's
   text, so a screen reader says it.

3. **How many are marked is in the status line, not in a message.** The status
   line is rewritten whenever anything syncs, so a message there lasts until
   the next keystroke. What is marked is part of what the window is showing,
   so it is part of what the status line says, for as long as it is true.

4. **A batch is on one connection.** Marks spread over two connections are two
   batches: a script belongs to a query tab, and a tab belongs to one
   connection. Rather than do half of it or ask which, the batch commands are
   offered only when every mark is on one connection.

5. **Every batch operation writes, and none of them runs.** Script as SELECT,
   as CREATE and as DROP all open one script in a query tab, unsaved, for
   somebody to read, keep or edit (ADR-0011 §15). A DROP for forty tables is
   exactly the thing nobody should be one menu item away from.

   An object the connection cannot write that statement for is named in the
   script as a comment rather than left out of it, because a script quietly
   short of what was marked is the one somebody runs thinking it is all of it.

6. **Exporting the batch is one file per object**, named after the object, in
   the directory chosen — chosen by naming the first file, which is the
   bargain a file dialog strikes when what is wanted is a directory
   (ADR-0121). Each is a task of its own, so one failing leaves the rest
   running and says which failed.

## Consequences

- An export that belongs to no tab now runs under the window's context rather
  than a tab's, and says what it did in the status line. Both were already
  possible — a saved model is a task with no tab — and this is the first
  export to use them.
- The batch export does not count the rows first. Counting every marked table
  before exporting any would double the round trips for a progress bar; the
  task says how many rows have gone rather than how far through it is.
- Marks survive a tree reload because they are IDs, not nodes. A mark on a row
  that has gone since is left out of the batch rather than failing it: it
  names nothing, and nothing is what is done to it.
- Nothing yet marks a whole class folder's contents at once. Marking forty
  tables is forty actions, which is the cost of a batch built on purpose; if
  that becomes the common case, "mark everything in here" is the command to
  add, not a change to any of this.
