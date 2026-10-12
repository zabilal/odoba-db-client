# ADR-0176: A stream is a log

**Status:** Accepted · **Date:** 2026-10-12
**Tasks:** T5.16 · **Requirements:** §4.3, FR-13.1–13.6, FR-13.10, FR-13.11, FR-13.17, FR-13.19–13.21, REQ-DRV-3, NFR-S3
**Packages:** `internal/source/drivers/nats`, `cmd/ikigai`, `cmd/ikigai-cli`, `.github/workflows`

## Context

T5.16's second source. NATS itself keeps nothing: a message is handed to
whoever is listening and is gone. JetStream is the part that keeps them, in
streams, with consumers that each remember how far they have got. The driver is
for JetStream, and a server without it is refused at the door with that reason.

## Decisions

1. **A stream is shown where a topic is, with a topic's columns.** A stream is
   one ordered log of messages read from a position, which is the shape this
   application already draws for Kafka. So a stream is a `KindTopic`, a
   consumer a `KindConsumerGroup`, and a message's row has the columns a Kafka
   record's has: partition, offset, timestamp, key, value, headers. The whole
   stream window — following, seeking, the record view, filtering what has been
   read, export, the rate — reads NATS with no change to it.

   Where the two differ the difference is kept and said, not hidden:

   - *partition* is always 0. A stream is one log. The tree shows nothing
     under a stream for the same reason: a level holding one partition would be
     a level invented to match somebody else's shape.
   - *key* is the subject the message was published on, as text. A message has
     no key; what tells messages apart and decides where they go is the
     subject, which is what a key is for.
   - *offset* is the stream sequence, which begins at 1 and may have gaps where
     messages were removed.

   The alternative was columns of its own — sequence, subject — and a second
   stream window to read them. The mapping costs a zero in a column; the
   alternative costs every stream feature again.

2. **Looking at a stream perturbs nobody.** JetStream hands over messages in
   order only through a consumer, so a read makes one — of the kind that changes
   nothing. It is ephemeral, acknowledges nothing, moves no other consumer's
   place, and is removed when the read ends; the server removes it by itself
   after thirty seconds if the read could not. It is marked as this
   application's own and left out of the consumers shown: somebody looking at
   who is reading a stream did not ask to see themselves looking. The live
   suite holds all of it — the count of consumers at the server before, during
   and after a read, and a real consumer's place unchanged (FR-13.19).

3. **A read ends where the stream ended when it began.** The last sequence is
   asked first and the read stops there, or when the reader says nothing more
   is pending — which is what ends a read from a time, which has no sequence to
   compare, and a read of a stream whose last messages were removed, which has
   an end nothing will arrive at. What is written after a read began belongs to
   the next one. An empty or purged stream is read as nothing without a
   consumer being made at all.

4. **A consumer's place is where everything before it is dealt with.** Kafka's
   group commits an offset and everything before it is done. A consumer here is
   told message by message, so there are two places: how far it has been sent,
   and how far everything has been acknowledged. What is shown as where it is,
   is the second, and what is shown as how far behind it is, is everything not
   yet acknowledged, sent or not — the server's own count rather than the
   distance to the end, because a consumer of some of a stream's subjects was
   never going to read most of what lies between. One that has never been sent
   anything has no place, which is not the same as being at the beginning.

5. **A message is written on a subject the stream keeps, or not at all.** A
   message is published to a subject, and the stream that keeps it is whichever
   listens there. The subject is what is given as the record's key; a stream
   with exactly one subject that is not a pattern needs none given; anything
   else has to be said, because guessing a subject is guessing who receives the
   message. A subject the stream does not keep is refused before anything is
   sent, so a message cannot be written into one stream by somebody looking at
   another, or published to nobody.

   The server can be asked to hold a message to a stream, and the first version
   asked it. It is asked with a header, and the header is kept with the message:
   every message this wrote carried `Nats-Expected-Stream`, which nobody had
   written. The live suite found it by reading back what it had written. Two
   streams cannot keep the same subject, so matching the subject here against
   what the stream keeps decides the same thing and leaves the message as it
   was.

6. **One attempt to connect, encrypted unless told otherwise.** Verification is
   on unless a connection says otherwise (NFR-S3). The client is told not to
   retry a first connection: a test connection has to fail when it fails. A
   secret given with no user name is a token, which is NATS's other way of
   saying who is calling.

## What is not proven

- A server that checks a user, a token or a certificate.
- A cluster of more than one server, and a stream with replicas.
- Key-value and object stores, which are streams underneath and are listed as
  those streams.

## Consequences

- `gate.sh` and the nightly run require the NATS server.
- T5.16 stays open for Pulsar.
- Making and removing streams, moving a consumer's place, and reading a bucket
  as keys and values are each unclaimed.
