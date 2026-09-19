# ADR-0098: What a record says of itself

**Status:** Accepted · **Date:** 2026-09-19
**Tasks:** T2.66 · **Requirements:** FR-13.8 · **Packages:** `internal/ui/cellview`, `internal/ui/shell`

## Context

A record reaches the grid as a row: a partition, an offset, a time, a key, a
value and its headers. That is enough to scan a topic and nowhere near enough
to read one record, because the two fields worth reading are bytes, and bytes
are not one thing. The same bytes are text to the person who wrote them, JSON
to the service that parses them, and a dump to whoever has to know exactly
what went over the wire.

## Decisions

1. **A value is offered in every form it admits, and no others.** Bytes that
   do not parse are not offered as JSON; bytes that are not valid UTF-8 are
   not offered as text. A form that cannot be read is worse than a form that
   is not there — text made of replacement characters is a lie about what was
   written, and a person who sees it cannot tell whether the record or the
   reader is at fault.

2. **Hex is always there**, because bytes are always bytes. It is the form
   that never lies and never fails, so it is the one everything falls back to.

3. **The most decoded form comes first.** Somebody opening a record wants to
   see what it says; they descend towards the bytes only when the meaning is
   in doubt.

4. **The chosen form is kept by name while it still applies.** Reading down a
   topic in hex should not jump back to text at every record. Where the next
   record's bytes do not admit that form, the choice falls back rather than
   showing nothing.

5. **Headers are a table, in the order they were written, with repeats.**
   Kafka lets a header name repeat and the driver keeps every one; a table
   built from a map would throw away the difference between a header sent once
   and a header sent twice. The values are bytes and are shown as text where
   they are text, which is nearly always, and as hex where they are not.
   Before this they were rendered as JSON, which showed them base64-encoded:
   a trace-id read `YWJjMTIz`, which is not something anybody can act on.

6. **Nothing written and nothing there are different.** A record with no key
   says so; it is not shown as empty bytes, because a producer sending no key
   and a producer sending an empty one mean different things.

7. **Copy copies the form on screen.** Somebody reading a record as hex who
   presses Copy means the hex. Copying the whole of it rather than the part
   shown is the same promise the cell viewer already makes.

8. **The record view is for records.** It is offered where the object is a
   topic and nowhere else; every other source's rows are rows, and the form
   view already reads one of those down.

## Consequences

Decoding beyond what the bytes say of themselves — Avro, Protobuf, a schema
fetched from a registry (FR-13.7, T2.68) — lands behind this same chooser as
further forms, without changing what any of the above promises.

Which forms a value admits, and what a headers table holds, are decided in
`cellview` where they can be tested without a window; the shell draws what it
is given, as it already does for the cell viewer.
