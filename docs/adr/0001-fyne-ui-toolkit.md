# ADR-0001: Fyne as the UI toolkit

**Status:** Accepted · **Date:** 2026-09-09

## Context

Ikigai DB needs a virtualised editable data grid, a dialect-aware code editor,
a pan/zoom node canvas and interactive charts. Candidates were Wails (Go core
plus a web frontend in a native webview), Fyne, Gio, Qt bindings and Electron.

An initial recommendation favoured Wails, because mature implementations of all
four components exist in the web ecosystem and essentially nowhere reachable
from Go.

## Decision

**Fyne, pure Go end to end.** No web technologies, no embedded browser.

This was the owner's explicit decision, overriding the Wails recommendation.
One language, one toolchain, one debugger and one dependency graph is treated
as a project goal in its own right, not a tiebreaker.

Fyne over Gio because the application's centre of gravity is a table, a tree,
splits and tabs. Fyne ships production versions of all four; Gio ships none of
them, so Gio would mean building those *before* starting on the four hard
components.

## Consequences

**Accepted costs**

- Four components must be built from scratch (REQUIREMENTS §8.4, W1–W4). The
  query editor is the largest single build risk in the project; nothing in Go
  approaches CodeMirror.
- CGO is required to build. The application is pure Go; the build is not.
  Single-host cross-compilation is out — CI needs native runners per platform.
- Canvas-based text rendering. Dense text-heavy views are the workload most
  exposed to this, hence the mandatory W1 spike before anything is built on
  the grid.
- Accessibility is a known gap; screen-reader support is not promised for v1.0.
- Fyne's stock Material-derived theme is not shippable for this product's
  intended feel, so a custom theme is a Phase 0 deliverable rather than a
  polish task.

**Benefits realised**

- The pure-Go build constraint disappeared. Since CGO is required anyway,
  DuckDB, `mattn/go-sqlite3` and libSQL no longer need build tags.
- No Node toolchain, no bridge serialisation between UI and core.

**Mitigation**

ARCH-1 keeps every core package free of Fyne imports, enforced mechanically by
a depguard rule in `.golangci.yml`. If this decision is ever revisited, the
valuable core is portable.
