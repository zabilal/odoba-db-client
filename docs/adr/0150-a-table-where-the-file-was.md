# ADR-0150: A table where the file was

**Status:** Accepted · **Date:** 2026-09-24
**Tasks:** T4.15 · **Requirements:** FR-10.9, FR-10.5, FR-10.6, FR-4.9
**Packages:** `internal/app`, `internal/ui/shell`

## Context

FR-10.9 asks for table-to-table copy, including across different sources:
production into a local copy, one engine's table into another's.

The import already reads a file's rows, makes each one the destination
table's values column by column, and hands them to that source's bulk loader
as it asks for them (ADR-0046, ADR-0049, ADR-0051, ADR-0054). It streams, it
batches, it knows what to do with a row that will not go in, and all of it is
proved.

## Decisions

1. **A copy is the import with a table where the file was.** A file's rows
   reach the destination as a `model.RowStream`; a table's rows are a
   `model.RowStream`. So `app.CopyRows` opens both ends, pairs the columns and
   calls `transfer.Load` — and everything about types, batches, keys, replacing
   and refusing is the import's, already written.

   This is why there is no `internal/copy`: a copy that had its own loader
   would be a second answer to every question the import has already answered,
   and the two would drift.

2. **Columns are paired by name**, telling no difference between cases,
   spaces, underscores and hyphens — `transfer.Suggest`, the same rule the
   import's mapping starts from. What the destination has nowhere to put is
   **said before anything is copied**, which is the one thing a copy can do
   that an import cannot: both ends are known in advance, so the form shows
   how many columns matched and names what is left behind.

3. **A copy with no column in common is refused**, not run. Writing nothing
   and calling it a copy is the failure nobody notices.

4. **The destination is chosen from a list of the connection's tables**, walked
   from the tree with the same walk the structure search uses (ADR-0148),
   stopping at two thousand. A list longer than that is not one anybody picks
   from, and whatever asks says it stopped short: a destination missing from a
   list reads as a destination that cannot be written to.

5. **Production and replacing ask first**, in the words the import uses, with
   the driver's own guard saying whether a write must be confirmed (FR-4.9).
   Copying into a production connection is the case this feature exists for,
   so it is the case that must ask.

## Consequences

- A copy is as good as the destination's bulk loader. A source with none says
  so before the form is filled in, and is offered as a destination with
  nothing to copy into.
- Nothing is offered for making the destination table. A copy goes into a
  table that is already there; scripting the source as CREATE and running it
  is how the table gets made, and both are one menu apart.
- The copy reads the source through a plain browse, so it takes the table as
  it is: no filter, no sort, no subset. Copying some of a table is a query and
  an export away, and a copy of a filtered view of a table would need the
  form to carry the whole of the browse's state.
- Two connections on the same engine are no different here from two engines.
  Nothing in the copy knows what either end is, which is what makes "across
  different sources" true without a line of code about it.
