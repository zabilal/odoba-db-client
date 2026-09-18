# ADR-0093: What a partition says

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.61 · **Requirements:** FR-13.3 · **Packages:** `internal/ui/shell`

## Context

FR-13.3 asks for a topic's detail: per partition, which broker leads it, which
brokers hold replicas, which of those are in sync, the earliest and latest
offset — and lag.

The driver already reads four of those five when it describes a topic
(ADR-0092). The fifth is a different kind of thing.

## Decisions

1. **The partition table shows what a partition is**: its number, the broker
   it is led from, the brokers that hold copies, the copies that are in sync,
   and where its log begins and ends.

2. **A partition with no leader says "none".** It cannot be read or written
   at all until one is elected, and that is the fact worth putting in front of
   somebody — a bare -1 in a column is a number they have to know how to read.

3. **Offsets nobody could read say "unknown", and a log whose ends meet says
   it is empty.** Not knowing and holding nothing are different things, and
   only one of them is a fact about the data. This is the rule T2.60 already
   holds in the driver, said again where it is read.

4. **Copies that are behind are counted in words.** Partitions whose in-sync
   set is smaller than their replica set are named above the table: the
   replicas exist, but some would not be there if the leader failed now.
   Somebody opening this view is usually looking for exactly that, and
   counting it is cheaper than reading two columns of ids.

5. **Lag is not here, and is not missing either.** It belongs to a consumer
   group, not to a partition: a partition has no lag of its own, only a lag
   somebody reading it is behind by. franz-go's lag is keyed by group and
   begins by describing groups; the model puts `Lag` on `GroupOffset` and says
   in as many words that group offsets are read on demand, because a cluster
   may have thousands of groups. Showing a topic's lag would mean enumerating
   every group and describing each, on a view that opens when somebody clicks
   a topic — the cost ADR-0092 refused for the topic list, arriving by another
   door. It lands with consumer groups (T2.76), where a group's lag is the
   subject rather than a figure borrowed from one.

## Consequences

Four of FR-13.3's five are answered where it asks for them, and the fifth is
placed rather than dropped: the requirement is met by two tasks, and the one
that owns lag is the one that has the groups to compute it from.

Nothing was added to the driver for this. Everything shown was already read
when a topic is described, which is why a view that says considerably more
costs exactly what it did before.
