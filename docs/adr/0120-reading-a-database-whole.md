# ADR-0120: Reading a database whole

**Status:** Accepted · **Date:** 2026-09-22
**Tasks:** T3.9 · **Requirements:** FR-7.1
**Packages:** `internal/source/drivers/postgres`, `internal/app`

## Context

ADR-0119 built a comparison that takes two models and reads nothing. This is
where the models come from.

`Describe` answers one object and asks the catalogue five times to do it.
Comparing two databases that way is a round trip per object per side, which
on a real schema is thousands. That is why `source.Snapshotter` was declared
when the driver contract was written — and, until now, implemented by nobody.

## Decisions

1. **Every query in the snapshot reads the whole database at once, and the
   rows are stitched together by relation afterwards.** Nine queries,
   whatever the schema holds.

2. **The bulk queries are the per-object queries with the filter taken out,
   and the code that reads a row is shared.** Two readings of the same
   catalogue that could drift apart would make a comparison report
   differences that are this program's rather than the databases'. The live
   test is written as exactly that claim: read the fixture schema both ways
   and compare the two with the comparison engine.

   That test found two disagreements the moment it was written, and both
   were real:

   - `Describe` never filled a table's triggers, although the canonical
     model has held them since it was written and the structure tab has
     drawn an empty Triggers section for them ever since.
   - An included column of an index kept the quotes `pg_get_indexdef` prints
     round a name that needs them, while the key columns had theirs trimmed.
     The DDL generator quotes what it is given, so an index including a
     column called `Order` rendered `INCLUDE ("""Order""")` — a column
     nobody has. Nothing had ever compared the two readings.

3. **A trigger's timing, events and condition are read from `tgtype` and
   `tgqual`, not parsed back out of the statement.** The bits are
   PostgreSQL's own and have not moved in a very long time; the alternative
   is parsing what `pg_get_triggerdef` prints, which is the same information
   spelt out. `INSTEAD OF` sets the `BEFORE` bit as well, so the order the
   two are read in is the difference between a trigger on a view being
   reported correctly and being reported as a `BEFORE` trigger.

4. **A driver with no `Snapshot` is walked object by object, by `app`.** The
   walk reads what the explorer reads, through the same calls, so an engine
   nobody has written a `Snapshot` for can still be compared. The difference
   between the two is how long it takes and nothing else: both fill the same
   model, and a comparison cannot tell which produced it.

   The walk lives in `internal/app` and not in `internal/diff`, because a
   comparison that could reach a server could not be tested without one.
   `Snapshotter`'s own doc comment said `internal/diff` would do it; the
   comment was written before ADR-0119 and is corrected here.

5. **A trigger and an index are not read from their own folders.** They
   arrive with the table they are on, which is where the canonical model
   puts them, and reading them again would double them.

6. **An object that will not describe fails the read.** A schema quietly
   missing a table would compare as a table somebody had dropped, and a sync
   script would offer to drop it.

7. **The system schemas are left out.** A comparison that reported
   `pg_catalog` would report it every time and be right about nothing.

8. **A failure says which of the two sides it was.** "Could not read the
   database" is half an answer when two were being read.

## Consequences

- PostgreSQL claims `Schema.Diff` now, which the conformance suite pairs
  with implementing `Snapshotter`.
- The structure tab gains a Triggers section that was always drawn and
  always empty, now with the timing and events it has a column for.
- A snapshot takes a pointer into each schema's slices of tables and views,
  so the stitching can write through them. Appending moves a slice, so the
  pointers are taken again once every append is done rather than mid-loop —
  a mutation that skips that step is caught by the live test, which is the
  only place it would show.
- Nothing in the window compares anything yet. T3.11 is the tree that draws
  this, and T3.10 is comparing against a model saved to disk.
- `Describe` still answers no user type, so a walked snapshot has none while
  a one-pass snapshot does. On PostgreSQL that costs nothing, because
  PostgreSQL does not walk; on an engine that does, a comparison will not
  see its types.
