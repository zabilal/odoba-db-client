# ADR-0071: A conformance suite with two paradigms

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.38 · **Requirements:** FR-4.4, FR-4.5, FR-12.1, NFR-S4 · **Packages:** `internal/source/conformance`, `internal/source/drivers/mongo`

## Context

The shared suite is what a driver has to answer to (REQ-DRV-1): every
capability it claims is checked against a live server, so a claim is a
promise. Its write checks were written against a table — a `writes` object
with `id`, an integer primary key the server numbers; `name`, text that
cannot be NULL and is `'none'` by default; and `n`, an integer that can be
NULL — and they check the defaults the server fills in, the key it gives, and
the rollback that undoes a plan that failed.

A collection has none of that. It has no declared columns, so there is
nothing to default; no numbered key, only the `_id` MongoDB gives; and a
standalone server has no transaction to undo. The MongoDB driver therefore
ran the suite with every write check skipped, which left the strongest part
of the contract — FR-4.4, FR-4.5 and NFR-S4, the guard among them —
unchecked for the one driver that writes differently from all the others.

The choice was between bending a collection into the table the checks expect
(a seeded `id` field, a `name` the driver would have to default itself) and
giving the suite a second set of write checks. The first would test a fiction:
the driver would be answering for a shape no MongoDB user has.

## Decisions

1. **`checkWriter` dispatches on `Capabilities().Paradigm`.** A relational
   source runs the checks that were there; a document source runs
   `checkDocumentWriter`. Every other check in the suite is shared as before —
   a paradigm is a difference in what a row *is*, not in what a driver owes.

2. **The document checks assume only the contract.** No columns, no defaults,
   no key of the suite's choosing: documents are added, read back by a field
   of their own, and told apart by the identity their own stream reports
   (`model.IdentityOf`). What is checked is what FR-12.1 promises — a change
   writes the fields it names and leaves the rest alone, a field can be taken
   away (`model.Removed`), a change to a document that is gone fails and says
   which change it was, new documents need no key, and a plan's atomicity is
   what the source claims (`Data.TransactionalWrite`), not what SQL has.

3. **The guard is checked in both paradigms, in the same words.** A read-only
   connection refuses with `ErrReadOnly`, a production plan is marked
   `Guarded` and refuses with `ErrConfirmationRequired` until consent is
   carried into the plan. NFR-S4 is about the data layer, and the data layer
   is the same layer for both.

4. **`Target.Writable` means an empty object of the source's own kind.** For a
   store with declared columns it is still the `writes` table described
   above; for a document store it is an empty collection, which the checks
   fill themselves. A document has no shape until one is written, so there is
   nothing to seed.

5. **The identity of a writable object is asked, not assumed.** The read-only
   guard check used to key its changeset by `id`, which a collection would
   refuse before the guard was ever reached. `writableIdentity` gives the
   `id` key for a relational source and, for a document source, whatever a
   browse of the object says its documents are told apart by.

## Consequences

The MongoDB driver now runs the suite's write checks rather than skipping
them, so what T2.34 built is held to the shared contract and not only to its
own tests. A driver in a third paradigm — a key-value store (2.F) — will need
a third branch or a check of its own; that is the point of dispatching on the
paradigm rather than on the driver.

Two checks still skip for MongoDB, and honestly: `Distinct`, which needs
`Data.DistinctValues`, and `EditableResults`, which the console does not
claim. A skip for a capability not claimed is the suite working as intended;
a skip for a capability claimed is a failure, and there are none.
