# ADR-0011: The shell, where UI work runs, and tables without a row count

**Status:** Accepted · **Date:** 2026-09-10
**Tasks:** T1.1–T1.12, T1.24, T1.41 · **Packages:** `internal/ui/shell`, `internal/ui/uithread`,
`internal/ui/explorer/view`, `internal/ui/grid`, `internal/e2e`, `cmd/ikigai`

## Context

Everything below the UI existed (ADR-0008–0010) but nothing opened a window.
Wiring it to Fyne forced four decisions that the code alone does not explain.

## 1. UI work goes through an injected Runner

Fyne wants widget work on its main goroutine, via `fyne.Do`. Calling `fyne.Do`
from a data callback is right in the app and wrong under test: Fyne's test
driver runs the function at once, on the calling goroutine, so a page-load
callback ends up refreshing widgets from a fetch goroutine. The race detector
caught exactly that in the explorer (text shaping from a timer goroutine).

**Decision.** Code that schedules UI work takes a `uithread.Runner`
(`func(func())`). The app passes `uithread.Fyne`; tests pass a
`uithread.Queue` and drain it on the test goroutine, which then stands in for
the UI goroutine. `uithread.Coalesce` turns a burst of triggers into one run
and replaces the grid's own flag (ADR-0002).

Every asynchronous result in the shell arrives through the Runner and checks
its tab's context first, so a result that lands after its tab closed is
dropped rather than drawn into a detached widget.

## 2. Every shortcut is a menu item, and only that

From Fyne 2.8.1's desktop driver:

- On a key press the window tries the main menu's shortcuts *before* the
  focused widget, on every platform. A focused `Entry` swallows every shortcut
  it does not own, so a shortcut registered on the canvas is dead whenever a
  text field has focus. A menu shortcut is not.
- On macOS the native menu owns its key equivalents. Cocoa hands the event to
  the menu and never to GLFW's `keyDown`, so it fires once. Registering the
  same chord on the canvas as well would add a second path; we do not.
- Fyne's menu-shortcut matching calls an item's `Action` even when the item is
  `Disabled`. So every menu action goes through `Registry.Run`, which checks
  `Enabled` again.
- The native macOS menu reads `Disabled` as it opens, with no hook to compute
  it then. The shell recomputes enabled and checked state on each selection,
  tab and connection-status change, and refreshes the menu only if something
  changed.

**Decision.** The menu bar is declared as command IDs. A test fails any
command that has a shortcut but no menu item, and any two commands sharing a
chord. Phase 1's chords: ⌘K palette · ⌘N new connection · ⌘W close tab ·
⌘R refresh sidebar · ⇧⌘R reload rows · ⌘↓ open data (Finder's Open) ·
⌘0 toggle sidebar · ⇧⌘] and ⇧⌘[ next and previous tab. Off macOS, ⌘ is Ctrl.

## 3. A table with no row count grows as it is read

PostgreSQL reports no exact count, because counting is a full scan
(`Data.ExactCount` is false), so a browse's count is -1. The grid sized itself
by resident pages until a count arrived. With nothing resident that is zero
rows, so no cell was drawn, so no row was asked for, so no page was fetched:
**every real PostgreSQL table opened empty.** The grid's synthetic fetcher
always counts, which hid it. The J1 journey test below would have caught it.

**Decision.**

- The model tracks how far fetches have reached. A short page is the end of
  the data (the `Fetcher` contract). It makes an unknown total known, and
  corrects a known one that disagrees.
- With the total unknown, the grid shows the rows loaded so far plus one page
  of placeholders. Drawing those placeholders is what fetches the next page.
- `Invalidate` keeps a counted total until the next count replaces it, so
  Reload Rows does not collapse the grid to one page and lose the scroll
  position. If a filter shrank the data, the first short page corrects the
  total sooner. A total known only from reaching the end is dropped.
- The tab's footer says "1,024+ rows" until the end is found, then the count.

**Consequence.** An uncounted table's scrollbar is not proportional until the
end is reached. The estimate the sidebar already shows (`~1.2M`, from
`reltuples`) could size it provisionally. That is deferred until the grid can
mark an estimate as one, because an estimate must never pass for exact.

## 4. The connection form is data

The form's fields come from the driver's `Descriptor`. Host, port, database
and user map to the saved connection the way `connstr` maps them. Secret
fields go to the keychain through `app.Connections`, and anything else goes
into `Params`. Blank fields take the driver defaults their placeholders show.

A pasted URL fills the form and is then cleared, because it may hold a
password, which belongs in the masked field. Parameters that have no field
survive a save. Editing never reads a saved secret back into the form, and a
blank password keeps the saved one.

## 5. Testing without a display

Shell tests run on Fyne's test driver, with a Queue as the Runner. Their fake
driver registers as `postgres` so pasted URLs resolve to it, because depguard
keeps real drivers out of `internal/ui`. `internal/e2e` holds journey tests
over real drivers. J1 (tag `conformance`) walks the sidebar to a table on
PostgreSQL, runs Open Data, and waits for the footer's row count. For
PostgreSQL that count can only come from rows actually fetched.

## 6. Reviewing without a window server

`internal/e2e`'s `TestScreenshots` drives the real shell over a SQLite
fixture and renders its main states to PNG files, through Fyne's software
painter and the real theme. Run it with
`IKIGAI_SCREENSHOTS=dir go test -run TestScreenshots ./internal/e2e/`.
It is opt-in, so the ordinary run writes nothing.

Its first run found six problems, one of them a crash:

- **The find bar's fields had negative widths.** The bar was laid out while
  hidden, and showing it re-laid out only the bar, still zero wide, not the
  container that holds it. The fields were unusable, and the software
  painter crashed on them. It now re-lays out its parent when shown or
  hidden, and a test checks both fields get room.
- **The match count drew clipped** ("4" for "4 matches"), for the same
  underlying reason: a label that grows does not make its container re-lay
  out. The count now has a fixed width, which also keeps the buttons still.
- **The grid showed the window through it** around and between cells; it now
  has its own content background.
- **Alternate-row stripes drew as grey blocks,** because a cell can tint only
  its own width. Rows are plain until stripes can be drawn at full width
  (T1.50).
- **The status line read "local —"** when connected; it now says
  "Connected".
- **The tree used arrows** for disclosure, and now uses the chevrons the
  design system asks for (ADR-0006).

## 7. An error is a band across the top of the window

An error that is not a form's own answer is said in a band across the top
of the window, over the sidebar and the tabs alike (FR-15.7): its first line, an action where there is one, Details for the
rest, Copy for all of it, and a close button. It replaces Fyne's error
dialog, which was modal, covered the data and could not be copied, against
UX principle 4. A later error replaces an earlier one, and the band
re-divides the window as it opens, as the WHERE bar does (ADR-0016). An
error inside a panel that covers the window, such as Saved Queries, is said
in that panel, where it can be seen. A test fails if the modal error dialog
comes back. The band's tint is drawn, not themed, so an appearance change
repaints it: the first version kept the dark tint under light-mode text,
and could not be read.

## 8. Tabs move and pin, by command

Tabs move left and right, and pin, from the Window menu (FR-15.2). Pinned
tabs stay together at the left, in the order they were pinned, marked with
a pin, and a tab moves only among the tabs of its own kind; new tabs open
after the pinned ones. Fyne's tab bar cannot be dragged, so moving is by
command. Dragging a tab needs a tab bar of our own, and is open.

## 9. Unsaved query text is kept as it is typed

A query tab's text that is saved nowhere else is written to the local
database within a second of an edit (NFR-R2). Writes are throttled, not
debounced, so steady typing is still kept every second. The text lives in
the key-value table under `scratch/`, one entry per tab. One goroutine
writes the entries in turn, the latest word on each replacing any not yet
written, so text forgotten after a save is never written back by a slower,
earlier write.

Saving forgets the text, as does closing the tab on purpose or deleting its
connection, whose dialog says the tabs close. Quitting and a disconnect keep
it, and so does a crash, up to the last second. At the next start each kept
buffer reopens as a tab on its connection, still marked unsaved, and edits
to a saved query still save in place. Text whose connection has gone
reopens in a tab that does not connect and says why: it is never thrown
away unseen. An unreadable entry is left where it is and named, and a
failed write is said once in the error band. Only unsaved query text comes
back at the next start; the rest of the session (T1.14) does not yet.

## Not decided here

Single-instance handling (T1.1), tab reorder and pinning (T1.4), custom key
bindings (T1.8), the task centre (T1.10) and a copyable error sheet (T1.11)
are still open. When a connection has no saved password, the tab offers
"Edit Connection…". A proper prompt with a "remember" choice is still to be
designed. Nothing here has been looked at in a real window yet; the first
`go run ./cmd/ikigai` is the first visual review.
