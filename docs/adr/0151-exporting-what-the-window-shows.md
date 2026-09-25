# ADR-0151: Exporting what the window shows

**Status:** Accepted · **Date:** 2026-09-24
**Tasks:** T4.16 · **Requirements:** FR-10.8, FR-13.16, FR-13.7
**Packages:** `internal/app`, `internal/ui/shell`

## Context

FR-13.16 asks for a consumed window of a topic to be exported to NDJSON or
CSV. The export already worked on a topic's tab — a topic's records reach the
grid as rows like anything else — and what came out was hex in CSV and base64
in NDJSON, because a record's key and value are bytes.

Nobody wants that. A record is read in the window through a decoder: as text,
as JSON, or through a schema from the registry (ADR-0098, T2.70). The bytes
are what arrived; the decoded form is what it says.

## Decisions

1. **An export of a window writes what the window shows.** A topic's rows are
   wrapped so that the key and value arrive decoded, and the export writes
   them: JSON as JSON, text as text, a schema's record as its fields. The
   decoded column says it holds text and is named after the decoder, so a file
   says how it was read.

2. **The decoder is the one the window is reading with**: the choice somebody
   made for that topic's field, which is already remembered per connection,
   field and topic; or, where they made none, the most decoded form the bytes
   admit, with the registry's decoder tried first. The same rule the record
   view uses to pick the form it opens on, applied to the file.

3. **Bytes nothing will read are written as bytes.** An undecodable record is
   still worth exporting, and leaving it out would be a file quietly short of
   the window it is of. This is also why a decoder that refuses is passed over
   rather than taken for the answer: a schema says what most of a topic is,
   not what every record in it is.

4. **The wrapper copies each row.** The row belongs to whoever made it, and a
   stream that decoded its caller's rows in place would leave them decoded for
   the next reader — a record that cannot be read twice, once in the window
   and once into a file. This was a real bug, found by a second test reading
   the same fixture.

5. **It applies wherever a topic is exported**: from its tab, and from a batch
   of marked objects. Where it was exported from is not what it holds.

## Consequences

- The export of a table is untouched: nothing without a `key` or `value`
  column of bytes is changed, so the wrapper is safe over any rows at all.
- A row's decoded value can be larger than the bytes it came from — an Avro
  record is a few bytes on the wire and a paragraph of JSON — so a file is not
  the size of the window it is of. Nothing here holds more than a row at a
  time either way.
- FR-10.8's other half, replaying a file into a topic, is not met. It needs a
  producer on the load path: the import writes through a source's bulk loader,
  and the Kafka driver has none. No task claims it yet.
