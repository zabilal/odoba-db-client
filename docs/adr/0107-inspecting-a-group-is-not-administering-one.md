# ADR-0107: Inspecting a group is not administering one

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T2.76 · **Requirements:** FR-13.10, FR-13.12, FR-13.13, REQ-DRV-1
**Packages:** `internal/source`, `internal/source/conformance`, `internal/source/drivers/kafka`

## Context

`capability.Stream` has always had three separate flags: `ConsumerGroups` for
group and lag inspection, `ResetOffsets` for moving a group's committed
offsets, and `TopicAdmin` for creating, deleting and reconfiguring topics.
They are separate because they are different promises, and two of them are
among the most destructive things this application could do.

The interface behind them was not separate. One `StreamAdmin` carried all of
it — listing groups, reading their lag, resetting offsets, creating and
deleting topics, altering configuration, adding partitions — and the
conformance suite asked for the whole of it if any one of the three flags was
raised.

So showing how far a consumer group has got, which changes nothing, could not
be claimed without also promising to reset offsets and to delete topics. For a
driver that has written the first and none of the rest, the only honest option
was to claim nothing and leave the capability unraised — which makes the
capability useless for deciding whether to offer the feature.

## Decisions

1. **The half that changes nothing becomes its own interface.**
   `GroupInspector` holds `ConsumerGroups` and `GroupOffsets`. Neither writes
   anything to the server; both exist to answer a question somebody asked.

2. **`StreamAdmin` embeds it.** A driver that implements the whole of stream
   administration satisfies both interfaces exactly as before, so this takes
   nothing away. Nothing implements `StreamAdmin` today, so the split costs
   no driver anything at the moment it is made — which is the cheapest moment
   to make it.

3. **Each claim is checked against the interface that backs it.**
   `Stream.ConsumerGroups` requires `GroupInspector`; `Stream.TopicAdmin` or
   `Stream.ResetOffsets` requires `StreamAdmin`. The suite stops asking for a
   promise the flag never made.

4. **The flags were right and the interface had not caught up.** This is not a
   new distinction; it is the one `capability.Stream` already drew, finally
   drawn in the same place twice.

## Consequences

Kafka claims `Stream.ConsumerGroups` and means it: it lists groups and reads
their lag, and it says nothing about resetting an offset, because it cannot.
When topic administration and offset resets are written they will implement
the rest of `StreamAdmin` and raise their own flags, and the check for those
is already in place and already failing anyone who raises them early.

The general rule this follows is REQ-DRV-1: a claim is a promise. Bundling
read-only work with destructive work behind one promise makes the smallest
honest claim expensive, and an expensive honest claim is one that drivers
quietly stop making.
