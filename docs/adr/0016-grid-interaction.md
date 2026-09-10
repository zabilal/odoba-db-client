# ADR-0016: The grid's interaction model

**Status:** Accepted · **Date:** 2026-09-10
**Tasks:** T1.49 (and T1.48–T1.56 as they land) · **Packages:** `internal/ui/grid`, `internal/app` (`browse.go`), `internal/ui/shell`

## Context

The grid shows a window of a result that may be far larger than memory
(ADR-0002, ADR-0011). Every interaction that reorders or narrows the rows has
to take that into account: a sort or filter over the loaded rows would be a
sort or filter over a fragment.

## Decisions

**The server sorts.** Clicking a column header asks for the object to be
browsed again in that order (`BrowseSource.With`), and the grid's model
switches to the new source. Sorting only what is loaded would sort a
fragment and pass it off as the table (FR-3.3).

**Header clicks follow the familiar pattern.** A column alone cycles through
ascending, descending and unsorted, replacing any other sort. ⇧-click adds
it as the next key, or cycles it within the sort. Headers show ↑ or ↓,
numbered when several columns sort. Fyne's tap event carries no modifier
keys, so a header cell notes Shift on mouse-down.

**Only what can be re-sorted is sortable.** A browse re-sorts on the server
if the source says it can (`capability.Data.ServerSort`). A query's result
cannot be re-sorted without running the query again, so its headers do
nothing. The grid only emits sort keys; the shell turns them into
`BrowseOptions`, so no SQL is built in the UI (ARCH-2).

**A result from a replaced source is dropped, not shown.** The model's
fetcher is swapped behind an atomic pointer, and every swap or invalidation
bumps a generation. A page or count begun in an older generation is
discarded when it lands. Without that, a page requested under the old order,
still in flight when the sort changed, would land in the new grid and show
rows in the wrong places. Reload Rows had the same latent flaw. A test holds
a fetch open across the switch; with the guard removed, it fails.

**The latest request wins.** Two quick clicks send two browses. The tab
numbers its sort requests and applies only the latest, and a sort that
fails puts the header back as it was.

## Not decided here

The filter row and per-column filters (T1.54–T1.56), selection and copy
(T1.51–T1.52), the cell viewer (T1.53), and column resize, reorder and hide
(T1.48). Each will be added here as it lands.
