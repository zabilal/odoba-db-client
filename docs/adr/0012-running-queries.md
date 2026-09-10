# ADR-0012: Running queries

**Status:** Accepted · **Date:** 2026-09-10
**Tasks:** T1.61–T1.64 · **Packages:** `internal/app` (`query.go`), `internal/ui/shell` (`query.go`)

## Context

The editor could edit, but nothing could run. The source contract already had
the pieces: `QueryMulti` checks every statement against the guard before any
runs (ADR-0005), and `Sessioner` pins a server session (ADR-0008). What was
missing was the layer between a script in an editor tab and rows in a grid.

## Decisions

**One pinned session per query tab.** `app.QuerySession` takes a `Session`
where the source has them and falls back to its `Queryer` where it does not.
So `SET`, temporary tables and an open transaction survive from one run to
the next. Closing the tab closes the session, off the UI goroutine.

**The app layer drains each result into memory, up to a cap.** Each result
set is read on a goroutine of its own, up to `MaxResultRows` (100 000,
NFR-P11), so the connection is free as soon as the rows are in. `Fetch` waits
until the rows it asks for have arrived, so a short page really is the end,
and the grid's end-of-data handling from ADR-0011 applies unchanged. A new
run closes the previous run's results first, because a session holds one open
result at a time.

**Offsets are bytes at the app boundary.** Sources report character offsets:
that is the contract, and PostgreSQL's splitter counts runes. The editor
positions by bytes. The app layer converts in both directions, in
`StatementAt` and `StatementResult.Offset`. Unconverted, any non-ASCII text
before the caret made ⌘↵ run the wrong statement. A mutation test strips the
conversion and fails.

**Production writes are a question, not an error.** The tab runs a script
unconfirmed. `QueryMulti` refuses it before any statement runs
(`ErrConfirmationRequired`). The tab then asks, and runs it again confirmed.
Asking is safe because nothing has executed in between.

**A finished script leaves its results streaming.** "Finished" means every
statement has returned a result, not that every row has arrived. Stop (⌘.),
the next run and closing the tab all end the reading. The first version
cancelled the run's context as the script ended, and that context also
governs the readers. A large `SELECT` showed its first rows and then
"Stopped". A plain test run passed by timing; the race detector's timing
exposed it. `TestRowsKeepArrivingAfterTheScriptEnds` pins it.

**Chords.** Run is ⌘↵ and runs the selection, or else the statement at the
caret. Run All is ⇧⌘↵, Stop is ⌘. and New Query is ⌘T. Open Data moved from
⌘↓ to ⌘O. A menu shortcut is tried before the focused widget, and on macOS
⌘↓ is the editor's "end of document". `view.Reserved()` lists the editor's
chords, and a shell test refuses any menu binding to one of them.

## Not decided here

Error positions in the editor (T1.68; each run carries its base offset for
it), parameters (T1.65), history (T1.66), saved queries (T1.67) and an
open-transaction indicator (FR-5.14).
