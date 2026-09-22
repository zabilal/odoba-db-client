# ADR-0021: Performance gates measure what CI can see

**Status:** Accepted · **Date:** 2026-09-11
**Tasks:** T1.73, T2.97 · **Requirements:** NFR-P1–P6, NFR-P10, NFR-Q3 · **Packages:** `internal/app`, `internal/ui/grid`, `internal/ui/explorer/view`, `internal/ui/shell`, `internal/ui/editor`, `internal/testutil/race`

## Context

NFR-Q3 asks for benchmarks that assert NFR-P1 to P6 in CI. Those budgets
are end to end: a window on screen, a tree drawn, rows visible. A CI runner
has no GPU and no display, and Fyne's test driver draws in software, so an
end-to-end figure there measures the runner, not the application. The
Phase 0 gates already took this line for NFR-P4 and P5 (ADR-0002).

## Decisions

1. **Each gate times the application's own CPU work, against a fraction
   of its budget**, leaving the rest to what it cannot see: the graphics,
   the driver and the server.

   | NFR | Budget | Gate | Fraction held | Measured (2026-09-11) |
   |---|---|---|---|---|
   | P1 cold start | 800 ms | `TestGateP1ColdStart`: building the window from its stores, the session read back | 200 ms | 91 ms |
   | P2 1 000-object tree | 2 s | `TestGateP2ThousandObjectTree`: loading and drawing it | 500 ms | 4.3 ms |
   | P3 first 200 rows | 300 ms | `TestGateP3FirstRowsDrawn`: the grid built and drawn once their page is in | 100 ms | 0.46 ms |
   | P4 scrolling | 16.7 ms a frame | `TestGateG0_1`: a viewport update and a table refresh | 4 ms, 8 ms | (Phase 0) |
   | P5 keystroke to glyph | 16 ms | G0-2 in `highlight_test.go`: re-highlighting | 4 ms mean, 16 ms worst | (Phase 0) |
   | P6 idle memory | 300 MB | `TestGateP6IdleHeap`: the heap five connections add, a table open in each | 100 MB | 2–5 MB |
   | P10 live tail | responsive, memory bounded | `TestGateP10LiveTail`: taking 20 000 records, and the heap a tail holds after 2 000 000 | 500 ms, 16 MB | 1–3 ms, 0 MB |

2. **P3 is measured in an application already running.** Its first
   measurement was 110 ms, over its 100 ms. That was the process's first
   window paying once for fonts and for laying out each kind of widget,
   which is cold start's cost and already inside P1's figure. The gate now
   draws a grid first, logs its time, and holds the second to the budget.

3. **P10 measures the tail, because the window does not reach one.** The
   requirement is about what a person sees while a topic runs at 10 000
   messages a second, and nothing in the window follows a topic yet (T2.96).
   Holding a figure for that would be holding a figure for something that
   does not exist, so the gate holds the half underneath: what taking the
   records costs, against a quarter of the time they arrive over.

   Its timing half has a great deal of headroom — 1 ms against 500 ms —
   because a tail's own bookkeeping is a mutex and a ring write. That is not
   a loose gate by accident. It fails when a tail does per-record work it
   cannot afford, which is the regression that matters; it does not fail on
   a few milliseconds, which would only make it fail on a slower runner for
   something nobody could see.

   Its memory half cannot be a budget alone: a tail that kept every record
   would also pass a budget, on a small enough topic. So it runs the tail
   again over a hundred times the records — two million, which a tail that
   kept them would need something like a hundred megabytes for — and holds
   what it adds to what the first run added.

4. **P6 holds the heap its connections add, not the whole heap.** Read
   whole, the heap of a test binary holds what every earlier test left
   behind: the gate measured 35 MB alone and 254 MB after the rest of its
   package, and failed its first full run. It now reads the heap after a
   collection before the five connections open and after, and holds the
   difference, which was 5 MB alone and 2 MB after the rest.

4. **The gates do not run under the race detector** (`race.SkipTimingGate`),
   which slows code 5–20 times; nor under `-short`. CI runs them in its
   performance job.

5. **What a gate cannot see needs an attended session**: true frame rate
   (`go run -tags spike ./cmd/gridspike -bench`), the time to a window on
   screen, and the process's resident memory, of which the Go heap is one
   part.

## Verification

Each gate fails when the code it times is made slower or heavier than its
budget: a 150 ms pause in building a grid, 600 ms in the explorer's load,
250 ms in building the shell, and 30 MB kept for every table opened.

## Not decided here

A trend of these figures across commits, to catch a slow drift that stays
under its budget.
