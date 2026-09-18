# ADR-0079: A keyspace in the conformance suite

**Status:** Accepted · **Date:** 2026-09-14
**Tasks:** T2.46 · **Requirements:** REQ-DRV-1, FR-4.4, FR-4.5, FR-12.2, NFR-S4 · **Packages:** `internal/source/conformance`, `internal/source/drivers/redis`

## Context

ADR-0071 gave the shared suite a document paradigm beside the relational one,
and said a key-value store would need a third. Run against Redis unchanged,
the suite passed every check but its two write checks, which assume a table:
an `id` column, a key the server numbers, a rollback. A Redis keyspace has
none of them, and it is shaped unlike both other paradigms in two ways.

It is rows twice over. A database's keys are its rows, and each key opens onto
what it holds, which is rows of its own (`source.RowObject`, ADR-0074): a
hash's fields, a list's elements, a stream's entries. Writing happens at both
levels — a key's time to live and name on the keyspace, a field or a member
inside a key.

And the suite cannot make a key. A key comes into being when something is
written to it (ADR-0076 §6), and no row of a keyspace is that something. The
document checks could fill an empty collection themselves; these cannot fill
an empty keyspace.

A Redis connection also takes three shapes — a single server, one with the
JSON module, and a cluster — and the code that answers differs between them.

## Decisions

1. **`checkWriter` has a third branch, `checkKeyValueWriter`**, chosen by
   `Capabilities().Paradigm`. `writableIdentity` asks a browse for its rows'
   identity in every paradigm but the relational one, so the read-only guard
   check keys its change by a key's name rather than by an `id`.

2. **The target fills the keyspace.** For a key-value store `Target.Writable`
   is a keyspace holding at least three keys the target wrote, one holding a
   value a change can be written to. The checks change and delete keys, and
   a key planned as a new row of its keyspace must be refused: it would hold
   nothing.

3. **Every key is opened onto what it holds.** Each row of the keyspace names
   an object of a declared kind that is not the keyspace itself; its rows have
   columns, are told apart by columns they have, and count as they read where
   the source claims an exact count. Then, inside each key: a change to a
   part that is not there writes nothing; a change writes what it names and
   leaves the rest alone; a part is added with what it was given, needing no
   key; one added under a key another part has is not written over it; and a
   part taken away takes nothing with it. On the keyspace: a key's own column
   given a value reads as one, emptied reads as empty, and no other key
   changes; a key deleted is gone, and so is what it held; a plan whose last
   change is to a key that has gone fails there, with the changes before it
   undone only where the source claims transactions. The guard is checked as
   in the other two paradigms, in the same words.

4. **A change is refused when it is planned, or does just what it says.** A
   kind of value takes only some changes — a log is never rewritten, a string
   has no part to add, a list's element is removed by value — and that is the
   kind's to decide. So a refusal from `Plan` is logged rather than failed, and
   a run's log says what each kind refused and why; but a refusal must have
   written nothing, and a plan accepted must do what it says when applied.

5. **What is written is what the grid would hand a writer.** The checks type
   a sample into each column by its class and read it with `value.Parse`, as a
   cell is read. A driver's own tests had written JSON as a Go string; the grid
   hands it over as `model.JSON`, and only writing it that way showed the
   difference.

6. **Rows are compared as a set, and time as what is left of it.** A keyspace
   is walked in no order, and neither are a set's members, so rows are compared
   sorted. A time to live counts down between two reads of it: rows compare it
   by whether there is one, and a value written reads back as more than
   nothing and no more than was written.

7. **Redis answers to the suite in all three shapes**, each target filling a
   keyspace that is emptied first and after: a single server (a database to
   browse and another to write), the server with the JSON module (a document
   among the keys), and the cluster (keys shared out between its shards). The
   gate now requires all three servers, as it requires the others.

8. **The checks are checked.** Redis passes them, so a check that stopped
   checking would pass as well. The checks report through a small interface
   rather than `*testing.T`, and a unit test runs them against a keyspace in
   memory, once as it should be and once for each fault put into it, and each
   fault must be found by the check written for it.

## Faults found

Three faults in the Redis writer, all fixed:

- **A value that is one row was written through another key's change.** A
  string and a JSON document are one row, addressed by the key's own name,
  and the writer never looked at the name a change carried: a change keyed
  by anything wrote this key's value. A change for another name is now
  refused when it is planned.
- **A list position past 32 bits wrote over another element.** Redis reads
  `LSET`'s position as a 32-bit number, so 2³² was taken for 0 and written
  there. The writer relied on the server's "index out of range". It now
  reads the list's length and treats a position outside it as no element,
  which is a row changed since it was read.
- **JSON as the grid reads it reached Redis as a slice of numbers.**
  `model.JSON` is a byte slice, which `str` printed as one, so an entry added
  to a stream and a document set from the grid were refused as not JSON.

Putting the cluster through the suite found a fourth, which no check of the
suite's touches: **a rename between hash slots.** A cluster renames a key only
within its slot and answers CROSSSLOT otherwise, which ADR-0076 §8 missed by
saying each change has one place to go — a rename names two keys. It is now
refused before it is sent, in words that say names sharing a `{tag}` share a
slot. Moving the key between shards instead (a copy and a delete) was not
chosen: it is not atomic, it holds the whole value in the client, and it is
not what a person renaming a key asked for.

Two live tests closed their connection before their cleanups ran — a deferred
`Close` runs before `t.Cleanup` — so the keys they wrote stayed on the cluster
and the JSON server. The suite reads every key there, so its targets empty the
keyspace first; the two tests now close in a cleanup of their own.

## Consequences

Every paradigm that writes now answers to the suite. A key-value driver after
Redis inherits the checks with no Redis in them: a keyspace, its keys'
objects, the samples a grid would send.

The key-value checks log what they skip rather than failing it, so a run can
be green while a kind refuses a change it should take. That is covered by the
driver's own live tests of each kind, not by the suite, which cannot know what
a kind ought to accept.
