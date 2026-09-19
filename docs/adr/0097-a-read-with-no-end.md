# ADR-0097: A read with no end

**Status:** Accepted · **Date:** 2026-09-19
**Tasks:** T2.65 · **Requirements:** FR-13.6, FR-13.19, NFR-P10 · **Packages:** `internal/source/drivers/kafka`, `internal/app`

## Context

Every read in this application is paged: the grid asks for a window and the
source answers it, and a short page means the data ran out. A tail is the
opposite. The source answers when it has something, for as long as somebody is
writing, and it never runs out — so nothing about the paging path fits, and
the parts that do not fit are the interesting ones.

## Decisions

1. **A following read never says the log ended**, because it has not. `Next`
   waits for what is written next, or for the reader to give up. A read that
   returned end-of-data here would tell the grid a busy topic was empty.

2. **A tail begins where the log is now**, unless somebody names a position.
   Following is about what happens next; replaying a week of records to reach
   the present is a different request, and can still be made by naming one
   (the last N, an offset, a time) — those read what is there and then carry
   on.

3. **Reading from the end means something only while following.** A read that
   starts at the high watermark and stops there reads nothing at all, so it is
   refused outside a tail rather than returning an empty result that looks
   like an empty topic.

4. **A tail is bounded by its reader, not by a record count.** The driver
   stops when the reader gives up, and what keeps memory flat is the window
   the reader holds (NFR-P10) — because there is no number of records that is
   the right number to stop a live log at.

5. **The window drops the oldest, and counts what it dropped.** A tail is
   about what is happening now, so when the window is full the earliest record
   goes. How many went is kept and can be shown: a person watching a topic
   move faster than they can read should be told that, not quietly shown a
   gap.

6. **Pausing stops reading rather than discarding.** A paused tail asks the
   source for nothing, so the records wait where they are and resuming carries
   on from them. Pausing is backpressure, not loss — which is the only reading
   of "pause" that does not lose somebody's data while they are looking at it.

7. **Pausing takes effect at the next record, not inside a read.** A record
   already asked for arrives and is kept; the pause is observed when the
   follower comes round again. Anything else would mean discarding a record
   that was already in hand, which is the one thing pausing is supposed not to
   do.

8. **The guarantee from the message browser still holds.** A tail assigns its
   partitions explicitly and joins no consumer group, so watching a topic
   cannot move a production consumer's place in it (FR-13.19).

9. **Closing a following read takes no lock its reader holds.** A tail waits
   inside a poll for as long as nobody writes, and it holds the read lock the
   whole time it waits, so a close that wanted that lock could never run.
   Closing instead sets a flag guarded by nothing and closes the client, which
   is what ends a waiting poll; the read then ends rather than fails. This was
   found by a mutation that hung the runner for five minutes instead of failing
   a test, and the deadlock was reachable by any caller who closed a tail
   without cancelling its context first — an ordering the application layer
   happens to follow and a driver has no business requiring.

## Consequences

Kafka is the first source here to follow anything: every other driver refuses
`Follow`, and the capability gates no conformance check, so what holds this is
the tests written for it — a tail that waits rather than ending, takes what is
written next, carries on from a named position, and stops when it is given up
on.

The window lives in the application layer rather than the driver, beside the
paged reads, because what to keep is a question about the person watching
rather than about the log.
