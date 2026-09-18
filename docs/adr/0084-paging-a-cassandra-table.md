# ADR-0084: Paging a Cassandra table

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.51 · **Requirements:** FR-2.5, FR-3.1, FR-3.3, FR-3.6, FR-12.3, NFR-P11, REQ-DRV-3 · **Packages:** `internal/source/drivers/cassandra`

## Context

The grid asks a source for rows the same way everywhere: `Fetch(offset,
limit)`, one window at a time, opening a browse per window. It may jump —
`Prefetch` warms the pages around wherever the viewport lands, so a scrollbar
drag schedules a page nothing before it has read — and it learns where the
data ends by getting back fewer rows than it asked for.

Cassandra cannot answer that question as asked. There is no OFFSET in CQL,
because there is no cheap way to find the thousandth row of a table whose rows
live in partitions spread over a ring: reaching it means reading the nine
hundred and ninety-nine before it. What a cluster gives instead is a paging
state — a token that resumes one particular query exactly where it stopped,
forward only.

## Decisions

1. **A browse remembers where its pages ended.** The connection keeps, per
   query, the paging states it has reached: offset → state. The page after a
   page already read resumes from its state, which is exact and costs nothing.
   Sequential scrolling — the thing people actually do — is therefore precise.

2. **A page nothing has reached is walked to, within a bound.** From the
   furthest state below it, rows are read and let go of until the page begins.
   The bound is ten of the grid's own pages; past that the browse refuses and
   says why, rather than quietly reading a table's worth of rows to answer one
   window. A refusal a person can act on ("scroll to it") beats a spinner that
   never ends (NFR-P11).

3. **The states are keyed by the query.** A paging state belongs to the query
   it came from, so changing a filter, a projection or a condition starts
   again from nothing — which is right, because the old states describe rows
   that are no longer the ones being read. What is kept is bounded, and
   dropped wholesale when it grows past that: a state is cheap to make again.

4. **LIMIT is left off a paged read.** CQL's LIMIT bounds the whole query
   rather than a page of it, and would fight the paging state. The page size
   is what the cluster is asked for; the statement the grid *shows* still
   carries the LIMIT, because that is what a person would type to read one
   page themselves.

5. **Rows are ordered only by what clusters them.** A cluster orders rows
   within a partition, by the clustering columns, and orders nothing across
   partitions. The driver knows the key — it reads it for the tree — so it
   refuses a sort on anything else itself, naming what the rows *can* be
   ordered by, rather than passing it to the cluster to reject less kindly.

6. **A row is addressed by its primary key**: the partition key, then the
   clustering columns, which is what addresses one in CQL (FR-4.7). Nothing
   edits rows yet, but a browse that knows how a row is addressed is what
   editing will be built on.

7. **Nothing is counted.** Counting a table is a read of every partition on
   every node, so neither `ExactCount` nor `ApproximateCount` is claimed and
   the grid pages without a proportional scrollbar (FR-2.5) — which it is
   built to do.

## Consequences

A Cassandra table opens in the grid, and its nodes say so. Filtering and
sorting are claimed as the cluster's, with what CQL cannot mean refused by the
dialect (ADR-0082) and what a cluster cannot order refused here.

The bounded walk is the one place this driver spends a person's patience on
their behalf, and it is spent forward only: reading backwards is free, because
the state for an earlier page is already known.

A jump into the far end of a large table is refused. That is a real difference
from the SQL drivers, and it is the honest one: the alternative is a query
that reads gigabytes to draw a screen.
