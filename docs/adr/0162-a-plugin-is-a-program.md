# ADR-0162: A plugin is a program

**Status:** Accepted · **Date:** 2026-09-25
**Tasks:** T5.10 · **Requirements:** FR-16.2, REQ-DB-1, REQ-DB-2, NFR-S1, NFR-S2, NFR-S4, NFR-D6, ARCH-1, ARCH-4
**Packages:** `plugin`, `internal/plugin`, `examples/csvdir`, `internal/store`

## Context

FR-16.2 asks for a plugin SDK for third-party sources and file formats, "Go
plugin interface or subprocess". The requirement offers the choice; the
circumstances do not.

## Decisions

1. **A plugin is a program, not a shared library.** Go's `plugin` package works
   on Linux and macOS and not on Windows; it refuses to load anything not built
   with the same toolchain version and byte-identical dependency versions; it
   cannot be used with the `-trimpath` the release build uses; and a panic inside
   one takes the application down with it. Any one of those would be
   disqualifying. A program has none of them, can be written in any language, and
   can be killed — which is what cancelling something ultimately means (ARCH-4).

2. **The protocol is the interface; the Go package is a convenience.** JSON
   objects, one a line, on standard input and output. A line is what every
   language has a reader for, and a plugin somebody can write in twenty lines of
   Python is a plugin somebody will write. `docs/PLUGINS.md` is the
   specification; `plugin.Serve` is for the Go case and imports nothing of this
   application's, because a plugin is a separate module and cannot import
   `internal/...` at all.

3. **Nothing runs unless somebody turned it on.** A program that ran because it
   was in a folder is a program nobody chose. The settings have no plugins
   section until somebody writes one, which is the whole of "off" — the same
   shape as the assistant's (ADR-0160).

4. **A plugin is trusted with the connections it is used for, and that is said
   out loud.** Opening a connection sends the plugin its settings and its
   secrets, because that is what connecting needs; there is no version of this
   that gives it less. So `docs/PLUGINS.md` and the README say it in those words,
   and the keychain is still the only place a secret is kept (NFR-S1).

5. **Everything a plugin says is data, and none of it is believed.** A node with
   no path is dropped rather than drawn. A row longer than its columns is cut and
   a shorter one padded, because a grid with a ragged row panics. A line that is
   not JSON is logged and ignored. An answer to a request nobody is waiting for
   is dropped. None of this is hypothetical politeness: the first plugin written
   against a new protocol gets something wrong, and the application must be the
   thing that survives it.

6. **A row with no values is refused rather than dropped.** The line it would
   make cannot be told from a line with no row in it, and the host guessing would
   be the host guessing about somebody's data. The plugin author is told, where
   they can fix it.

7. **One goroutine writes to a plugin, and nothing waits on a plugin that has
   stopped reading.** A write to a pipe blocks until the far end reads, so a
   plugin that has hung would otherwise hang whoever was talking to it — and one
   of those is the goroutine that draws the window. Requests queue and time out;
   a cancel is best-effort and never waits at all. This was a real defect, found
   by a test that cancelled a request against a plugin that had stopped reading.

8. **A plugin finishes the answer it is writing, and gives up on the rest.** The
   input ending means the host has finished, so `Serve` cancels everything in
   flight and then waits for it. Both halves were defects found by mutation:
   without the waiting, a host that closed a plugin's input to ask it to stop
   lost the answer to whatever was in flight, which reads at the other end as a
   plugin that died mid-request; without the cancelling, a handler that ignored
   its context kept the plugin alive for as long as it took, or for ever.

9. **A plugin that will not stop is killed.** Its input is closed first, which is
   how a program is asked; two seconds later it is killed, because an application
   that cannot shut down because of a plugin is an application a plugin can hang.

10. **Version 1 takes no statements.** A query editor is not a "run this" call: it
   is splitting a script, classifying each statement so a read-only connection
   can refuse the ones that write, quoting identifiers, and completing from the
   schema. A protocol that answered only "run this" would be asking the
   application to trust a plugin with the one guard that exists to be untrusting
   (NFR-S4). Browsing is the whole of the data path, which is the required one for
   every source anyway (ADR-0005). Nothing is claimed that is not there: the
   capability for statements is not in the protocol, so a plugin cannot claim it.

11. **Version 1 writes nothing either,** and answers no badges: the count beside a
    tree node is the expensive question, asked once per visible node, and a plugin
    answering it over a pipe would be the slowest part of the tree.

12. **A plugin registers like any other driver.** `source.Register` with a
    `Driver` whose `Describe` comes from the handshake, so a plugin source appears
    in the new-connection picker with no UI change at all (REQ-DB-1). A plugin
    claiming a driver name the application already has is refused, and said.

13. **One process serves every connection to that driver.** A handle names each
    one. Closing a connection tells the plugin and leaves it running; the process
    goes when the application does.

14. **The command line loads them too.** A source somebody added is a source, and
    a pipeline that can browse one should be able to export from it — with the
    same switch, because a plugin turned on in one and off in the other would be
    a difference nobody could account for.

15. **The plugin host is on the offline list.** It runs a program somebody
    installed; it does not fetch one. There is no plugin directory to download
    from and no plugin manager, and a host that could reach the network would be
    the beginning of both (NFR-D6).

## Consequences

The worked example — `examples/csvdir`, a folder of CSV files browsed as a
database — is about two hundred lines, most of them reading CSV, and it is built
and run for real by a test. What that test proves is the process-shaped part: the
pipes, the handshake, the shutdown. The rest is proved over a pair of pipes with a
plugin written in the test process, which is how a plugin that answers nonsense —
a row longer than its columns, a node with no path, a line that is not JSON, an
answer to a question nobody asked — is exercised without writing a program for
each. A second test plugin under `testdata` misbehaves on purpose: it exits
before saying anything, writes something that is not the protocol, says hello and
then ends, says hello and then ignores everything including being told to stop,
behaves differently depending on where it was installed, or writes a row before
saying what its columns are.

Three defects came out of holding the tests to the mutations, and all three were
in the shutdown: writing to a plugin that had stopped reading blocked the caller
(decision 7), an answer in flight was lost when the input ended, and then the fix
for that could keep a plugin alive for ever (decision 8). None of the three was
reachable by reading the code; each was found by removing a line and asking which
test noticed.

What is not here: no plugin for a file format (the other half of FR-16.2 — the
protocol's `kind` field exists for it and nothing implements it), no way to
install or remove one from the window, no signing or verification of what is
installed, and no writing. Each is honest work left undone rather than a decision
against it; the file-format half is the one a pipeline would want next.

## Alternatives

**Go's `plugin` package.** Rejected by decision 1, on four independent grounds.

**A Go interface compiled in, with plugins as forks of the application.**
Rejected: that is not a plugin system, it is a patch.

**gRPC, or HashiCorp's go-plugin.** Rejected. Both are better engineering for a
larger problem and both cost a dependency tree and a code generator; a plugin
author would need the toolchain to write one, and "any language" becomes "any
language with the right generated stubs". JSON lines cost one standard-library
decoder at each end.

**Loading whatever executable is found in the folder.** Rejected: a README and a
licence sit beside a program, and running everything in a directory is how a data
file becomes an execution. The program is called `plugin` and nothing else is run.

**Letting a plugin declare that it handles statements, with the host trusting its
classification.** Rejected by decision 10. The guard is the one thing that cannot
be delegated to code we did not write.
