# ADR-0104: A document with five bytes in front

**Status:** Accepted · **Date:** 2026-09-19
**Tasks:** T2.72 · **Requirements:** FR-13.7 · **Packages:** `internal/source/drivers/kafka`

## Context

Avro needed a schema parsed and Protobuf needed one compiled (ADR-0102,
ADR-0103). JSON Schema needs neither: a record written under one is a JSON
document with Confluent's five-byte header in front of it, and the schema says
what shape that document should have rather than how to read it.

So the question is not how to decode it. It is what a schema adds to a record
that can already be read without one.

## Decisions

1. **Reading it is stripping the header.** What follows the schema id is the
   document, returned as its own text so that every number keeps its digits —
   the same promise the local JSON decoder makes (ADR-0100).

2. **Those five bytes are the whole problem, and they are not nothing.** They
   are valid UTF-8, so a framed record already reads as text today: the
   document shows with five junk characters glued to its front, and never as
   JSON at all. Stripping them is what turns it back into a document.

3. **What follows must be a document.** Bytes that do not parse say so rather
   than being handed over as though they were JSON, and so does a record with
   nothing after its header.

4. **Validation is left out, deliberately.** A decoder reports through its
   error, and a decoder whose Decode returns an error is dropped from the
   forms a value is offered in — so a validating decoder that found a document
   the wrong shape would take the value off the screen with it. That
   contradicts what a decode failure is supposed to do: be shown beside the
   bytes rather than instead of them (ADR-0102).

   Saying "valid JSON, wrong shape" needs somewhere to say it that is neither
   the value nor an error, and there is no such place today. Adding one is a
   change to what a decoder is, and it should be asked for rather than arrive
   as a side effect of reading a third language.

## Consequences

All three languages a Confluent registry holds are now read, by three
different routes — parsed, compiled, and unwrapped — and all three arrive as
entries in the same list of forms (ADR-0100). Nothing about how a record is
read, where the choice is remembered, or what a value is shown as changed to
accommodate any of them.

The test that held the boundary while JSON Schema was unwritten is now the
test that it reads. Leaving it asserting a refusal that no longer happens
would have been a lie the suite told quietly.
