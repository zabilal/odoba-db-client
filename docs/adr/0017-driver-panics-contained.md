# ADR-0017: A driver's panic is contained

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T1.16 · **Requirements:** NFR-R1, FR-15.7 · **Packages:** `internal/panics`, `internal/app`, the drivers, `internal/ui/explorer/view`, `internal/ui/shell`

## Context

NFR-R1: a driver panic must not take down the application; each connection
is isolated and recoverable. Before this, nothing recovered a panic
anywhere. One nil map in a driver, on any of the goroutines that load
pages, count, list values, run scripts or ping, would have ended the
process, and every connection with it.

## Decisions

**A panic becomes an error where driver code is entered.** `internal/panics`
has two helpers to defer: `Recover`, for a function that returns an error,
and `Catch`, for a goroutine, which has nobody to return one to. Each turns
the panic into a `*panics.Error` ("the driver failed while counting rows,
and stopped: …") and writes the stack to the log, never into the message
(FR-15.7). The application installs its logger as the default, so the
stack reaches the log file.

**Every way into a driver is guarded.** A panic can be caught only on the
goroutine it happens on, so the guard sits at each entry:

- the app layer's calls: opening and reading rows, counting, listing
  values, writing INSERTs, building statements; opening, testing, pinging
  and closing connections; opening sessions, running and splitting
  scripts, and closing sessions;
- the goroutines the app starts: the result relay and the result reader;
- the goroutines the drivers start: each script runner turns a panic into
  its statement's error, each result hand-off closes its session, and
  MySQL's cancel watcher stops;
- the explorer, which calls a driver directly to list objects.

A ping that panics is a failed ping, so the connection reads as down.

**The connection can be dropped and opened afresh.** A driver that has
panicked may have left its own state broken. So the shell says what
happened in the error band and offers Disconnect, which closes the
connection's tabs and its driver; the next use opens a new one. The
application and every other connection carry on.

## Verification

Tests make a fake driver panic in a browse, a fetch, a result's rows and a
ping, and check that each ends in a `*panics.Error`. A shell test makes a
browse panic and taps Disconnect. Removing a guard makes the test binary
itself die of the panic. The guards inside the three real drivers are not
exercised by any test: nothing in them can be made to panic on demand
without adding a hook for it.

## Not decided here

Reconnecting automatically after a panic. For now a person decides.
