# ADR-0020: Badges are read as their rows are drawn

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T1.45 · **Requirements:** FR-2.5, NFR-P2 · **Packages:** `internal/ui/explorer`, `internal/ui/explorer/view`

## Context

FR-2.5 asks for row-count and size badges in the explorer, fetched lazily
and cancellably, never blocking expansion. Each driver already answers
`Introspector.Badge`, and each answer costs a query: PostgreSQL reads a
table's row estimate from the catalogue and a database's size with
`pg_database_size`, which can be slow on a large one; MySQL reads a
table's estimate from `information_schema`; SQLite has none, since it keeps
no estimates and an exact count is a full scan. Until now nothing asked.

## Decisions

1. **A badge is read the first time its row is drawn**, off the UI
   goroutine, and the row is drawn again when it lands. Expanding a branch
   never waits for a badge. A node that came with a badge of its own, from
   its driver's listing, is drawn as it is and not read again; folders and
   columns have none to read.

2. **At most four are read at once**, each for at most ten seconds, so a
   schema of a thousand tables does not send a thousand queries. The rest
   wait their turn.

3. **Every answer is remembered**, "none" included, and a timeout or an
   error is remembered as none: a slow or failing server is not asked again
   on every redraw, and a source with no badges, such as SQLite, is asked
   once a node.

4. **Reads are cancelled when their rows leave view.** Closing a branch
   cancels the reads below it, which are then forgotten, to be read again if
   their rows are drawn again. Refreshing a node cancels and forgets the
   badges below it, so they are read afresh. Fyne's tree does not say when
   a row scrolls out of sight, so a read for a row scrolled away runs on
   until it lands or times out; there are never more than four.

5. **Reading a badge calls the driver directly**, so a panic in it comes back
   as an error (ADR-0017) and is remembered as no badge.

## Verification

The explorer view is tested to read a badge when its row is drawn and not
again, to leave a node's own badge alone, to ask a node with no badge once,
to cancel a read when its branch closes, to read at most four at once, and
to draw nothing for a driver that panics.

## Not decided here

Cancelling the read for a row that has scrolled out of sight, which needs
the tree to say when that happens. An exact count on request, for sources
that only estimate.
