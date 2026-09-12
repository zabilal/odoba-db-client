# ADR-0064: A shape read from documents

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.32 · **Requirements:** FR-12.4 · **Packages:** `internal/source`, `internal/source/drivers/mongo`, `internal/app`, `internal/ui/shell`

## Context

A collection has no structure the server can be asked for: what it holds is
whatever its documents hold. FR-12.4 asks for that shape to be inferred from
a sample and presented — and `internal/model` has said since the paradigm
was designed that inference must never be mistaken for schema.

## Decisions

1. **`source.ShapeInferrer` is optional, and paired with a capability.**
   `Structure.InferredShape` says a source's structure is its data's; the
   conformance suite fails a source that claims it without implementing the
   interface, as it does for every other pair.

2. **It is asked for, never implicit.** Inference reads documents. The
   structure tab offers *Sample 200 documents*, says how many it read, and
   offers to read again; nothing samples on its own. The caller names the
   number, and the driver bounds it.

3. **The answer carries its evidence.** How many documents were read, every
   type each field was seen with and how often, and in what fraction of them
   the field was there at all. A field holding both a string and a number is
   a real and common condition, and the shape says so rather than choosing.
   The panel never renders a shape without saying it was sampled.

4. **MongoDB samples with `$sample`**, not the first N documents: on a
   collection written over time the first are the oldest and the least like
   the rest. A view is sampled the same way — its documents are a
   pipeline's, and someone browsing one needs its shape as much.

5. **Embedded documents and arrays are read into**, to a depth of four and
   two hundred fields a level. An array's members are read as the field's
   own — what matters about `items` is what an item holds, not that there
   are three of them — and what is inside is counted against the inner
   documents, so a field in every member reads as present in all of them.

6. **Fields are ordered as they are read**: `_id` first, then the fields most
   documents hold, then by name. Types are ordered by how often each was
   seen, then by name. Two runs over one sample come out the same way.

7. **BSON types map onto the model's classes**, so the grid renders and edits
   a document's values by the same rules as a table's (T2.33), while the
   native name stays MongoDB's own — `objectId`, `binData`, `decimal` — so
   what the panel says can be looked up.

## Consequences

- A shape is a sample, and a rare field may be missed; the panel says how
  many documents were read, which is the honest way to say so.
- Inference costs a read of N documents each time it is asked for. Nothing
  caches it: a collection changes, and a stale shape would be worse than
  asking again.
- Arrays of mixed types are described as arrays and no more. A uniform array
  names its element type (`array<string>`), which is what the grid needs to
  render one.
