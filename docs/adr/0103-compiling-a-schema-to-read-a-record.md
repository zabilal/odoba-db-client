# ADR-0103: Compiling a schema to read a record

**Status:** Accepted · **Date:** 2026-09-19
**Tasks:** T2.71 · **Requirements:** FR-13.7 · **Packages:** `internal/source/drivers/kafka`

## Context

Avro arrives from a registry as a schema this build can parse and use
immediately (ADR-0102). Protobuf does not. A registry holds the `.proto`
source somebody wrote, and nothing can be read by it until that text has been
compiled into descriptors — work that `protoc` does on a developer's machine
and that has to happen here, in process, with no files on disk.

Protobuf also says something about a record that Avro does not. A file may
declare several messages, so Confluent's framing carries an index after the
five-byte header naming which one this record is.

## Decisions

1. **The schema is compiled when the decoder is asked for.** Compiling is
   work, and a topic's records all share a schema; doing it per record would
   pay that cost for every row drawn. A schema this build cannot compile says
   so then, rather than failing on every record afterwards.

2. **Standard imports are available.** A registry's schema may import
   `google/protobuf/timestamp.proto` and expect it to be there, so the
   compiler is given the same standard imports `protoc` has.

3. **The index says which message wrote the record, and a path that leads
   nowhere is refused.** On the wire a Protobuf record's fields are numbered
   rather than named, so reading it as the wrong message decodes into nonsense
   rather than failing — which makes a wrong index worse than a missing one.
   An empty index means the first message, which is what the shortcut a
   producer writes means.

4. **The index counts what the descriptor holds, not what somebody typed.** A
   map field generates a synthetic entry message, nested like any other, and
   it takes its place in the ordering. A test that assumed source order of the
   messages written by hand was wrong about a real schema, and would have been
   wrong about any schema with a map in it.

5. **Only populated fields are shown.** Protobuf gives an unset field its
   type's zero value, and a record that says nothing about a field is not the
   same as one that says zero. Showing every field would put words in the
   producer's mouth.

6. **Values stay the values they are.** Bytes stay bytes, so that what is
   shown is text where it reads as text and hex where it does not (ADR-0102);
   64-bit integers stay integers. Going through JSON would have given base64
   for the first and strings for the second, which is why the message is
   walked rather than marshalled.

7. **An enum reads as the name it was written as**, where the schema has one.
   A number says nothing to somebody reading a record.

## Consequences

JSON Schema (T2.72) is the one language left unwritten, and its refusal is
held by a live test rather than by luck: a subject in that language still says
so in its own words.

The two languages now read arrive by different routes — one parsed, one
compiled — but they join the same list of forms a value is offered in
(ADR-0100), and neither changed how a record is read, where the choice is
remembered, or what a value is shown as.
