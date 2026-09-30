# ADR-0168: What ships

**Status:** Accepted · **Date:** 2026-09-30
**Tasks:** T4.30, T4.36, GATE G0 · **Requirements:** NFR-P8, NFR-S9, NFR-Q1, NFR-Q3, RISK-5
**Packages:** `.github/workflows`, `internal/testutil/race`, `internal/ui/explorer/view`

## Context

Three things were open at once, and they turn out to be the same question asked
three ways: what leaves this machine, and what proves it.

NFR-P8 budgeted the binary at 60 MB. A stripped build is 86.6 MB, and three
drivers are most of the difference — go-ora 14.8, franz-go 10.4, aws-sdk-go-v2
5.1, leaving 55.8 without them. The budget could only be met by shipping some
drivers and not others, and that was the owner's to settle.

OQ-1 asked for a licence. The repository had none, which for a project meant to
be used by other people is not a neutral state.

And the oldest open gate item said "conformance suite and CI written but never
run — the repository has no remote". A remote arrived with the first pull
request. Nothing updated that line, so it went on saying CI had never run while
CI had been running, and failing, on every push for four days.

## Decisions

1. **Every driver ships.** Installing the application is the whole of installing
   support for the databases it speaks: a person who has a Kafka cluster and an
   Oracle instance should not have to discover that the build they downloaded
   cannot see one of them. So no driver goes behind a build tag for size, and
   DuckDB stays behind one only because it needs a C toolchain (ADR-0146).

2. **The binary budget becomes a ceiling against accidental growth.** 120 MB,
   and it fails the build. If the binary *is* the drivers then a budget forbidding
   their size forbids the product, and the number stops meaning anything — which
   is what had happened: it had been raised twice in a day, each time for a real
   addition, which is a ratchet documenting growth rather than a budget limiting
   it. What makes the new number honest is the other half of the requirement:
   every source added says in TASKS.md what it measured. The total is then an
   accumulation of named decisions, and a build that grew without one of them is
   exactly what the ceiling catches.

3. **Apache 2.0.** Permissive, so the licence does not reach the code of a
   company that uses the tool, and with a patent grant, which something speaking
   fifteen database protocols is worth having. The text is copied verbatim from a
   dependency that ships it rather than transcribed, because a licence with a
   typo in it is a different licence. NOTICE carries the project's own copyright
   and says in one line that no code came from DBGate or any other client, with
   CLEANROOM.md for what was used instead (RISK-5).

4. **The gate and CI prove different things, and both are needed.** The local
   gate has twenty-one database servers and no other platform; CI has three
   platforms, the race detector, and no room for twenty-one servers. So: live
   drivers and the end-to-end journeys are the gate's, because a hosted runner
   cannot hold them; Linux and Windows builds, `-race`, the performance budgets
   and the binary's size are CI's, because this machine is one platform and does
   not run the detector. Neither is the whole of the verification, and a red one
   is not a thing to leave for later — which is the lesson of this commit.

5. **A deadline in a test scales under the detector; a budget declines to run.**
   The detector slows execution 5-20x. A budget measured under it measures the
   instrumentation, so the timing gates skip (`race.SkipTimingGate`, unchanged).
   But a deadline is not a budget: it is a statement that something should have
   happened by now — a plugin answered, a queue drained — and under the detector
   the same work honestly takes longer. `race.Slower` moves those, so they go on
   meaning what they said.

6. **What a workspace narrows is behind a lock.** The explorer's loader held
   `Shows` as a plain field, written when a workspace was switched — on the
   goroutine that draws the window — and read while the tree loaded, which
   happens on goroutines of its own. The detector was right about it. It is a
   method and a mutex now, and the test that holds it narrows the tree in a loop
   while loading it in another, because the window a real switch opens is a
   moment wide and not worth waiting for.

## Consequences

CI is green and is now the thing to read after a push. Four faults came out of
looking at it: the Linux build could not compile at all, because glfw builds its
Wayland backend and the apt list had only the X11 headers; six data races, five
in test fakes and one real; four plugin tests failing on deadlines that measured
the detector; and the nightly conformance run had no MongoDB replica set to give
T5.12's change streams, which it starts in a step now — a service container
cannot be initiated after it starts, and a replica set must be.

The binary is 86.6 MB and will grow: the sources still to come each cost
something, and each will say what. The ceiling is high enough not to be moved
for a driver and low enough that doubling would fail.

What this does not do: it does not package or sign anything, so `fyne package`
is still proven on macOS alone (T4.24 to T4.26), and CI builds the three
platforms without producing an installable artefact for two of them.

## Alternatives

**Build tags for the three largest drivers.** Rejected by the owner, and the
reason is the product: two build shapes to keep working, and a user who has to
know which build has Oracle in it before downloading. The size is the cost of not
asking them that.

**Raising the budget to 90 MB.** Rejected as the same fiction one notch up: the
binary was already 86.6, and the next source would have moved it again. A number
that moves for every planned addition is not a budget.

**Running the live servers in CI.** Rejected: twenty-one containers do not fit a
hosted runner, and the eight that do are already there. The gate is where the
rest are proven, on a machine that has them.
