# ADR-0164: The same grid, following

**Status:** Accepted · **Date:** 2026-09-25
**Tasks:** T2.96, T5.12 (in part) · **Requirements:** FR-13.5, FR-13.6, NFR-P10, REQ-DB-2
**Packages:** `internal/app`, `internal/ui/shell`

## Context

Two things had been built, tested, and left unreachable. `source.Seek` finds a
position in a log — the beginning, the last few, an offset, a time — and every
driver with a log implements it. `app.Tail` follows a log into a window of fixed
size, with pausing that applies backpressure rather than dropping records
(ADR-0097). Neither had a caller: the window opened every object with an empty
`BrowseOptions`.

So J8, the journey about debugging a stream, could walk three of its five steps,
and TASKS.md said in as many words that finishing it "means building two controls
nothing in the task list claims". This is those controls. It is also the thing
T5.12 needs: Mongo's change streams and Redis's pub-sub are following reads, and
there was nowhere for a following read to go.

## Decisions

1. **A tail replaces the grid's fetcher; it is not a second view.** The grid asks
   a `Fetcher` for windows of rows, and a tail holds a window already — so
   `app.Tail` answers `Fetch`, `Count` and `Changes`, and following swaps what the
   model reads from. Stopping swaps it back. That is the bargain the pipeline
   editor already strikes with a collection's documents, and it means every other
   thing the grid can do — the viewer, the form view, copying, exporting — works
   over a tail without knowing one is there.

2. **Following begins where the log is now.** A tail that replayed a week of
   records to reach the present would be a different request, and one that can
   still be made: start somewhere, then follow. So turning it on passes
   `SeekEnd`, which is refused everywhere else and means "from now on" inside a
   tail (ADR-0097).

3. **A quiet log costs nothing.** The grid is drawn again only when records have
   arrived, which is what `Changes` is for: a counter that only grows, checked on
   a timer four times a second. A window that redrew itself on a topic nobody was
   writing to would be a window warming a room.

4. **What is not being seen is said.** The footer says following or paused, how
   many records are held, and how many were dropped out of the window. A person
   watching a log move faster than they can read it is owed that rather than a
   quiet gap (NFR-P10).

5. **A position is read as it was typed, and refused otherwise.** "The last few"
   needs a count above nothing, an offset a whole number, a time something that
   parses as one. A time nobody could read taken as "now" would read the whole
   log; a count nobody could read taken as one would read one record.

6. **A time is not offered where the source cannot find one.** The choice is
   built from `Stream.SeekTimestamp`, so a source that cannot answer it does not
   show it (REQ-DB-2). The rest of the choices every log has.

7. **Asking where to start while following stops following.** A tail begins where
   the log is now, so asking it to begin elsewhere is asking for a read. Stopping
   first is what that means, rather than a second reading of the same request.

8. **"The last few" says what it means in the dialog.** It is that many in every
   log, each partition having its own end, so asking a topic of eight partitions
   for the last hundred may answer eight hundred (T2.64). The window says so
   where the number is typed rather than in a note nobody reads afterwards.

9. **Three lines came out for being unprovable.** `SetFetcher` already drops
   what was cached, so invalidating after it said the same thing twice; a tab's
   browse and its grid are set together, so asking about both asked twice; and
   the guard that kept the "reading from" text out of the footer while following
   cannot be told from its absence, because while a tail runs the footer is the
   bar's own line. The same pass found an assertion of mine matching the message
   that goes away — "Reading from the last 5…" — rather than the count line that
   stays.

## Consequences

J8 walks all five of its steps now: a topic opened, records decoded against the
registry, exported as they were read, the log read again from its beginning, and
a record written while the window was watching arriving in it. T2.96 closes, and
with it the last thing Phase 2's exit was waiting on.

What this does not do: it does not offer a tail on anything but a log, because
nothing else claims `Stream.Follow` yet — that is T5.12, and the point of doing
this first is that a following read now has somewhere to go.

## Alternatives

**A separate live view, beside the grid.** Rejected: a record in a tail is a row
like any other, and a second view would need its own viewer, its own form view,
its own copying and its own export. The grid already has all of them.

**Following by re-reading the log on a timer.** Rejected: that is polling, it
re-reads what it has already read, and a driver that follows properly — which
every log driver here does — answers when somebody writes.

**Dropping the oldest silently.** Rejected by NFR-P10 and by decency: the count
of what fell out of the window is the only way somebody can tell that they are
not seeing everything.

**Asking for a position with a text box and a grammar.** Rejected: four choices
and three fields is a dialog somebody can read, and a grammar would be a parser
with its own errors for a question with four answers.
