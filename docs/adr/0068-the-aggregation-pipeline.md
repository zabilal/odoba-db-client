# ADR-0068: The aggregation pipeline

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.35 · **Requirements:** FR-12.1, FR-4.9 · **Packages:** `internal/source`, `internal/source/drivers/mongo`, `internal/app`, `internal/ui/shell`

## Context

A collection is read by a pipeline as often as by a filter: grouping,
joining, projecting and counting are stages, not a WHERE clause. FR-12.1
asks for an editor for them. The grid reads through a fetcher (`BrowseSource`),
so what a pipeline needs is a fetcher of its own.

## Decisions

1. **`source.Aggregator` is optional, and paired with `Data.Pipeline`.** The
   conformance suite fails a source claiming the capability without the
   interface, as it does for every other pair. A pipeline is the person's own
   text in the source's own form; nothing in the UI parses it.

2. **A pipeline is not a filter and not a sort.** It says its own matching and
   its own order, so a browse's filters, sorts and WHERE are refused rather
   than quietly added to it: a stage after a `$group` sees what the group
   made, and a sort the grid added would silently be one too.

3. **Only paging is added, and only where a pipeline reads.** `$skip` and
   `$limit` are appended so the grid can page and memory stays bounded
   (NFR-P11). A pipeline that writes — MongoDB's `$out` and `$merge` — must
   end in the stage that writes, so nothing is added after it, and it
   produces no documents to page.

4. **A pipeline that writes is a write.** The guard is asked before it runs:
   nothing runs on a read-only connection, and a production connection is
   asked first, with the same words as a commit (FR-4.9, NFR-S4). A field
   called `$out` inside a stage is a field, not a stage.

5. **The columns are the fields the documents produced hold**, in the order
   the first document to hold each gave it, `_id` first where a stage kept
   one. A pipeline's output has no declared shape at all, so nothing is
   claimed about a column's type.

6. **Nothing a pipeline produces is written back.** Its documents are
   computed and the collection may not hold them in that shape, so the stream
   has no identity (FR-4.7).

7. **The editor sits above the grid, where the WHERE bar sits for a table**,
   and what it produces goes in the grid's own place. Show Documents puts the
   collection back. The editor is the tab's, so what was typed survives
   hiding it.

## Consequences

- Each page re-runs the pipeline with a different `$skip`. A pipeline is not
  a cursor, and a stable page needs a stable sort — `$sort` is a stage a
  person can add, and without one the server's order is its own.
- One run holds at most five thousand documents in memory, because the
  columns are the fields the documents hold and they must be known before the
  first row is drawn.
- The grid's filters and sorts are still drawn while a pipeline's documents
  are shown, and act on the browse they came from. Hiding them is a change to
  the grid this does not make yet.
