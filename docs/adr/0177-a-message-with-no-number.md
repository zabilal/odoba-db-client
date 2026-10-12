# ADR-0177: A message with no number

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.16 · **Requirements:** §4.3, FR-13.1–13.6, FR-13.11, FR-13.19–13.21, REQ-DRV-3, NFR-S3, NFR-P8
**Packages:** `internal/source/drivers/pulsar`, `cmd/ikigai`, `cmd/ikigai-cli`, `.github/workflows`

## Context

T5.16's third source, and the last of it. Pulsar is a log like Kafka's and is
read like one, so it is drawn the way NATS was (ADR-0176): a topic where a topic
is, a message with a record's columns, and the stream window unchanged. This
record is about where that stops being true.

## Decisions

1. **A connection is two connections.** What a cluster holds — tenants,
   namespaces, topics, who is subscribed — is asked of its admin service, over
   HTTP on a port of its own. Messages are read and written over the broker
   protocol. There is no doing with one what the other is for, so the form asks
   for both ports. `Open` asks the admin service what the cluster is and makes
   the broker client without dialling: which broker serves a topic is the
   topic's to say. So a wrong broker port is found when something is read, and
   is said then; the live suite holds that it is said in time.

2. **A message's offset is its address, as text.** Kafka numbers the records of
   a partition. Pulsar addresses a message by the ledger it was written to and
   its entry there, and a topic moves from ledger to ledger as it goes: there is
   no single number that says how far along a message is. The offset column
   holds `ledger:entry`, as text. Nothing is invented to make it a number.

3. **What needs a number to count with is refused.** Two of the ways of saying
   where a read begins — "an offset" and "the last few" — count. Neither can be
   answered here without pretending: an offset would have to be an entry in a
   ledger nobody named, and counting back from the end crosses ledgers of
   lengths this would have to ask about and batches it would have to open. Both
   are refused, saying why and what to do instead: read from a time. The
   beginning, a time, and following from the end all work.

4. **Looking at a cluster does not add to it.** A cluster as it is installed
   makes a topic the first time one is named. So reading a topic that is
   misspelt would create it, and writing to one would put a message where
   nobody is listening. Before a reader or a producer is made the admin service
   is asked whether the topic is there, and a topic that is not is said not to
   be. The live suite reads, writes to and describes a topic that does not
   exist, and then lists the namespace to see that it still does not.

5. **A read is through a reader.** It is Pulsar's own way of looking at a topic
   without taking part: it acknowledges nothing and no subscription's place
   moves. While it is open it is a subscription of a kind, named as this
   application's, and left out of the subscriptions shown. A producer is made
   for one message and closed after it, so that nothing is left listed as
   publishing for as long as the window is open.

   A reader has no end fixed in advance, unlike the reads of Kafka and NATS: a
   bounded read stops at its bound or when the reader says nothing more is
   there, and what is written meanwhile is read too. That is said in
   `docs/KNOWN-DEFECTS.md` rather than worked round.

6. **No value and an empty value are two things.** A message written with no
   value is what a compacted topic reads as a key removed; it is written as
   that and read back as nothing. One written with an empty value is read back
   as empty. The first test of this expected them to be the same.

7. **A namespace is one level, and a topic in pieces is shown once.** The tree
   is cluster, `tenant/namespace`, topics. A tenant holds nothing but
   namespaces, and a level with nothing in it but another level is a click
   nobody needed. A topic cut into partitions is shown as itself with how many;
   its partitions are topics of their own underneath and are not listed beside
   it. Reading it reads all of them, each message saying which it was in.

8. **Subscriptions are said where a topic is described.** Who is subscribed and
   how many messages each has still to acknowledge is one line of a topic's
   structure. They are not objects in the tree: listing a namespace's
   subscriptions means asking every topic in it, and a class that costs a
   request a topic to open is not one to add in passing. No consumer-group
   capability is claimed.

## Cost

The client library is large. The stripped binary was 88.9 MB before T5.16's two
new drivers and is 96.9 MB with both, against a ceiling of 120 MB (NFR-P8); the
two were not measured apart, and most of it is this one, whose module brings a
hundred others.

## What is not proven

- A cluster that checks a token, a password or a certificate; a cluster of more
  than one broker; geo-replication.
- Non-persistent topics, which have not been tried.
- Messages written in batches by somebody else's producer: the suite writes one
  to an entry.

## Consequences

- T5.16 is done: Redpanda (ADR-0175), NATS JetStream (ADR-0176) and Pulsar.
- `gate.sh` and the nightly run require the Pulsar server.
- Subscriptions as objects, moving one's place, administering topics, a rate,
  and reading from the last few messages are each unclaimed.
