# ADR-0171: What one machine cannot see

**Status:** Accepted · **Date:** 2026-09-30
**Tasks:** T4.30, T4.32 · **Requirements:** NFR-P1, NFR-P8, NFR-Q3, RISK-10
**Packages:** `internal/store`, `internal/store/secrets`, `internal/tunnel`, `internal/ui/filedlg`, `internal/ui/shell`, `.github/workflows`

## Context

ADR-0169 was the first round of what CI found on platforms this project had only
ever been reasoned about. This is the second, plus the two jobs that were still
red for reasons of their own.

The pattern is the same one and worth naming: a codebase written on one machine
accumulates places where *this* machine's answer has been mistaken for the
rule. None of them are subtle once seen. All of them were invisible until
something else looked.

## Decisions

1. **A rule about another platform is spelled in that platform's terms.**
   `pathsFor` takes the operating system as an argument precisely so that every
   platform's conventions can be tested anywhere — and then asked
   `filepath.IsAbs` whether an XDG variable was absolute, which answers for the
   machine running the code. On Windows that wants a drive letter, so every
   valid `XDG_CONFIG_HOME` was called invalid. The XDG specification is a Unix
   specification and absolute there means a leading slash, which is now what the
   check is. Its test spells its paths as Unix paths for the same reason:
   `filepath.Join("/", "xdg")` asks the question in whatever the host happens to
   use.

   Nobody was affected, because production never asks for Linux's answer on
   Windows. The function was still wrong about what it claimed to do.

2. **A path that leaves a URI is spelled this platform's way.** Fyne's file
   dialog hands back a `fyne.URI`, and a URI holds forward slashes wherever it
   is: on Windows the answer for `C:\Users\ada\a.csv` is `C:/Users/ada/a.csv`.
   Windows opens either, so nothing failed at once — but that path was then
   shown to somebody, kept as a recent file and joined with other paths as
   though it were native. `closed` converts it. The claim is testable on any
   platform, which is what makes it worth a test: what comes back carries this
   system's separator and not the other one.

3. **A mode is asserted where a mode is what the system means.** Windows has no
   mode to read — a file's protection there is its ACL, inherited from the
   directory, and `os.Chmod` can only turn the read-only bit on and off, so every
   writable file reads as 0666. The secrets file's test asserted 0600 and failed.
   It now says what the platform means and asserts what can be asserted there:
   that the file is not read-only, because a secrets file that cannot be
   rewritten is one no secret can be changed in. This is ADR-0169's rule applied
   again — assert what the platform means rather than skip.

4. **A Unix constraint is a Unix constraint.** The SSH agent's socket goes under
   `/tmp` because `sun_path` has about a hundred characters and a directory named
   after a test uses most of them. That is Unix's limit, and Windows has no
   `/tmp` to put it in, so there the socket goes wherever the system keeps
   temporary files.

5. **A measurement is compared with a budget only where the measurement means
   something.** The cold-start gate timed one construction of the window in a
   fresh process, which pays for the toolkit as well — fonts read and measured,
   a theme resolved, icons rasterised, each once for the process and none of it
   this code. On an idle machine that is the difference between 130ms and 230ms;
   on a hosted runner with neighbours it was 523ms against a 200ms budget, on a
   commit that changed nothing about it, while the commit before — measuring the
   same distribution — passed. A gate that fails on somebody else's load teaches
   people to ignore the gate.

   It now builds the window twice, reports the first and holds the second, which
   is what the P3 gate already did for the same reason. The held number is four
   times smaller, so the budget is halved to match: the requirement's share is
   200ms, and holding the window's own build against that would let a doubling
   of this code's cost pass. NFR-P1's whole number still needs a process
   actually starting and a display actually drawing, which is GATE G0-1 on an
   attended machine.

6. **`bc` is not a thing a job may assume.** The binary-size job printed
   megabytes with `bc`, which Git bash on the Windows runner has not got, and the
   step ran under `-e` — so a job whose measurement was fine (97,078,272 bytes,
   well inside the 120 MB budget) failed on its own arithmetic. `awk` does it.

7. **The linter runs on every platform, because it only sees one.** The lint job
   had never run at all: the action's published binaries are built with an older
   Go than this module targets, and a linter cannot load a configuration for a
   language version it cannot parse. Building it with the Go the job already has
   fixed that, and the first run found three unwrapped errors in the XDG
   portal's file dialog — Linux-only code that no run on a Mac had ever looked
   at. `native_windows.go` and `native_darwin.go` are the same question asked
   the other way, so the job is a matrix of all three.

   Cross-linting with `GOOS` set is not the answer: DuckDB's bindings are cgo and
   do not type-check cross-compiled, so the whole run comes back as one import
   failure. For the same reason the job drops the `duckdb` build tag, which the
   configuration keeps so that a local run and the commit gate lint that driver
   where its library is.

## Consequences

Two of these are defects a user could have met: a Windows file dialog handing
back a path in the wrong spelling, and three errors in the Linux file dialog that
could not be reached through. The rest are a codebase learning the difference
between a rule and this machine's answer to it.

Two changes are only observable on Windows — the XDG absoluteness test and the
path conversion — so on this machine they are equivalent mutants, and CI is what
proves them. The rule each one encodes is locked by tests that do hold here: that
a relative or Windows-shaped XDG value is ignored, and that the path handed back
carries this platform's separator and not the other.

RISK-10 said "cross-platform build unproven" and the mitigation was always "CI
will tell us". It has now told us eleven things across two rounds, and the useful
observation is how few of them were about difficult code.
