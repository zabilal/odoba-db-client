# ADR-0069: Making and unmaking indexes

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.36 · **Requirements:** FR-6.3, FR-6.4, FR-12.1 · **Packages:** `internal/source`, `internal/source/drivers/mongo`, `internal/app`, `internal/ui/shell`

## Context

FR-12.1 asks for index management on a collection, and FR-6.4 requires every
structural change to show what it would do before it does it. A collection
has no DDL to alter: an index is made and unmade by a call of its own. This
is the first structural change the application makes at all — the table
designer (3.A) is later — so it sets the shape for the rest.

## Decisions

1. **`source.IndexManager` is optional, paired with `Schema.Indexes`**, and
   checked by the conformance suite as every other pair is. It is a refinement
   of introspection rather than of writing: an index is not a row.

2. **A change is planned, then applied.** `PlanIndex` and `PlanDropIndex`
   return a `WritePlan` — the same reviewable form a changeset takes
   (ADR-0031) — carrying the call in `Statement.Op` (ADR-0066) and the
   mongosh line a person reads in `SQL`. Nothing reaches the server until
   `ApplyIndex` runs the plan.

3. **A structural change is guarded as `AccessDDL`.** Nothing runs on a
   read-only connection, and a production connection asks again, with the
   consent carried into a plan made for it — the same two steps a commit
   takes, in the same words (FR-4.9, NFR-S4).

4. **Some drops are refused outright**: every index at once (`*`, which the
   driver would obey), and the `_id` index, which is how a document is found.

5. **The structure tab is where it happens.** It already lists a collection's
   indexes; Add Index… and Drop Index… sit under them, the form is read into
   an index, and the call is shown in the review before Run. Dropping is
   offered only where there is something to drop.

6. **Fields are typed as a person says them**: `name, score:-1, body:text` —
   a name alone is ascending, `-1` or `desc` descending, and anything else
   after the colon is the kind of index it is. Nothing is guessed: a colon
   with nothing after it, or a field with no name, is refused with the form
   of words it wanted.

7. **The structure is read again once a change has run**, so the tab shows
   what is there rather than what was asked for.

## Consequences

- A plan is one call, and says `Atomic: false`: there is nothing to undo it,
  and the UI must not promise otherwise.
- An index cannot be altered, only made and unmade — which is what MongoDB
  offers. Changing one is a drop and a make, which a person does as two
  reviewed steps.
- The review dialog is `reviewBody`, written for a changeset's statements. An
  index plan carries one statement and no bound values, so it reads as the
  call and its description; nothing was added to the dialog for it.
