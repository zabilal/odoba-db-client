# ADR-0074: What a key holds, and a row that is an object

**Status:** Accepted · **Date:** 2026-09-13
**Tasks:** T2.41 · **Requirements:** FR-4.1, FR-4.4, FR-4.5, FR-12.2, NFR-S4 · **Packages:** `internal/source`, `internal/source/capability`, `internal/source/drivers/redis`, `internal/app`, `internal/ui/shell`

## Context

FR-12.2 asks for editors for each kind of value Redis holds: string, hash,
list, set, sorted set. They are five different shapes, and DBGate-like clients
give each a panel of its own.

This application has one place rows are shown and edited — the grid — and one
contract to reach it (REQ-DB-4). The question is whether a value needs a panel
per kind, or whether each kind is simply rows of its own shape.

The second question is how a person gets from a database's keyspace to one
key. ADR-0073 made keys rows rather than tree nodes, and a row has never been
something the UI could open.

## Decisions

1. **Every kind of value is rows.** A hash is its fields and theirs, a list its
   elements in order, a set its members, a sorted set its members and their
   scores, and a string the one value it is. The grid draws and edits all five
   without knowing which it has, because each browse says its columns and what
   tells one row from another — which is what every other source does.

2. **The column a row is known by is the one the server addresses it with**:
   a hash's `field`, a list's `index`, a set's or sorted set's `member`, and
   for a string the `key` itself, whose one row is the whole of the value. The
   two that are addresses rather than contents — a key's name and an element's
   position — are read-only: a key is renamed on its own, and an element moved
   is a rewrite of the list rather than an edit of it.

3. **A row that is an object of its own is the source's to say.**
   `source.RowObject`, paired with `capability.Data.RowObjects`: given a row of
   an object, the source answers what it names. The grid offers **Open What the
   Row Holds** where a source says so and nothing where it does not, so the
   UI still knows nothing about Redis (REQ-DB-1). It is the same move as a
   foreign key's Go to Referenced Row (ADR-0040), for a paradigm with no keys
   to follow.

4. **A plan begins by asking what the key is.** The commands a change becomes
   depend entirely on the kind, and a changeset does not carry it. One
   round trip settles it, and a key that has gone since the tab was opened is
   refused there, before a command is sent, rather than being recreated by a
   write that was meant to change it.

5. **A change is a look and then a write.** Redis has no command that changes
   a part of a value only if it is still there, and `WATCH` would hold a
   connection for the length of a person's edit. So each change looks first —
   `HGET`, `SISMEMBER`, `ZSCORE`, `LSET`'s own refusal — and a part that has
   gone since it was read fails as a row changed since it was read, without
   being put back. A part that is there already is refused rather than written
   over: `HSETNX`, `SADD`'s count, `ZADD NX`.

6. **A part renamed is moved in one transaction, carrying what it holds.**
   The two commands go as a `MULTI`, and the value or score comes from the
   part as it is when the change is made — so a plan that changes a field and
   then moves it moves what it changed. The plan renders the value it reads
   while planning, which is what it will write unless an earlier change in the
   same plan changes it.

7. **A plan is not atomic, and says so.** Each command stands on its own: a
   plan that fails partway leaves the commands before it, and the UI says so
   before committing (FR-4.5). The guard is asked first, as everywhere else
   (NFR-S4).

8. **What Redis cannot do is refused in its own words**, not worked around: a
   list's element cannot be deleted by position (Redis removes by value, which
   is `LREM` in the console), a string is set rather than inserted or deleted a
   row at a time, and a key is renamed on its own rather than by editing the
   value it holds.

9. **A value is text where it is text and bytes where it is not.** Redis holds
   bytes; what is valid UTF-8 is shown as the text it is, and what is not is
   shown as bytes rather than as mangled letters.

10. **Setting a string keeps its expiry** (`KEEPTTL`): editing a value is not
    a reason for a key to stop expiring.

## Consequences

- The five editors FR-12.2 asks for are the grid, five times, with everything
  it already has: the cell viewer for a long value, the form view, filtering,
  copying, and the review-before-commit path.
- A hash, a set and a sorted set are narrowed by the server as the keyspace is
  (`HSCAN`/`SSCAN`/`ZSCAN MATCH`); a list and a string are not, so a filter on
  either is refused rather than applied to the rows already read.
- `Data.RowObjects` is the first capability about what a row *is* rather than
  what can be done to it. A stream's records (T2.42) and a Kafka topic's
  partitions may want the same.
- JSON and stream values are named as what they are and refused until T2.42,
  rather than being read as something they are not.
