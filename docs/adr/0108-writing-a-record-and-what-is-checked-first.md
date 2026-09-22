# ADR-0108: Writing a record, and what is checked first

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T2.77 · **Requirements:** FR-13.11, FR-13.21, FR-4.9, FR-1.8
**Packages:** `internal/source/drivers/kafka`, `internal/app`, `internal/ui/shell`

## Context

Everything this driver had done until now was read something. Producing is the
first thing it does that changes anything, and three questions had to be
answered before a record could be written at all: what "schema validation"
means, where a record goes when somebody names a partition, and what the
guardrails do.

`ProduceRequest` had already settled some of it. A subject names a schema to
check against, an empty subject skips the check, and `Confirmed` carries
consent. FR-13.21 settles the rest: producing inherits read-only mode and the
production guardrail without exception.

## Decisions

1. **Validation checks; it does not encode.** A subject names a schema the
   record must be readable by, and the check hands the bytes to the same
   decoder a reader would use. Encoding a typed value into framed Avro,
   Protobuf or JSON Schema is the work of ADR-0102 and ADR-0103 in reverse —
   three languages, each able to be wrong in its own way — and it is not what
   the field asks for.

   What is checked is what will be written. A record consumers cannot decode
   is a production incident rather than a warning, and the moment before it is
   written is the last one at which it can be prevented.

   The cost is real and is not pretended away: writing a schema'd record this
   way means supplying bytes that are already framed. Encoding from a typed
   value is a task of its own, and this one does not pretend to be it.

2. **Naming no subject writes what it was given.** Most topics have no schema,
   and a connection that names a registry must still write to them. The check
   is something somebody asks for, not something a registry imposes on every
   topic near it.

3. **A partition is honoured or refused, never quietly moved.** kgo chooses a
   partitioner once per client rather than once per record, so one partitioner
   does both jobs: a record addressed to a partition goes there, and one
   addressed to none is hashed by key exactly as the default does.

   "None" is -1 rather than the zero value, because 0 is a partition: a record
   that meant nothing by its partition and one that meant the first must not
   look alike.

   A partition the topic has not got is refused before anything is produced,
   by checking how many it has. The partitioner cannot refuse it — it answers
   with an index and has nowhere to put an error — so out of range would
   quietly become some other partition, and a record in the wrong log is worse
   than a record that was never written.

4. **The guard refuses before anything is dialled.** Read-only refuses
   outright, and consent cannot buy past it: that is a decision already made
   about the connection, not a question about this record. A production
   connection asks, and the consent belongs to that record and no other.

   Because the refusal happens before any broker is contacted, the question
   the interface asks — "nothing has been sent yet" — is literally true rather
   than merely reassuring.

5. **The explorer offers it only where it applies.** A topic's menu offers to
   write a record; a table's does not carry the item at all. An item greyed
   out on every table in the tree would be an advertisement for something a
   table cannot do.

## Consequences

Kafka claims `Stream.Produce` and means it, and the shared suite holds the
claim to `source.StreamProducer`. The claim says nothing about resetting
offsets or administering topics, which are separate claims behind a separate
interface (ADR-0107).

What is missing, and named here so that it is missing on purpose: there is no
way to type a value and have it encoded under a schema. Somebody producing to
a topic with a registry must supply framed bytes, and the view will tell them
plainly when what they supplied cannot be read back. That is a smaller thing
than the encoder, and an honest one.
