# ADR-0100: What bytes can be read as

**Status:** Accepted · **Date:** 2026-09-19
**Tasks:** T2.68 · **Requirements:** FR-13.7 · **Packages:** `internal/app/decode`, `internal/ui/cellview`, `internal/ui/shell`, `internal/store/localdb`

## Context

A record's key and value are bytes. The record view (ADR-0098) already offered
them in the forms those bytes admit — JSON, text, a hex dump — but it decided
that inline, by asking whether the bytes parsed and whether they were valid
UTF-8. Avro, Protobuf and JSON Schema arrive next (T2.69 onwards) and cannot be
decided that way: they need a registry to ask, a subject to ask about, and a
schema that may change between one record and the next.

So the question is not how to add three more forms. It is what a form is.

## Decisions

1. **A form is a decoder, seen from the side the person is on.** The forms a
   value admits are the decoders that can read it, and the names a person
   chooses between are the decoders' own names. One list, one answer to what
   these bytes can be read as, rather than a local mechanism and a registry
   mechanism that must be kept agreeing with each other.

2. **A decoder says what the bytes are; the view says how that reads.** JSON
   returns the document's own text so no number loses a digit, text returns a
   string, and raw returns the bytes unchanged — which is how hex stays a hex
   dump without the decoder knowing anything about dumps. What T2.69 adds is
   entries in the list, not a second renderer.

3. **A decoder that cannot read the bytes says so rather than guessing**, and
   only the decoders that can read a value are offered for it. An undecodable
   record is still worth seeing, so a fault is shown beside the bytes and never
   in place of them (`source.Decoder`).

4. **Empty bytes are not a JSON document.** They are the empty string, which
   somebody may well have written, and they are bytes. Nothing written at all
   is a third thing again, and the record view says so before any decoder is
   asked (ADR-0098).

5. **How a topic is read is remembered by connection, topic and field.** A key
   and a value are different bytes and get different answers; the same topic
   name on another connection is another topic.

6. **Only a choice somebody made is remembered.** Opening a topic is not
   choosing how to read it, and putting back a remembered form is not choosing
   it again. Keeping the default would record a preference nobody expressed —
   and it would outlast the default that produced it, so a topic that becomes
   decodable later would still open the old way.

7. **A remembered way of reading that no longer fits falls back rather than
   showing nothing.** Records in one topic need not agree: a value that was
   JSON yesterday may be bytes today, and the form falls back the way it does
   between one record and the next.

8. **The choice is kept as the name it goes by**, not as an index or a type.
   A name is what survives a build that offers a different set — a decoder that
   needed a registry stops applying when the registry goes, and reads back as a
   name nobody knows rather than as the wrong decoder.

## Consequences

`Forms` is now the presentation of the local decoders, so the record view, its
tests and its mutations keep their meaning while what stands behind them has
changed. T2.69's registry decoders join the same list, and the only thing that
must grow to accommodate them is which decoders apply to a given topic.

Nothing yet decodes in the grid: a topic's key and value cells still show a hex
preview whatever the chosen form. That is a separate decision about what a cell
of a log's row should say, and is left open.
