# ADR-0157: A table that is a collection

**Status:** Accepted · **Date:** 2026-09-25
**Tasks:** T4.6 · **Requirements:** REQ-DB-1, REQ-DB-4, REQ-DRV-1, REQ-DRV-3, FR-2.5, FR-3.4, FR-4.4, FR-4.7, FR-12.1, FR-12.4, NFR-S4, NFR-S6, NFR-P8
**Packages:** `internal/source/drivers/dynamodb`, `cmd/ikigai`

## Context

DynamoDB is a document store that calls its collections tables. An item has a
primary key — a partition key, optionally with a sort key — and beyond those
two attributes nothing about its shape is declared: two items in the same table
need have nothing else in common. There is nothing above a table to choose
between, a connection being to one region. And it is a service, not a server:
there is no version to report and no session to end.

Three things about its read operation shape the driver. A `Scan` has no order —
the service returns items as it finds them. It has no offset. And it pages: a
call returns up to a megabyte and says where to carry on from.

## Decisions

1. **A DynamoDB table is a `KindCollection`.** An object's kind in this model
   is what it is rather than what its engine calls it — a Cassandra keyspace is
   a `KindSchema` because it contains tables — and a set of items with no
   declared shape is what the model calls a collection. The class label follows
   from the kind and reads the same on every engine, which is the point
   (REQ-DB-4). `Describe` returns a `*model.Collection` with an inferred shape,
   which is what the structure tab already draws for MongoDB.

2. **The SDK, not the protocol.** This is the opposite of ADR-0110's choice for
   cloud token minting, and the difference is what was being bought. There it
   was three SDKs for one small documented signature, and writing SigV4 was a
   day's work held to AWS's own published test vectors. Here it is a data
   client: the `AttributeValue` types, the paging, the retry with jitter and
   the error taxonomy *are* the thing, and writing them again would risk
   somebody's items to save 5.1 MB. It also keeps the network where every other
   driver's is — in the driver's library rather than in our own `net/http` —
   which the offline rule already allows and would have had to be amended for.

3. **A number keeps its digits.** DynamoDB's numbers carry up to 38 significant
   digits and arrive as text. A whole number small enough to be an `int64` is
   one, because a grid right-aligns and sorts those and an item's key is very
   often a small integer; everything else becomes `model.Decimal`, which is the
   model's way of saying "an exact number, as its digits". Reading them as
   floats would quietly change values a person is looking at.

4. **The null is a value.** An attribute set to `NULL` is there and holds
   nothing; an attribute nobody wrote is not in the item at all. The first
   becomes `nil` and the second is simply absent from the row — and a filter
   for "empty" means either of them, because the grid shows both the same way.

5. **A sort is refused, and an offset is paid for.** There is no order to ask a
   scan for, so a sort is refused rather than ignored (REQ-DRV-3): unsorted
   items presented as sorted is the failure that rule exists for. There is no
   offset either, but paging is what the grid does, so a later page is reached
   by reading the pages before it and dropping them — the same bargain the
   Redis driver strikes with `SCAN`, and said plainly in the code because it is
   a read the person is charged for.

6. **Every name and every value goes in through a placeholder.** Not a nicety:
   "name" and "size" are two of DynamoDB's several hundred reserved words, and
   an attribute name written into a filter expression is a name interpolated
   into a statement (NFR-S6). So `#c0` for a name and `:v0` for a value,
   everywhere, including a projection.

7. **A LIKE pattern is a prefix or it is refused.** DynamoDB has `begins_with`
   and `contains` and no pattern language at all. A pattern whose only wildcard
   is a trailing `%` is `begins_with` and nothing is lost; anything else is
   refused, because matching it would mean matching it somewhere it cannot be.

8. **Every write carries a condition.** `PutItem` and `UpdateItem` both create
   the item when it is not there. Without a condition, an insert would
   overwrite whatever is at that key and report success, and a change to an
   item somebody else had deleted would put it back. So an insert asks for the
   key not to exist and a change and a delete ask for it to exist, and the
   service refusing that condition becomes the contract's "no row matched".

9. **The tree describes every table to say which ones open.** Whether a table
   opens onto anything is whether it has a secondary index, and there is no
   call that lists the indexes of a region — so listing the tables costs one
   `DescribeTable` each, eight at a time. The alternative was claiming children
   for every table and giving nothing for most of them, which the conformance
   suite rightly refuses: a node that opens onto nothing reads as a failure.

10. **Four things are not claimed.** No query language: DynamoDB has PartiQL,
    and the requirements ask for this source at the depth the rest of this
    driver reaches, not for a second editor dialect. No picklist: a column's
    distinct values would mean scanning the whole table and counting in memory,
    which is a full read of a store charged by the read. No bulk load: the load
    contract's rules — empty the target first, a batch a transaction, a row
    refused stopping the load — are rules DynamoDB has none of, as for every
    other store here whose rows are not a table's. And no transactional write:
    `TransactWriteItems` takes at most a hundred items, and promising atomicity
    for some changesets and not others is worse than promising none.

11. **Read-only is the guard's alone.** There is no read-only session or
    transaction to ask a service for: an identity's permissions are the
    service's answer and not this connection's to set. So the guard is the only
    thing holding NFR-S4 here, and `guardcheck` holds it.

## Consequences

The binary went from 81.0 MB to 86.1, and the CI ceiling from 84 to 90. That is
the second time the ceiling has moved in a day, and it is worth saying plainly
rather than only recording: a ratchet that moves whenever something measured
arrives is documenting the growth, not limiting it. NFR-P8's 60 MB is the
owner's open decision (T4.30), and DynamoDB is now the third name on it after
Oracle and Kafka — three drivers, 30.3 MB between them, all three pure Go and
all three candidates for a build tag if the budget matters more than reach.

Sixteen modules arrived with the SDK, against seventeen direct dependencies
before it. They are in the SBOM and under govulncheck like everything else, and
that is the real ongoing cost rather than the megabytes.

## Alternatives

**Writing the protocol by hand,** on the existing SigV4. Rejected: see
decision 2.

**`KindTable`,** so the tree says "Tables" as AWS does. Rejected: the kind
would say relational about a store with no declared shape, and the model
normalises vocabulary on purpose.

**`RDB$DB_KEY`'s equivalent — no key at all, and edit by scan.** There isn't
one: every DynamoDB table has a partition key, which is why the identity is
always a primary key here.

**Claiming `BulkLoad` with `BatchWriteItem`.** Rejected: the call exists, the
contract's rules do not. `PutItem` overwrites rather than refusing, a batch of
twenty-five is not atomic, and emptying a table means reading every key and
deleting it.
