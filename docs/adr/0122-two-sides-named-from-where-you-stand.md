# ADR-0122: Two sides, named from where you stand

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.11 · **Requirements:** FR-7.2, UX-6
**Packages:** `internal/ui/shell`

## Context

The comparison engine answers a tree in which everything appears, identical
objects included, with a status on each node and, where something differs,
both of its values (ADR-0119). FR-7.2 asks for that tree in the window, with
added, removed, changed and identical, and the detail of one object beside
it.

The engine's words are *added* and *removed*, which are exact and
directional: added means "in `to` and not in `from`". In front of somebody
looking at two databases, they are ambiguous. Added to which one? Removed by
whom?

## Decisions

1. **The two sides are named from where the person is standing.** The
   connection is *here*; the saved model is the other side. So an object the
   engine calls Added reads as "missing here", and one it calls Removed reads
   as "only here". The sentence beside it says it again in full: "In the
   saved model and not in this database."

   This is the decision with the worst failure mode in the feature. Get it
   backwards and somebody adds what they meant to drop, having read the
   screen correctly.

2. **The whole comparison is kept and the filter changes only what is
   drawn.** That is why the engine keeps identical objects: a tree of
   differences alone cannot be filtered into one that shows what did not
   change. Filtering trims nothing — switching back shows everything again
   without comparing anything a second time.

3. **A filter keeps the way down to what it draws.** Filtering to what is
   missing and drawing only missing nodes would hide the schema and the table
   above a missing column, and then the column cannot be reached. Every node
   records which statuses are somewhere beneath it, and a node is drawn when
   it matches or something under it does.

4. **The other side is a saved model, not another connection.** A model in
   version control is the case this is for — the one somebody reviewed and
   agreed — and it is the only side certainly available, because another
   connection may not be open. Comparing two live databases is what
   `app.Compare` does and what T3.9 proved; giving it a way in is a second
   picker and a second set of decisions, and no task claims it yet.

5. **The model is chosen by picking a file inside it.** A saved model is a
   tree of files and the platform's dialog chooses files, so what is chosen
   names the directory it is in. A directory picker means
   `canChooseDirectories` on macOS and the portal's own flag on Linux, which
   is a change to `filedlg` that saving a model will want too (T4.8).

6. **A value that is absent reads as absent.** An empty cell beside a full
   one is indistinguishable from a value that happens to be blank.

7. **The footer says the comparison changed nothing.** It reads both sides
   and writes to neither, and a screen full of the words missing, only and
   changed invites the question.

## Consequences

- Comparing the same database against the same model twice brings the first
  tab forward. Two comparisons of one pair could disagree — the database may
  have moved between them — and reading both would be reading one for
  nothing.
- A comparison that could not be made fails the tab rather than drawing an
  empty tree, which would read as a database with nothing in it.
- Nothing here generates a script. T3.12 selects differences and T3.13 runs
  or saves what it writes; this only says what differs.
- `canCompareSelected` checks that the connection is open as well as asking
  whether it can be compared. The second question already answers the first,
  because a connection that is not open has no source and `app.CanCompare`
  says no to nothing — but the two are different questions and the code says
  both, rather than leaning on a nil check three layers away.
