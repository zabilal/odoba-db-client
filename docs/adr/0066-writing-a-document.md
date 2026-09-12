# ADR-0066: Writing a document

**Status:** Accepted · **Date:** 2026-09-12
**Tasks:** T2.34 · **Requirements:** FR-4.4, FR-4.5, FR-12.1 · **Packages:** `internal/source`, `internal/source/drivers/mongo`, `internal/model`

## Context

The grid holds pending changes and commits them through `source.Writer`
(ADR-0031): a plan of statements a person reviews, then applied. A document
store's writes are not statements — they are calls — and a document's fields
are its own, so a change can take one away.

## Decisions

1. **A statement carries the call it renders.** `source.Statement` gains
   `Op any`: the driver's own form of the operation, written by its Plan and
   read by its Apply. SQL is text a driver can execute; `updateOne` is not.
   Nothing outside the driver reads it and nothing shows it — the SQL field
   holds what a person reviews, which for MongoDB is the mongosh call,
   rendered in extended JSON so that it says exactly what will be sent.

2. **`model.Removed` says a field is to be there no longer.** A document's
   fields are its own, and a field that is absent is not a field that is
   null. It reaches MongoDB as `$unset`, and a new document simply does not
   get it. A relational store refuses it and says why: a column belongs to
   the table, so no row can be without one.

3. **A document is told from another by its `_id`, and nothing else.** Any
   other field may name several documents, and `updateOne` would change one
   of them and report that it changed one — which would read as a change made
   precisely (ADR-0034). A changeset keyed by anything else is refused before
   anything is written. New documents need no key, as none addresses them yet.

4. **An `_id` that reads as an ObjectID is one.** The grid holds what a
   browse gave it — an ObjectID written out as its hex — so a 24-character
   hex string becomes an ObjectID again on the way back, and anything else is
   itself: a collection may key its documents however it likes.

5. **Nothing is undone.** A standalone MongoDB has no transaction, so the
   plan says `Atomic: false`, the UI warns before committing, and a failure
   leaves the writes before it with the outcome saying how many were made.
   The rules for what a write's count means are the contract's, not SQL's, so
   the shared `sqlscript.ApplyWith` applies them: nothing matched is a
   document changed or deleted since it was read.

6. **The conformance suite checks a plan against what its source claims.** It
   used to insist every plan be atomic; now it insists a plan says what
   `Data.TransactionalWrite` says, which is the rule the contract states.

## Consequences

- A collection on a replica set could write in a transaction, and does not.
  The capability is per connection, so that is a later change: what is
  claimed here is what a standalone server can keep.
- An `_id` that is a string of 24 hex characters is read as an ObjectID, and
  a change to such a document matches nothing and is refused as gone. It is a
  no-op, not a wrong write, and the person is told.
- The shared write checks in the conformance suite still assume a declared
  schema with server defaults, so MongoDB does not run them. A document-shaped
  write check is what T2.38 still wants.
