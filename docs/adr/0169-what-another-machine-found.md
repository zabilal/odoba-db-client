# ADR-0169: What another machine found

**Status:** Accepted · **Date:** 2026-09-30
**Tasks:** T4.31, T4.32, GATE G0 · **Requirements:** NFR-D3, NFR-Q1, NFR-Q3, RISK-10
**Packages:** `internal/ui/canvas`, `internal/app/connstr`, `internal/cloud`, `internal/importer`, `internal/single`, `internal/plugin`

## Context

ADR-0168 made CI run. This is what it said.

Everything in this project had been built and proved on one machine: macOS on
arm64. RISK-10 named that years of task lines ago — "cross-platform build
unproven" — and the mitigation was always "CI will tell us". CI now tells us, and
the answer is six faults, in five packages, none of which a second machine of the
same kind would ever have found.

## Decisions

1. **A diagram may not hand back overlapping tables, on any architecture.** The
   force simulation is chaotic and the pass that separates boxes afterwards was
   bounded: a hundred and twenty relaxation passes, and then whatever it had. On
   arm64 that converged; on amd64, for the same schema, it left one pair on top of
   each other. Floating point decides it, so the guarantee cannot rest on the
   relaxation. It rests on a sweep: taken in order, a table that overlaps anything
   already placed moves right of all of it, which cannot overlap by construction,
   so by induction nothing does. One pass, no convergence to wait for.

2. **The sweep runs inside a component and again over everything.** Inside,
   because resolving there keeps the arrangement compact — leaving it to the end
   spreads a pile across the whole diagram, which the zoom-1.0 gate sees as a
   picture with nothing in it (G0-3, and that gate is what caught it when this was
   tried the other way). Over everything at the end, because packing moves a
   component as a whole and leaves its pinned tables where they are, so a free
   table can be carried onto a pinned one — the one overlap a pass over each
   component separately cannot see.

3. **Two pinned tables in one place stay there.** Pinned means pinned: what moves
   is whatever else wanted that space. So the sweep places pinned tables first, as
   anchors — taking them last would mean skipping one and leaving the overlap it
   was in — and never moves one. Two of them on top of each other is what somebody
   who dragged them there asked for, and a layout deciding otherwise would be a
   layout that knew better.

4. **A connection string's scheme is read before the rest of it is parsed.** A
   scheme nobody handles is clearer than a malformed URL, and the rest may well
   not parse: `sqlite://C:\Users\…` is unparseable as a URL, so on Windows
   somebody typing it was told to check their password for unescaped characters
   when what was wrong was that SQLite opens a file rather than dialling an
   address. The refusal now names the scheme, on every platform.

5. **CLOUDSDK_CONFIG is honoured everywhere gcloud honours it.** Which is
   everywhere. The lookup read it on Unix and not on Windows, where it went
   straight to `%APPDATA%\gcloud` — so somebody who had moved their gcloud
   configuration would have been told they had never signed in. A real defect,
   found by a test that had always passed on the only platform it ran on.

6. **A file mode means what the operating system means by it, and Windows means
   nothing.** Two tests asserted Unix modes: a password file others can read is
   refused, and a single-instance socket is the user's alone. The production code
   already knew better in one case — libpq makes no permission check on Windows,
   and neither does this — and the test did not. Both tests now assert the
   platform's own answer rather than skipping, because a check that quietly
   stopped happening on a platform is the thing worth catching.

7. **A deadline is not a budget, and neither is a test timeout.** The window's own
   suite takes over eight minutes under the race detector on this machine; on a
   hosted runner it went past Go's ten-minute default and was killed, which reads
   as a failure and is a clock. The run has thirty minutes now. This is the same
   distinction `race.Slower` draws inside the tests (ADR-0168), applied to the
   command that runs them.

8. **A read gives up when it runs out of what it was holding.** A plugin test
   required the very next read after a cancellation to report it; on Linux a row
   was already in hand and was returned first, which is correct — the same bargain
   a paused tail strikes (ADR-0164). The test reads until the cancellation
   arrives now.

## Consequences

Linux and Windows are proved for the first time, and macOS is proved under the
race detector. T4.31 and T4.32 move on that: the journeys and the suite run on
three platforms rather than on one with the other two assumed.

RISK-10 is smaller but not closed. What CI builds is not what a user installs:
`fyne package` still runs on macOS only, and packaging and signing are T4.24 to
T4.26.

Lint is the one job still red, and deliberately left so: it has never run either,
and it finds eight hundred and one things — four hundred of them unchecked errors
in production code, most of which are deferred Closes, and one of which is a real
violation of the project's most important structural rule (`internal/app`
importing `internal/ui`). That is a milestone of its own rather than a paragraph
of this one.

## Alternatives

**Raising the relaxation's pass count.** Rejected: it is the same hope with a
larger number in it, and the failure mode is silent. A bound that is exceeded
should hand over to something that cannot fail, not try a bit harder.

**Skipping the two Unix-mode tests on Windows.** Rejected: a skip is a test that
stopped running, and nothing would have said so. Asserting the platform's own
answer means the day Windows grows a mode this check will fail and somebody will
read it.

**Excluding Windows from CI.** Rejected. It found two real defects — one in the
gcloud lookup and one in what a connection-string refusal says — in its first
run, on a platform NFR-D3 promises.
