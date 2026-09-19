# ADR-0102: Reading a record by its schema

**Status:** Accepted · **Date:** 2026-09-19
**Tasks:** T2.70 · **Requirements:** FR-13.7 · **Packages:** `internal/source/drivers/kafka`, `internal/app/decode`, `internal/ui/cellview`, `internal/ui/shell`

## Context

ADR-0101 left a decoder that could say which schema wrote a record and nothing
more. Avro carries no field names or types in the payload — the schema is what
makes the bytes mean anything — so reading a record means holding both, and a
record read by the wrong schema is nonsense rather than an error.

ADR-0100 decided that the forms a value can be read in *are* the decoders that
can read it. A schema registry's decoder has to join that list rather than
stand beside it, or there would be two answers to the same question.

## Decisions

1. **A registry's decoder joins the list, first.** It knows more about these
   bytes than anything worked out from the bytes alone, so it is the reading
   somebody most likely wants; JSON, text and hex stay behind it, and a record
   whose schema cannot be read is still readable as what it is.

2. **The schema is parsed when the decoder is asked for, not per record.** A
   registry may hold a schema this build cannot read; saying so once beats
   failing on every record. Resolving the decoder is a round trip, so it
   happens once when a record view opens — asking per record would be a
   request per row drawn.

3. **A topic with no schema is the ordinary case.** So is a connection with no
   registry. Neither is a fault, and neither changes what the record view can
   already do: the local forms are what a record is read in when nothing else
   says otherwise.

4. **Only the language that has been written is read.** Avro decodes; Protobuf
   and JSON Schema still say they are not written yet. The claim must not run
   ahead of the code (REQ-DRV-1), and the refusal is a test of its own so the
   next task cannot quietly skip past it.

5. **Bytes and the schema disagreeing is said plainly.** A payload that will
   not decode says so, and the record is still shown as its bytes beside that:
   somebody looking at both can usually see which of the two is wrong.

6. **Shown as text means legible text, everywhere.** A decoded record may carry
   bytes in a field, and JSON has no way to write bytes: through `json.Marshal`
   alone they come back base64, which is the unreadable answer a header gave
   before ADR-0100. Valid UTF-8 is not enough either — `0x01` is a perfectly
   good rune that shows as nothing — so text is shown where it is printable and
   hex where it is not, in nested values and in a header's value alike.

## Consequences

Protobuf and JSON Schema (T2.71, T2.72) add entries to the same list and change
nothing else: not how a record is read, not where the choice is remembered, and
not what a value is shown as.

The rule in decision 6 closed a hole that ten mutations had walked past,
because the header fixture only ever used bytes that were invalid UTF-8. A
valid-but-unprintable byte is the case that tells the two rules apart.
