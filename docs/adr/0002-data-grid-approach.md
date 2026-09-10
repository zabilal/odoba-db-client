# ADR-0002: `widget.Table` is the data grid, provisionally

**Status:** Accepted, pending an interactive confirmation · **Date:** 2026-09-10
**Spike:** W1 (TASKS.md T0.38–T0.44) · **Risk:** RISK-1

## Context

The data grid is the product's centre of gravity. RISK-1 states the case
plainly: if the grid is mediocre, the product is mediocre. Since ADR-0001 chose
a pure-Go toolkit, the grid is ours to build, and the question was whether
Fyne's `widget.Table` can carry it or whether we need a custom raster-drawn
grid (T0.43).

`widget.Table` already supplies virtualisation, sticky rows and columns,
per-column widths and a header row — most of FR-3.1 and FR-3.2. It also
allocates one `CanvasObject` per visible cell, and at 30 columns by 60 rows
that is 1 800 objects per frame.

## Measurements

Against a real 10 000 000-row PostgreSQL table (2.4 GB, mixed types: long text,
NULLs, JSONB, numerics, timestamps) and a matching synthetic generator.

**Data path**

| | Result | Budget |
|---|---|---|
| First page resident, cold | 34 ms | 300 ms (NFR-P3) |
| `count(*)` over 10M rows | 310–950 ms | — |
| `Model.Row` at 10K rows | 13 ns, 0 alloc | — |
| `Model.Row` at 100M rows | 13 ns, 0 alloc | must not grow (NFR-P13) |

**CPU frame cost** (16.7 ms = 60 fps, 33.3 ms = 30 fps floor, NFR-P4)

| | Result |
|---|---|
| Format 13 cells | 0.48 µs |
| Update 520 cells (typical viewport) | 0.042 ms |
| Update 1 200 cells (30 columns) | 0.065 ms |
| Update 1 800 cells (30x60) | 0.099 ms |
| `Table.Refresh` incl. layout | 0.19–1.40 ms |

The CPU path has roughly two orders of magnitude of headroom.

## What could not be measured, and why the first gate was wrong

The first version of this gate measured `Canvas().Capture()` through Fyne's
test driver and **failed** at 38.96 ms mean. That number was misleading, and a
control experiment established why:

| | Time |
|---|---|
| Empty window, one rectangle | 23.0 ms |
| Full grid, 520 cells | 36.6 ms |
| **Grid's own contribution** | **13.6 ms** (26 µs/cell) |

63% of the "frame" was Fyne's test driver rasterising 1.4M pixels in software,
on the CPU, with no GPU — work the shipping GL build does not do. Gating on it
would have failed the spike for a cost that does not exist in production, and
sent us to build a raster fallback we may not need.

The gate now asserts CPU work per frame, which is real, portable and regresses
first as the grid grows. `control_test.go` is kept: it is the evidence for why
the capture number is not a gate, and re-running it is how a future reader
checks that reasoning still holds.

**True frame cadence remains unmeasured.** It needs a window server driving the
render loop; this session had none, so the GL path was never exercised.
`cmd/gridspike` samples Fyne's animation callback — which runs on the render
loop, so its intervals are real frame times with present included — and must be
run on an attended machine:

```
go run -tags spike ./cmd/gridspike -bench
```

## Decision

**Build the grid on `widget.Table`.** Do not build the raster fallback yet.

The evidence supports it: the data path is fast and provably independent of
result size, and the CPU frame cost has two orders of magnitude of headroom.
The open question is GPU compositing of ~1 800 small objects, which Fyne
handles with cached text textures and which is very unlikely to consume 16 ms
on any machine this application targets.

**GATE G0-1 is provisionally passed.** It closes fully when the interactive
harness is run and p95 lands inside the 30 fps floor. If it does not, T0.43 is
the answer and this ADR gets superseded — the model, formatter and cell layer
are all renderer-independent, so that swap costs the renderer only.

## What the spike changed in the design

**Refreshing per page load is a correctness bug, not just waste.** The first
interactive run crashed with `concurrent map read and map write` inside Fyne's
`tableCellsRenderer`. During a fast scroll many pages land within milliseconds,
each calling `fyne.Do(Table.Refresh)` from its own goroutine; those re-enter
the table renderer while it is already refreshing and corrupt its internal cell
map. It is a hard runtime fault, not a glitch.

`TableGrid.ScheduleRefresh` coalesces refreshes into at most one per 16 ms.
This was found only because the spike drove a real window against a real
database — no unit test would have produced the interleaving.

**Cells are a custom widget, not `widget.Label`.** A Label carries a full
widget lifecycle — its own renderer, padding, wrapping and truncation — and the
grid instantiates one per visible cell. `cellWidget` draws exactly two
primitives, a background rectangle and a text object, and skips work entirely
when a cell's content is unchanged.

**Long values are capped at 200 runes for display** (`MaxCellRunes`). Without a
cap, one 40 KB text column stalls a frame on text measurement alone. The full
value stays available in the cell viewer (FR-3.9).

## Follow-ups

- Run the interactive harness and close G0-1. **Owner action.**
- `Format` allocates ~2.25 times per cell. Harmless at current cost but worth
  reducing before Phase 1 ends, since it is sustained GC pressure during a
  scroll.
- `widget.Table.Length` returns `int`, capping result sets at 2^31 rows on
  32-bit platforms. Acceptable; noted so it is a known limit rather than a
  surprise.
