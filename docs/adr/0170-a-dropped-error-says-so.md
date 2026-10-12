# ADR-0170: A dropped error says so

**Status:** Accepted · **Date:** 2026-09-30
**Tasks:** T4.31, T4.32 · **Requirements:** ARCH-1, ARCH-4, ARCH-7, NFR-Q2, NFR-Q3, NFR-P12
**Packages:** `internal/app`, `internal/ui/shell`, `internal/testutil/errs`, and one line in about a hundred and fifty files

## Context

ADR-0168 made CI run, and ADR-0169 is what its test job said. This is what its
lint job said, which is the other half of the same discovery: the linter had
never run either.

It found eight hundred and one things. Almost all of them are one of five
questions asked several hundred times, and the work was to answer each question
once — in the contract, in the configuration, or at every site — rather than to
grind through a list.

## Decisions

1. **internal/app does not name a canvas type.** The first finding was
   depguard's, and it was the rule this project cares most about: ARCH-1 says
   the core stays usable with no UI dependency, and `internal/app/diagram.go`
   imported `internal/ui/canvas`. `ApplyLayout`, `LayoutOf` and `RestoreView`
   take canvas nodes, so they have moved to `internal/ui/shell`, with their
   tests. `internal/app` keeps `LayoutStore`, which is about a file on a disk.

   That this had to be found by a linter is the point. The rule was written down
   in ARCH-1, restated in the depguard configuration, and broken anyway, because
   nothing was running the configuration.

2. **A dropped error is written `_ = x()` at the site.** errcheck found six
   hundred and fifty-nine unchecked returns. Three hundred and nine of them are
   now marked this way: the drop is deliberate, it is visible to a reader, it is
   greppable, and errcheck still reports the next one that is not marked. The
   alternative — a configuration that excludes a whole category — buys silence
   for the code written next as well.

3. **Close on a row stream, a session or a source is a release.** By the time a
   caller closes one, whether the read worked has been reported by `Next` and by
   `Err`, and whether a write landed has been reported by the statement that
   made it or by `Commit`. There is nothing a caller can do with the error and
   nothing it could tell the person using the program. That is now written in
   `model.RowStream`, `source.Source` and `source.Session` where each declares
   `Close`, and the errcheck configuration names those three.

   It is not a general exemption for `Close`. A file being written is a different
   matter, because closing it is when the last of it reaches the disk.

4. **Deferred calls are exempt, because Go has no way to say otherwise.**
   `defer _ = x.Close()` is not valid Go, so the only ways to mark four hundred
   deferred releases are four hundred linter exceptions or one configured rule.
   The rule is configured, and the reason is that by the time a deferred call
   runs the function has already decided what it returns.

   The exemption's one real risk is a written file closed on the way out, so
   every such place in the tree was looked at before turning it off:
   `store.writeFileAtomic` closes explicitly and returns the error, the export
   path checks `Flush` on the writer that buffers the file, and the diagram and
   chart exports call `Sync` and check it while the error can still be reported.
   The deferred `Close` in each is after the data is already durable. That
   question is the one to ask of the next deferred close somebody writes.

5. **Identity is not `errors.Is`.** Every driver asserts that
   `classifyConnectError` hands back the error it was given for a failure
   already said in those terms, and for one it does not recognise. errorlint
   reads a comparison as a sentinel check and asks for `errors.Is`, which would
   accept a wrapper — so a classifier that wrapped its input instead of
   returning it would pass. Twelve such assertions now call
   `internal/testutil/errs.Same`, which is a comparison with the reason written
   above it, in one place instead of twelve.

6. **A linter that cannot see the difference is turned off with its reason, not
   silenced line by line.** ST1005 asks for error text that is a lowercase
   fragment ending without punctuation. Half of this codebase's flagged error
   text names a product — "Entra would not issue an access token" — and the rest
   is the sentence somebody reads in a dialog, written as a sentence on purpose.
   Three of staticcheck's quickfix checks propose forms that read worse here:
   expanding `!(a && b)` inverts every comparison in it, which is where
   precedence mistakes live; a switch nested in a switch arm is harder to read
   than the two-branch if it replaces; and naming an embedded field says which
   of two things is meant. misspell reads SQLite's `"DOUB"` type affinity as a
   clipped "DOUBT", and that is how SQLite's own documentation spells it.

7. **contextcheck is a review tool, not a gate.** ARCH-4 and NFR-P12 say every
   data-source call carries a context, and that is already enforced where it
   can be: `source.Source`'s methods all take one, so a driver that does not
   thread it does not compile, and the conformance suite proves each driver
   stops when its context is cancelled. What contextcheck adds is a demand that
   must be refused twice over — a window's callback fires long after the
   function that drew the button returned, and cleanup after a cancellation has
   to make a context that is *not* the cancelled one — and its demand is
   transitive, so threading one context makes it ask for one in every function
   that one calls, down to reading a local file. It was run once over the whole
   tree; its one real finding is fixed.

8. **The tagged suites are linted too.** A linter with no build tags reads a
   package as its untagged half, and the first run of this milestone was made
   that way. The commit gate said what that costs: `unused` reported a helper in
   an untagged test file that only a `conformance`-tagged file calls, the helper
   came out, and the tagged build stopped compiling. The configuration now names
   both tags, which found fifty-six more things in code that had never been
   linted at all — among them the depguard finding below, four more sentinel
   comparisons in the DuckDB driver, and fifty unchecked errors.

   The helper itself did not come back. It was `func jsonDecode(r io.Reader,
   into any) error { return json.NewDecoder(r).Decode(into) }` with one caller,
   which now decodes for itself.

9. **A driver's test does not import internal/app either.** ARCH-1 is about what
   the core depends on, and a `_test.go` file is not part of a package's
   importable surface — but `internal/source/drivers/postgres` had a live test
   that called `app.ScriptSchema`, and that test was never the driver's. Its
   claim is that ScriptSchema puts statements in an order a server accepts,
   which needs two tables referring to each other and two views whose build
   order is the opposite of their names. It has moved to `internal/app`, where
   depending on `internal/app` is what the package is, and it now asks for the
   fixture schema rather than relying on another suite having run first.

10. **The linter is built in CI rather than downloaded.** The lint job had never
   run at all, for a reason that had nothing to do with the code: the action's
   published binaries are built with an older Go than this module targets, and a
   linter cannot load a configuration for a language version it cannot parse.
   The job now builds golangci-lint with the Go it already has, at a pinned
   version, so that our language version is not in somebody else's release
   schedule and a linter release cannot turn CI red on a morning nobody changed
   anything.

## Consequences

Two defects came out of this, both in code no test reached:

- A Kafka fetch scanned its partition errors in a loop that returned on its
  first turn, so a cancellation sitting behind a real failure was reported as
  the failure — and a caller reads its own stop from a cancellation. The scan is
  now `firstFault`, a function over a list of errors, so which one gets reported
  is provable without a cluster.
- `ikigai query --saved` opened the local database with `context.Background`, so
  an interrupted run did not stop reading.

Eleven production errors are now wrapped with `%w` and so can be reached
through; six comparisons that a wrapped sentinel would have slipped past now go
through `errors.Is`. Fourteen declarations nothing referred to are gone, which
is the house rule about unprovable lines arrived at from the other direction.

Four more sentinel comparisons against `sql.ErrNoRows` in the DuckDB driver go
through `errors.Is`. Like the two in `internal/transfer`, these are equivalent
mutants and are recorded as such rather than given a test that cannot fail:
`database/sql` returns `ErrNoRows` bare, so nothing in the tree can produce a
wrapped one, and `errors.Is` is the same answer today that stays right if a
driver ever wraps it.

Four lines the linter's fixes touched turned out to have no test behind them,
and the mutation run is what said so. Each now has one: which of a poll's errors
is reported; that the value a column cannot hold is named by its column and
keeps what was wrong with it; every arm of the sentence saying how far behind a
consumer group is, including the two that are singular; and that a filter about
the cluster is not a filter about everything — the last provable only against a
broker, because what tells kadm's "any resource" from its "the cluster" is what
a cluster answers.

Lint is the last of CI's jobs to go green, and the reason it took a milestone
rather than a paragraph is that most of the eight hundred findings were one
question about the codebase's own contracts, asked several hundred times.
