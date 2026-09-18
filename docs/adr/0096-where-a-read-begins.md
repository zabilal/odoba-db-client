# ADR-0096: Where a read begins

**Status:** Accepted · **Date:** 2026-09-19
**Tasks:** T2.64 · **Requirements:** FR-13.5 · **Packages:** `internal/source/drivers/kafka`

## Context

A log is read from a position. FR-13.5 asks for five: the beginning, the end,
the last N, a given offset, and a time — per partition or across all of them.

Four of those are a question about where to start. One of them is not.

## Decisions

1. **Reading from the end is following, and is refused here.** The end of a log
   is where nothing has been written yet: a read starting there has nothing to
   return until somebody writes, which is a tail (FR-13.6, T2.65) rather than a
   read of what is there. It is refused in those words rather than silently
   returning nothing, because returning nothing looks like an empty topic.

2. **The last N is N in each log.** Every partition has its own end, so a count
   back from the end is a count back in each: asking a topic of eight
   partitions for the last hundred records asks for the last hundred of each,
   and may return eight hundred. That is what the mode can mean without
   ordering records across partitions, which a log does not do. Anything the
   UI shows must say so — "the last N messages" would be wrong.

3. **A position before a log begins is its beginning.** Logs are aged out from
   the front, so an offset that was valid yesterday may be gone today. That is
   not an error: it is the log having moved on, and the read starts at what is
   left.

4. **A position past the end leaves that log out.** There is nothing there to
   read, and including it would make the read wait for records nobody has
   written.

5. **A time with nothing after it leaves that log out too.** A partition whose
   records all predate the moment asked for has nothing at or after it; reading
   it from the beginning instead would answer a question nobody asked.

6. **The arithmetic is a function over what the brokers said**, not something
   woven into the consumer. Where each partition's read begins and ends is
   computed from three plain maps — where the logs begin, where they end, and
   where a time falls in them — so every case above is proved without a broker.
   A healthy cluster will not hold still in these states long enough to test
   them, which is the lesson of every rule in this driver that a mutation
   walked past.

## Consequences

Four of FR-13.5's five modes land here, and the fifth is placed rather than
dropped: it is the tail, and the tail is next.

`Stream.SeekTimestamp` is claimed, because a time is now a position this
driver can read from — one more broker request, and only when somebody asks by
time.
