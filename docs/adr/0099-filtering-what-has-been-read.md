# ADR-0099: Filtering what has been read

**Status:** Accepted · **Date:** 2026-09-19
**Tasks:** T2.67 · **Requirements:** FR-13.9, NFR-P10 · **Packages:** `internal/app/rowfilter`, `internal/app`, `internal/ui/shell`

## Context

Every filter in this application so far goes down to the server with the
browse: the filter row's notation becomes `source.Filter`s, the driver's
dialect renders them, and the answer is over the whole table. A broker will not
do that. It hands over bytes and asks no questions about them, so the Kafka
driver refuses a filter in those words (ADR-0095), and filtering records has to
happen here instead.

That is a weaker thing, and the weakness is the whole design problem.
`capability.Data` already says why: the grid must not offer filtering over
partial results, "which would silently mislead". A topic's browse is a window
over a log — a few hundred records out of millions — so a filter over it has
seen what it has read and nothing else.

## Decisions

1. **A filter here filters what has been read, and the footer says so.** Not
   "filtered", which is what a filter over a whole table says. A person who
   types a key and sees nothing must be able to tell the difference between
   "no record has this key" and "no record I have read has this key". The
   claim is narrow because the act is narrow.

2. **The same filter text means the same thing here as on a server.** These
   are the drivers' rules, not new ones: contains ignores case, a pattern's `%`
   stands for any run of characters and `_` for one, `NOT IN` keeps NULL rows
   unless NULL was listed, an empty list of values selects nothing, and
   excluding nothing keeps everything. Filtering that quietly meant something
   else on a topic would be worse than not filtering at all — the notation is
   the same notation, typed into the same row.

3. **Bytes are filtered as the text they carry.** A record's key and value are
   bytes; somebody typing `order-1` means the word, not its bytes. Numbers
   compare as numbers however they were carried, so that an offset filter is
   arithmetic rather than string ordering.

4. **A header is addressed by its name, or by name and value together.** A
   record's headers are a list of pairs, so each reads as `name=value`: typing
   the name finds the header, typing `name=value` finds that pairing, and a
   name sent twice keeps both of its values findable. A list matches where any
   one of its elements does, which is what filtering by a header means.

5. **A window is filled, not shortened.** The grid takes a short window to mean
   the data ran out, so a filter that dropped rows from a window would end the
   grid wherever the first few matches ran out. It reads further instead, until
   the window is full or the source has nothing more.

6. **How many rows match is unknown until everything has been read.** A count
   that was only what had been found so far would draw a scrollbar that lies.
   Unknown is a thing the grid already handles; a wrong number is not.

7. **A filter stops at a bound rather than reading a log forever**, and says
   that too. A log has no end worth reading to, and memory here must not depend
   on how much was written (NFR-P10). Stopping early is also why the count
   stays unknown in that case: it did not finish looking.

8. **It is offered where the server will not filter and the source is a
   stream.** The mechanism is general, but turning it on for every source that
   cannot filter — a wide-column store, a keyspace — changes what those grids
   claim, and each deserves its own answer rather than being swept along. That
   is left open deliberately.

9. **A filter naming a column the row has not got matches nothing**, rather
   than everything. It was asked and cannot be answered, which is not the same
   as being satisfied.

## Consequences

Searching a whole topic is a different act from filtering what is on screen,
and it is not this. It would mean reading the log from a position with a bound
and a progress report — closer to the tail (ADR-0097) than to the filter row —
and it should be asked for deliberately rather than smuggled in behind a
gesture that means something smaller everywhere else in the application.

The matcher is a pure function over a row, tested without a broker or a window,
because the rules it reproduces are the part that can quietly drift from the
drivers'.
