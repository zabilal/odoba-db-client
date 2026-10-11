# ADR-0172: Fonts nobody asked to keep

**Status:** Accepted · **Date:** 2026-10-11
**Tasks:** T4.30, T4.32 · **Requirements:** NFR-P6, RISK-10
**Packages:** `internal/ui/grid`, `internal/testutil/fonts`, `internal/plugin`, `internal/ui/shell`

## Context

Two jobs were red on the commit after ADR-0171, on a change that touched
neither. The Windows build died nine minutes into the window's suite with
`ThreadSanitizer failed to allocate 134217728 bytes (error code: 1455)` — no
assertion, no test named. The macOS build failed one plugin test on a handshake
that timed out.

The first looked like a runner being small. It was this application keeping
memory, in two ways, and one of them is in the product.

## Decisions

1. **The grid's small controls share one theme scope.** A column's filter and a
   cell's editor are entries drawn with the compact theme, and each was wrapped
   in `container.NewThemeOverride`. In Fyne 2.8.1 every override is a scope of
   its own — a new one when it is made, another when its renderer is, another
   *each time it is refreshed* — and `painter.CachedFontFace` is keyed by the
   scope, so the theme's fonts are parsed again for every scope that measures
   text and kept until the theme changes. Measured: 6.4 MB kept and 13ms spent
   per scope. A header refreshes its filter whenever the table is refreshed, so
   that was the cost of looking at a table, per column, repeatedly; and of every
   cell anybody started to edit.

   `grid.small` replaces the override. One object nothing draws is given the
   compact theme once, and every small control is put into that object's scope
   with `cache.OverrideThemeMatchingScope` — the function Fyne uses to put a
   list's rows into the scope of the list. Fyne does not export it, so it is
   reached with `go:linkname`.

   That is a reach into another module's internals and is meant to look like
   one. The alternatives were worse: there is no public way to share a scope;
   a fork of the toolkit to change one cache key is a fork of the toolkit; and
   drawing these two controls at full size breaks the row height the grid is
   built round. A Fyne that renames the function fails the build at the link,
   and the tests hold what matters either way — that a small control keeps
   nothing of its own, and that it is the size Fyne's own override would have
   made it.

   Fyne forgets a widget's scope when it lets go of the widget's renderer, so
   the wrapper adopts its content again whenever its own renderer is made and
   whenever it is refreshed, as Fyne's override does.

2. **A test suite gives back the fonts its earlier tests used.** This one is not
   in the product. Fyne shapes text with one shaper for the life of the process,
   and the shaper's buffer keeps a plan for every face it has shaped, by the
   face, with nothing that removes one (`harfbuzz.Buffer.planCache`). An
   application has a handful of faces. A suite that makes a new Fyne application
   per test parses the theme's fonts again each time — `test.NewApp` empties
   Fyne's own cache, so they are new faces — and all of them stay reachable.
   Eight hundred tests of the window at about five megabytes each: 4.3 GB held
   at the end and 7.9 GB at the peak, without the race detector.

   `testutil/fonts.Forget` replaces the shaper, and the window's test helper
   calls it before making each application. The suite now peaks at 1.6 GB and
   runs a quarter faster, most of what it had been doing being page faults.

   Its test asserts both halves: left alone the fonts stay, forgotten they go.
   The first half is there so that a Fyne which stops keeping them shows up as
   this package having nothing left to do.

3. **A test's stand-in answers when asked.** The test plugin's `deaf` and `bye`
   modes wrote their hello as soon as they started, with the request's number
   written in by hand. The host drops an answer nobody is waiting for — rightly —
   so a plugin that started faster than the host got to its question had its
   hello dropped, and the test failed ten seconds later on the handshake.
   Reproduced by delaying the host three seconds; with the stand-in reading the
   request first and answering its number, the same delay passes.

   Nothing in the host changed. A plugin that speaks before it is spoken to is
   not following the protocol, and dropping what it says is what
   `docs/PLUGINS.md` says happens.

## Consequences

- Two symbols of Fyne's are reached by name, one in the product and one in
  tests. Both are listed here so that an upgrade which breaks the link has
  somewhere to start: `fyne.io/fyne/v2/internal/cache.OverrideThemeMatchingScope`
  and `fyne.io/fyne/v2/internal/painter.shaper` (and, in one test,
  `cache.DestroyRenderer`).
- `github.com/go-text/typesetting` is a direct dependency now, for the shaper's
  type. It was already in the build.
- Both causes are upstream's to fix properly: a theme override that makes a
  scope per refresh with a font cache keyed by scope, and a shape-plan cache
  with no eviction. Neither has been reported yet.
- Whether this is all of the Windows failure is not known until that job runs.
  The suite needed more than the runner would commit; it now needs a fifth of
  what it did.
