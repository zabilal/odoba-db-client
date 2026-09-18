# ADR-0094: What hangs under what

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.62 · **Requirements:** FR-2.2, FR-13.10, NFR-P11 · **Packages:** `internal/source/drivers/kafka`

## Context

The explorer shows topics, partitions, consumer groups and schema subjects
(FR-2.2). Topics and the cluster are already there (ADR-0091, ADR-0092). This
is where the rest of the shape is decided: what hangs under what, and what
each level costs to open.

## Decisions

1. **A partition is a leaf.** What is under a partition is records, and
   records belong in the grid rather than in a tree — a log of a million
   offsets is not something to expand. The tree ends at the partition, and
   opening it is the message browser's business (T2.63).

2. **Listing a topic's partitions costs the metadata and the offsets, and
   nothing else.** What a topic occupies on disk is a request to every broker
   holding a replica; that is asked when somebody describes a topic, not when
   they expand one. Expanding a node and asking about a node are different
   acts, and only the second is a question about size.

3. **A group node needs nothing described.** Listing groups already returns
   each group's state and protocol, so the node can say what a group is doing
   from the listing alone. Describing every group to draw a list is the shape
   ADR-0092 refused for topics, and it would be worse here: a cluster may have
   thousands of groups.

4. **Opening a cluster now costs two requests** — the metadata that names its
   topics, and a listing of its groups. Both are bounded by the size of the
   cluster rather than by what it holds, which is the line ADR-0092 actually
   drew: what it refused was a cost that grows with the number of topics.

5. **A partition says what is wrong with it, and nothing when nothing is.**
   The node carries the broker it is led from and where its log runs; it
   carries how many copies are in sync only when that is fewer than the copies
   that exist. A remark on every healthy partition is noise that hides the one
   unhealthy one.

6. **Schema subjects are not here.** They need a registry to ask, which is
   T2.69, and `KindSubject` stays unclaimed until then — a claim is a promise
   the conformance suite holds a driver to.

## Consequences

The tree now goes cluster → topics → partitions, and cluster → consumer
groups, which is what somebody connecting to Kafka came to look at.

A group exists only while something is reading, which is worth knowing when
reading the tests: they join a group and stay joined while they look. That
also creates `__consumer_offsets` — so the internal-topic filter, which T2.60
could only prove with a unit test because no test cluster had an internal
topic, is now proven against a cluster that has one.
