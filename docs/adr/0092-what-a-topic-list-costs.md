# ADR-0092: What a topic list costs

**Status:** Accepted · **Date:** 2026-09-18
**Tasks:** T2.60 · **Requirements:** FR-2.2, FR-13.2, NFR-P11 · **Packages:** `internal/source/drivers/kafka`, `internal/ui/shell`

## Context

FR-13.2 asks for a topic browser listing every topic with its partition count,
replication factor, total messages and size on disk, with internal topics
filtered.

Those four numbers do not cost the same. Partition count and replication
factor come free with the metadata that lists the topics at all. A message
count needs the earliest and latest offset of every partition — two more
requests. A size on disk needs a log-directory description from *every broker*
that holds a replica, sharded across the cluster, because a topic's bytes are
wherever its replicas are.

On a cluster with a few topics that is nothing. On a real one with thousands,
drawing a list would mean thousands of partitions' offsets and a fan-out to
every broker, every time somebody opens a node.

## Decisions

1. **The list carries what listing costs.** Topics appear in the tree with
   their partition count, from the metadata that named them. Nothing else is
   fetched to draw a list.

2. **The expensive numbers belong to a topic, not to the list of topics.**
   Describing one topic reads its offsets and its size, because somebody asked
   about that topic. This is the same rule that keeps a Cassandra table
   unbadged (ADR-0084): a tree must stay cheap to open, or people stop opening
   it (NFR-P11).

3. **A count of records says "up to".** `model.Topic.MessageCount` is the sum
   of high minus low watermark, which is an upper bound on what is *retained*,
   not a count of what was ever written: a log is aged out and compacted
   behind its readers. The words in the structure tab say so, because a number
   labelled "messages" would be believed.

4. **A size is replicated bytes, and says so.** What the brokers report is
   what each replica occupies, so a topic with three replicas reports roughly
   three times its logical size. It goes in the topic's attributes with a
   label that names what it is, rather than as a bare size that reads as the
   data's own.

5. **Kafka's own topics are hidden**, as PostgreSQL's system schemas and
   Cassandra's system keyspaces are: `__consumer_offsets` and its like are the
   cluster's workings rather than somebody's data. A topic that is internal
   still says so when described, so nothing is hidden about what it is — only
   about where it appears.

## Consequences

Opening a cluster costs one metadata request, whatever the cluster holds.
Opening a topic costs three more, and only for that topic.

Against the requirement as written, the list shows two of its four numbers.
The other two are one click away, on the object they describe, which is the
only way to have them without making the tree slow for everyone — and a list
that took a second per draw would be a worse answer to FR-13.2 than this one.
